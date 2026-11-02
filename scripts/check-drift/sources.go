package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// preambleHeading names the text above a page's first heading.
const preambleHeading = "(before the first heading)"

// digestLength keeps a digest short enough to read in a diff. A collision would
// hide one edit to a changelog, which the next edit would reveal.
const digestLength = 12

// readPage fetches one Markdown page and splits it. Anything else is an error,
// because a page that cannot be read is not a page with no changes: vendors
// answer a moved URL with a 200 of HTML as readily as with a 404.
func readPage(ctx context.Context, client *http.Client, pageURL string) ([]section, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if bytes.HasPrefix(bytes.ToLower(bytes.TrimSpace(body)), []byte("<")) {
		return nil, errors.New("the page came back as HTML rather than Markdown")
	}
	sections := parseSections(string(body))
	if !slices.ContainsFunc(sections, func(s section) bool { return s.Heading != preambleHeading }) {
		return nil, errors.New("the page has no headings, so it is not the page this expects")
	}
	return sections, nil
}

// readPages reads every page, recording each failure as a gap.
func readPages(ctx context.Context, client *http.Client, pages []page, o *observation) {
	for _, p := range pages {
		sections, err := readPage(ctx, client, p.URL)
		if err != nil {
			o.gaps = append(o.gaps, gap{Check: checkChangelog, Name: p.Name, Reason: p.URL + ": " + err.Error()})
			continue
		}
		o.changelogs[p.Name] = sections
	}
}

// parseSections splits a Markdown page by heading. Each section is named with its
// parents, since "Oct 30" recurs every year. Its digest covers that name and its
// words with whitespace normalized, so reflowing a paragraph is not an edit.
func parseSections(text string) []section {
	var out []section
	var stack []string
	var body []string
	heading := preambleHeading
	flush := func() {
		if heading == preambleHeading && len(body) == 0 {
			return
		}
		words := strings.Join(body, "\n")
		sum := sha256.Sum256([]byte(heading + "\n" + words))
		out = append(out, section{Digest: hex.EncodeToString(sum[:])[:digestLength], Heading: heading, Text: words})
		body = nil
	}
	fenced := false
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(nil, 16<<20)
	for scanner.Scan() {
		line := strings.Join(strings.Fields(scanner.Text()), " ")
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		level, title := headingOf(line)
		if fenced || level == 0 {
			if line != "" {
				body = append(body, line)
			}
			continue
		}
		flush()
		stack = append(stack[:min(level-1, len(stack))], title)
		heading = strings.Join(stack, " > ")
	}
	flush()
	return out
}

// headingOf reads an ATX heading, returning level zero for any other line.
func headingOf(line string) (int, string) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(line[level:])
}

