// Package claudename reads facts about a Claude model from its name, for the
// API behavior no catalog publishes. Both providers serve Claude, under two
// spellings — claude-opus-5-5 from Anthropic, anthropic/claude-opus-5.5 from
// OpenRouter — so the reading lives here rather than in either of them.
package claudename

import (
	"strconv"
	"strings"

	"github.com/bensyverson/goodall"
)

// Version reads the first major.minor version out of a model name. Dots and
// hyphens both separate components, so claude-opus-5-5 and
// anthropic/claude-opus-5.5 read alike, and claude-3-7-sonnet-20250219 reads
// as 3.7. The minor part is only taken from a one- or two-digit component, so
// the release date in claude-opus-5-20260214 is not read as a minor version.
func Version(model string) (major, minor int, ok bool) {
	parts := strings.Split(strings.ReplaceAll(model, ".", "-"), "-")
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || part == "" {
			continue
		}
		major = n
		if i+1 < len(parts) && len(parts[i+1]) <= 2 {
			if m, err := strconv.Atoi(parts[i+1]); err == nil {
				minor = m
			}
		}
		return major, minor, true
	}
	return 0, 0, false
}

// AtLeast reports whether major.minor is at or above the given version.
func AtLeast(major, minor, wantMajor, wantMinor int) bool {
	return major > wantMajor || (major == wantMajor && minor >= wantMinor)
}

// RejectsForcedToolChoice reports whether the model answers a tool choice of
// "any" or a named tool with a 400, which neither provider's catalog says:
// Fable and Mythos from 5.1, and Opus and Sonnet from 5.5. Haiku 5.5 still
// accepts it. An Opus or Sonnet name with no version in it, such as
// claude-opus-latest, reads as the newest of its line.
func RejectsForcedToolChoice(model string) bool {
	name := strings.ToLower(model)
	major, minor, ok := Version(name)
	switch {
	case strings.Contains(name, "fable"), strings.Contains(name, "mythos"):
		return !ok || AtLeast(major, minor, 5, 1)
	case strings.Contains(name, "opus"), strings.Contains(name, "sonnet"):
		return !ok || AtLeast(major, minor, 5, 5)
	}
	return false
}

// ForcedToolChoice combines what a catalog published about forcing a tool
// with what the name says. A published refusal stands; otherwise a name that
// rejects forcing overrides the catalog, because OpenRouter lists tool_choice
// for models whose upstream refuses the forced forms.
func ForcedToolChoice(model string, published goodall.Support) goodall.Support {
	if published == goodall.Unsupported || RejectsForcedToolChoice(model) {
		return goodall.Unsupported
	}
	return published
}

// RefuseForcing returns the error a provider raises, before sending, for a
// request that forces a tool on a model that rejects it, or nil.
func RefuseForcing(provider string, req *goodall.Request) error {
	if m := req.ToolChoice.Mode; m != goodall.ToolChoiceAny && m != goodall.ToolChoiceNamed {
		return nil
	}
	published := goodall.SupportUnknown
	if req.ModelInfo != nil {
		published = req.ModelInfo.Capabilities.ForcedToolChoice
	}
	if ForcedToolChoice(req.Model, published) != goodall.Unsupported {
		return nil
	}
	return &goodall.CapabilityError{
		Provider:   provider,
		Model:      req.Model,
		Capability: goodall.CapForcedToolChoice,
		Message:    `a tool_choice of "any" or a named tool is refused by this model; use auto and name the tool in the prompt, or ask for structured output`,
	}
}
