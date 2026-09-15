package goodall

import (
	"errors"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// modulePath is the import path of this module; subpackages live under it.
const modulePath = "github.com/bensyverson/goodall"

// stdOnly lists the packages, relative to the module root, that may import
// nothing outside the standard library. The root is the zero-dependency core
// every other package types against; the SSE framer is shared by both
// providers and must stay just as light.
var stdOnly = []string{".", "internal/sse"}

// errNoPackage reports a directory that holds no Go package yet.
var errNoPackage = errors.New("no Go package")

// violation is one import that breaks a layering rule.
type violation struct {
	Package string // package directory, relative to the module root
	Import  string
	Rule    string
}

// isStandardImport reports whether path names a standard-library package.
// The go tool's own rule: a standard import path has no dot in its first
// element. "C" (cgo) is excluded explicitly so it can never pass as std.
func isStandardImport(path string) bool {
	if path == "C" {
		return false
	}
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// checkImports applies the layering rules to the package in dir and returns
// every violation. In-package test files are held to the same rules as
// production files; an external test package (package foo_test) may import
// this module's subpackages, since that is how end-to-end tests reach the
// providers, but it too may not import outside the standard library.
func checkImports(root, rel string) ([]violation, error) {
	dir := filepath.Join(root, rel)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, errNoPackage
	}
	pkg, err := build.Default.ImportDir(dir, build.IgnoreVendor)
	if err != nil {
		if _, ok := errors.AsType[*build.NoGoError](err); ok {
			return nil, errNoPackage
		}
		return nil, err
	}
	var out []violation
	// self is this package's own import path, which its external test
	// package imports to reach the API it is testing. That is the package
	// under test rather than a dependency, so it breaks no rule.
	self := modulePath
	if rel != "." {
		self = modulePath + "/" + rel
	}
	check := func(imports []string, external bool) {
		for _, imp := range imports {
			switch {
			case imp == self && external:
			case strings.HasPrefix(imp, modulePath+"/"):
				if rel == "." && !external {
					out = append(out, violation{rel, imp, "the root package must not import a subpackage"})
				}
			case !isStandardImport(imp):
				out = append(out, violation{rel, imp, "only the standard library is allowed"})
			}
		}
	}
	check(pkg.Imports, false)
	check(pkg.TestImports, false)
	check(pkg.XTestImports, true)
	return out, nil
}

// TestDependencyDirection is the guard for the plan's "zero dependencies"
// decision and its layering: the core and internal/sse import only the
// standard library, and the core never imports one of its own subpackages.
func TestDependencyDirection(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range stdOnly {
		violations, err := checkImports(root, rel)
		if errors.Is(err, errNoPackage) {
			// Announced rather than silent: a package that has not been
			// written yet has nothing to guard, but a renamed one would
			// show up here as a permanent skip.
			t.Logf("%s: no package yet, nothing to check", rel)
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for _, v := range violations {
			t.Errorf("%s imports %q: %s", v.Package, v.Import, v.Rule)
		}
	}
}

// TestCheckImportsCatchesViolations is the mutation proof for the guard: a
// synthetic root package that imports a third-party module and a subpackage
// must produce exactly those two violations, and an external test file that
// imports a subpackage must not.
func TestCheckImportsCatchesViolations(t *testing.T) {
	root := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package goodall\n\nimport (\n\t_ \"fmt\"\n\t_ \"github.com/example/dep\"\n\t_ \""+modulePath+"/anthropic\"\n)\n")
	write("a_test.go", "package goodall\n\nimport _ \"testing\"\n")
	// The external test file imports the package under test, which must not
	// count, alongside a subpackage, which is allowed there, and a
	// third-party module, which is not.
	write("b_test.go", "package goodall_test\n\nimport (\n\t_ \""+modulePath+"\"\n\t_ \""+modulePath+"/anthropic\"\n\t_ \"golang.org/x/tools/present\"\n)\n")

	violations, err := checkImports(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range violations {
		got = append(got, v.Import)
	}
	slices.Sort(got)
	want := []string{"github.com/example/dep", modulePath + "/anthropic", "golang.org/x/tools/present"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("violations = %q, want %q", got, want)
	}
}

func TestIsStandardImport(t *testing.T) {
	cases := map[string]bool{
		"fmt":                     true,
		"encoding/json/v2":        true,
		"net/http/httptest":       true,
		"C":                       false,
		"github.com/x/y":          false,
		"golang.org/x/tools":      false,
		modulePath + "/anthropic": false,
		"example.com":             false,
	}
	for path, want := range cases {
		if got := isStandardImport(path); got != want {
			t.Errorf("isStandardImport(%q) = %v, want %v", path, got, want)
		}
	}
}