// catalogBody covers both catalog shapes: data[].id, with Anthropic's paging, and
// TypeSafe's models[].name.
type catalogBody struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// readCatalog lists one catalog's ids, sorted and filtered. An empty list is an
// error: no vendor sells nothing, and some send an error with a 200.
func readCatalog(ctx context.Context, client *http.Client, spec catalogSpec, key string) ([]string, error) {
	var ids []string
	after := ""
	for {
		page, err := readCatalogPage(ctx, client, spec, key, after)
		if err != nil {
			return nil, err
		}
		for _, d := range page.Data {
			ids = append(ids, d.ID)
		}
		for _, m := range page.Models {
			ids = append(ids, m.Name)
		}
		if !page.HasMore || page.LastID == "" {
			break
		}
		after = page.LastID
	}
	if len(ids) == 0 {
		return nil, errors.New("the catalog came back with no models")
	}
	if len(spec.Prefixes) > 0 {
		ids = slices.DeleteFunc(ids, func(id string) bool {
			return !slices.ContainsFunc(spec.Prefixes, func(p string) bool { return strings.HasPrefix(id, p) })
		})
		if len(ids) == 0 {
			return nil, fmt.Errorf("the catalog has none of %v", spec.Prefixes)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// readCatalogPage fetches one page of a catalog.
func readCatalogPage(ctx context.Context, client *http.Client, spec catalogSpec, key, after string) (catalogBody, error) {
	target, err := url.Parse(spec.URL)
	if err != nil {
		return catalogBody{}, err
	}
	if after != "" {
		query := target.Query()
		query.Set("after_id", after)
		target.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return catalogBody{}, err
	}
	if spec.Header != nil {
		req.Header = spec.Header(key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return catalogBody{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return catalogBody{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return catalogBody{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var page catalogBody
	if err := json.Unmarshal(body, &page); err != nil {
		return catalogBody{}, fmt.Errorf("the catalog is not the expected JSON: %w", err)
	}
	return page, nil
}

// readCatalogs reads every catalog, recording a missing key or a failure as a gap.
func readCatalogs(ctx context.Context, client *http.Client, specs []catalogSpec, keys map[string]string, o *observation) {
	for _, spec := range specs {
		key := keys[spec.KeyEnv]
		if spec.KeyEnv != "" && key == "" {
			o.gaps = append(o.gaps, gap{Check: checkCatalog, Name: spec.Name, Reason: "no " + spec.KeyEnv + " in the env file"})
			continue
		}
		ids, err := readCatalog(ctx, client, spec, key)
		if err != nil {
			o.gaps = append(o.gaps, gap{Check: checkCatalog, Name: spec.Name, Reason: err.Error()})
			continue
		}
		o.catalogs[spec.Name] = ids
	}
}

// memberPaths lists the member paths of a JSON body or an SSE stream, with [] for
// an array. It reads names only. A body that is neither is an error.
func memberPaths(body []byte) (map[string]bool, error) {
	paths := map[string]bool{}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return paths, walkJSON(trimmed, paths)
	}
	frames := 0
	for line := range bytes.Lines(trimmed) {
		data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if err := walkJSON(data, paths); err != nil {
			return nil, err
		}
		frames++
	}
	if frames == 0 {
		return nil, errors.New("the body is neither JSON nor an event stream")
	}
	return paths, nil
}

// walkJSON adds one JSON value's member paths.
func walkJSON(data []byte, paths map[string]bool) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	walkValue(value, "", paths)
	return nil
}

// walkValue records each member beneath a value. Members in dataMembers hold the
// caller's own data, whose names say nothing about the wire.
func walkValue(value any, path string, paths map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for name, member := range v {
			if keyedMembers[lastName(path)] {
				name = "*"
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			paths[child] = true
			if !dataMembers[name] {
				walkValue(member, child, paths)
			}
		}
	case []any:
		for _, element := range v {
			walkValue(element, path+"[]", paths)
		}
	}
}

// lastName is the final member of a path, as written.
func lastName(path string) string { return path[strings.LastIndexByte(path, '.')+1:] }

// recordedPaths unions the member paths of every recording the globs match, never
// a recorded request, which is ours rather than the vendor's. Matching nothing is
// an error, since an empty set would call every member new.
func recordedPaths(root string, globs []string) (map[string]bool, error) {
	paths := map[string]bool{}
	files := 0
	for _, glob := range globs {
		matches, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			return nil, err
		}
		for _, file := range matches {
			if strings.Contains(filepath.Base(file), ".request.") {
				continue
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			found, err := memberPaths(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			for path := range found {
				paths[path] = true
			}
			files++
		}
	}
	if files == 0 {
		return nil, fmt.Errorf("no recordings match %v", globs)
	}
	return paths, nil
}

// modeledNames collects every JSON member name on the structs of the given
// packages, reading the source because the wire structs are unexported. It is a
// name check, not a path check: enough to tell a member nothing reads from one
// that has merely moved. A directory with no Go files is an error.
func modeledNames(dirs ...string) (map[string]bool, error) {
	names := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
			if err != nil {
				return nil, err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if field, ok := node.(*ast.Field); ok && field.Tag != nil {
					addTag(field.Tag.Value, names)
				}
				return true
			})
		}
	}
	return names, nil
}

// addTag adds the JSON name in a raw struct tag.
func addTag(raw string, names map[string]bool) {
	tag, err := strconv.Unquote(raw)
	if err != nil {
		return
	}
	name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
	if name != "" && name != "-" {
		names[name] = true
	}
}
