// Package strict checks a request's strict tools against the rules of the
// model family that will compile them, before the request is sent. Both
// providers serve Claude and OpenAI models, so the rules live here rather
// than in either of them; what every family shares is
// [goodall.Schema.ValidateStrict].
//
// A rule is checked only where the family is known: a refusal must name a
// limit the upstream really has, so a model whose rules nobody has measured
// gets its request sent and its 400, if any, reported as the provider's.
package strict

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bensyverson/goodall"
)

// Anthropic's request-wide limits on strict tools. Each is counted across
// every strict tool in the request; guided tools do not count. Source:
// platform.claude.com, the "Strict tool use" and "Structured outputs" pages,
// read 2026-10-29, and the live 400s quoted on each.
const (
	// MaxClaudeTools is the most tools one request may mark strict: "Too
	// many strict tools (21). The maximum number of strict tools supported
	// is 20." (claude-haiku-4-5, 2026-10-29).
	MaxClaudeTools = 20
	// MaxClaudeOptionalParameters is the most optional parameters, at any
	// depth, across every strict schema in one request: "Schemas contains
	// too many optional parameters (25) … (limit: 24)" (claude-haiku-5-5,
	// 2026-10-29, nested and array-element objects counted alike).
	MaxClaudeOptionalParameters = 24
	// MaxClaudeUnionParameters is the most parameters with a union type
	// (anyOf, or a type array) across every strict schema in one request.
	// goodall's Schema has a single Type and no anyOf, so no request can
	// reach it and nothing counts it; it is here so the limit is not
	// rediscovered when Schema grows a union.
	MaxClaudeUnionParameters = 16
)

// claudeFormats are the string formats a strict Claude schema may use. Source:
// the "Structured outputs" page, read 2026-10-29; an unlisted one is a 400,
// "For 'string' type, format 'color' is not supported".
var claudeFormats = []string{"date-time", "time", "date", "duration", "email", "hostname", "uri", "ipv4", "ipv6", "uuid"}

// Rules is the family of strict rules the model that compiles a request's
// schemas applies.
type Rules int

const (
	// RulesUnchecked is a model whose rules goodall does not know; nothing
	// is refused.
	RulesUnchecked Rules = iota
	// RulesClaude is Anthropic's: the shared shape, the supported formats,
	// and the request-wide limits above.
	RulesClaude
	// RulesOpenAI is OpenAI's: the shared shape, and every property listed
	// as required. Source: developers.openai.com, the "Function calling"
	// guide, read 2026-10-29 — "All fields in properties must be marked as
	// required" — and a live 400 from gpt-5.6-terra and, through
	// OpenRouter, openai/gpt-6-astra: "'required' is required to be
	// supplied and to be an array including every key in properties".
	RulesOpenAI
)

// String names the family.
func (r Rules) String() string {
	switch r {
	case RulesClaude:
		return "claude"
	case RulesOpenAI:
		return "openai"
	default:
		return "unchecked"
	}
}

// RulesFor reads the family from a model name as either provider spells it:
// a Claude name under any prefix, or an OpenRouter name under openai/.
func RulesFor(model string) Rules {
	name := strings.ToLower(model)
	switch {
	case strings.Contains(name, "claude"):
		return RulesClaude
	case strings.HasPrefix(name, "openai/"):
		return RulesOpenAI
	}
	return RulesUnchecked
}

// Check refuses, before sending, a request whose strict tools the model would
// refuse: a catalog that says the model takes no strict tools, a schema no
// strict grammar can express, or a limit of the family's. It returns nil for
// a request with no strict tool. provider prefixes the error and names the
// provider on a *goodall.CapabilityError.
func Check(provider string, req *goodall.Request, rules Rules) error {
	var marked []goodall.Tool
	for _, t := range req.Tools {
		if t != nil && goodall.StrictnessOf(t) == goodall.Strict {
			marked = append(marked, t)
		}
	}
	if len(marked) == 0 {
		return nil
	}
	if info := req.ModelInfo; info != nil && info.Capabilities.StrictTools == goodall.Unsupported {
		return &goodall.CapabilityError{
			Provider:   provider,
			Model:      req.Model,
			Capability: goodall.CapStrictTools,
			Message:    "the catalog says this model takes no strict tools; build them without goodall.Strict",
		}
	}
	if rules == RulesUnchecked {
		return nil
	}
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("%s: %w: "+format, append([]any{provider, goodall.KindInvalidRequest}, args...)...)
	}

	optional := 0
	for _, t := range marked {
		schema := t.Schema()
		if schema == nil {
			continue // the translator refuses a tool with no schema itself
		}
		if err := schema.ValidateStrict(); err != nil {
			return refuse("tool %q is strict, but %s", t.Name(), strings.TrimPrefix(err.Error(), "goodall: "))
		}
		var problem string
		walk(schema, "", func(s *goodall.Schema, path string) bool {
			switch rules {
			case RulesClaude:
				if s.Format != "" && !slices.Contains(claudeFormats, s.Format) {
					problem = fmt.Sprintf("parameter %q has format %q, which Anthropic's strict mode does not take (it takes %s)",
						path, s.Format, strings.Join(claudeFormats, ", "))
					return false
				}
				optional += optionalCount(s)
			case RulesOpenAI:
				if name, ok := firstOptional(s); ok {
					problem = fmt.Sprintf("parameter %q is optional, and OpenAI's strict mode needs every property listed as required",
						joinPath(path, name))
					return false
				}
			}
			return true
		})
		if problem != "" {
			return refuse("tool %q is strict, but %s", t.Name(), problem)
		}
	}
	if rules != RulesClaude {
		return nil
	}
	if n := len(marked); n > MaxClaudeTools {
		return refuse("%d tools are marked strict, and Anthropic takes at most %d strict tools in one request", n, MaxClaudeTools)
	}
	if optional > MaxClaudeOptionalParameters {
		return refuse("the strict tools have %d optional parameters between them, and Anthropic takes at most %d across one request; "+
			"make more of them required or mark fewer tools strict", optional, MaxClaudeOptionalParameters)
	}
	return nil
}

// walk visits s and every schema beneath it, depth first, with the dotted
// parameter path of each, until visit returns false.
func walk(s *goodall.Schema, path string, visit func(*goodall.Schema, string) bool) bool {
	if !visit(s, path) {
		return false
	}
	for i := range s.Properties {
		p := &s.Properties[i]
		if !walk(&p.Schema, joinPath(path, p.Name), visit) {
			return false
		}
	}
	if s.Items != nil {
		return walk(s.Items, path+"[]", visit)
	}
	return true
}

// optionalCount is how many of an object schema's own properties are not
// required.
func optionalCount(s *goodall.Schema) int {
	n := 0
	for _, p := range s.Properties {
		if !slices.Contains(s.Required, p.Name) {
			n++
		}
	}
	return n
}

// firstOptional names the first of an object schema's own properties that is
// not required.
func firstOptional(s *goodall.Schema) (string, bool) {
	for _, p := range s.Properties {
		if !slices.Contains(s.Required, p.Name) {
			return p.Name, true
		}
	}
	return "", false
}

// joinPath builds the dotted parameter name an error message carries.
func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}
