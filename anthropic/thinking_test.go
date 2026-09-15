package anthropic

import (
	"errors"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestThinkingStyleFor(t *testing.T) {
	cases := map[string]thinkingStyle{
		"claude-opus-4-6":                     styleAdaptive,
		"claude-opus-4-7-20260101":            styleAdaptive,
		"claude-sonnet-5":                     styleAdaptive,
		"claude-opus-5-20260214":              styleAdaptive,
		"claude-opus-latest":                  styleAdaptive,
		"":                                    styleAdaptive,
		"claude-haiku-4-5":                    styleBudget,
		"claude-opus-4-5-20251101":            styleBudget,
		"claude-opus-4-1-20250805":            styleBudget,
		"claude-sonnet-4-20250514":            styleBudget,
		"claude-3-7-sonnet-20250219":          styleBudget,
		"claude-3-5-haiku-latest":             styleBudget,
		"us.anthropic.claude-sonnet-4-5-v1:0": styleBudget,
		"claude-fable-5-1":                    styleAlwaysOn,
		"anthropic/claude-fable-5-1":          styleAlwaysOn,
		"Claude-Fable-5-2-20260301":           styleAlwaysOn,
	}
	for model, want := range cases {
		if got := thinkingStyleFor(model); got != want {
			t.Errorf("thinkingStyleFor(%q) = %q, want %q", model, got, want)
		}
	}
}

// thinkingRequestBody translates a request for the model and returns the
// marshalled body, so each case asserts the exact bytes Anthropic receives.
func thinkingRequestBody(t *testing.T, model string, cfg goodall.ThinkingConfig, maxTokens int) string {
	t.Helper()
	return bodyJSON(t, &goodall.Request{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi"})},
		Thinking:  cfg,
	})
}

func TestThinkingWire(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		cfg       goodall.ThinkingConfig
		maxTokens int
		want      string // the exact thinking fields, empty when none are sent
	}{
		{
			name:      "the zero config sends no thinking fields",
			model:     "claude-opus-4-6",
			maxTokens: 8000,
		},
		{
			name:      "adaptive with an effort",
			model:     "claude-opus-4-6",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortHigh},
			maxTokens: 8000,
			want:      `"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}`,
		},
		{
			name:      "adaptive with an effort and a display",
			model:     "claude-opus-4-6",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortMax, Display: goodall.DisplaySummarized},
			maxTokens: 8000,
			want:      `"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"max"}`,
		},
		{
			name:      "a display alone asks for adaptive thinking and no effort",
			model:     "claude-opus-4-6",
			cfg:       goodall.ThinkingConfig{Display: goodall.DisplayOmitted},
			maxTokens: 8000,
			want:      `"thinking":{"type":"adaptive","display":"omitted"}`,
		},
		{
			name:      "an effort goodall does not know passes through",
			model:     "claude-opus-4-6",
			cfg:       goodall.ThinkingConfig{Effort: goodall.Effort("ultra")},
			maxTokens: 8000,
			want:      `"thinking":{"type":"adaptive"},"output_config":{"effort":"ultra"}`,
		},
		{
			name:      "off is disabled on a model that accepts it",
			model:     "claude-opus-4-6",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortOff},
			maxTokens: 8000,
			want:      `"thinking":{"type":"disabled"}`,
		},
		{
			name:      "off is omitted on a model that refuses disabled",
			model:     "claude-fable-5-1",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortOff},
			maxTokens: 8000,
		},
		{
			name:      "fable still takes an effort",
			model:     "claude-fable-5-1",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
			maxTokens: 8000,
			want:      `"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"low"}`,
		},
		{
			name:      "a budget model derives low from max_tokens",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortLow},
			maxTokens: 8000,
			want:      `"thinking":{"type":"enabled","budget_tokens":1600}`,
		},
		{
			name:      "a budget model derives medium",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortMedium},
			maxTokens: 8000,
			want:      `"thinking":{"type":"enabled","budget_tokens":4000}`,
		},
		{
			name:      "a budget model derives high",
			model:     "claude-3-7-sonnet-20250219",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortHigh},
			maxTokens: 8000,
			want:      `"thinking":{"type":"enabled","budget_tokens":6400}`,
		},
		{
			name:      "a budget model derives xhigh",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortXHigh},
			maxTokens: 8000,
			want:      `"thinking":{"type":"enabled","budget_tokens":7200}`,
		},
		{
			name:      "a budget model derives max",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortMax},
			maxTokens: 8000,
			want:      `"thinking":{"type":"enabled","budget_tokens":7600}`,
		},
		{
			name:      "a budget below the minimum is raised to it",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortLow},
			maxTokens: 2000,
			want:      `"thinking":{"type":"enabled","budget_tokens":1024}`,
		},
		{
			name:      "a budget at the ceiling stays below max_tokens",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortMax},
			maxTokens: 1025,
			want:      `"thinking":{"type":"enabled","budget_tokens":1024}`,
		},
		{
			name:      "a budget model has no display, so a display alone sends nothing",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Display: goodall.DisplaySummarized},
			maxTokens: 8000,
		},
		{
			name:      "off is disabled on a budget model",
			model:     "claude-haiku-4-5",
			cfg:       goodall.ThinkingConfig{Effort: goodall.EffortOff},
			maxTokens: 8000,
			want:      `"thinking":{"type":"disabled"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := thinkingRequestBody(t, c.model, c.cfg, c.maxTokens)
			if c.want == "" {
				if strings.Contains(got, `"thinking"`) || strings.Contains(got, `"output_config"`) {
					t.Errorf("got %s, want no thinking fields", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("got  %s\nwant it to contain %s", got, c.want)
			}
		})
	}
}

func TestThinkingErrors(t *testing.T) {
	t.Run("a budget model with no room for the minimum budget", func(t *testing.T) {
		_, _, err := translateRequest(&goodall.Request{
			Model:     "claude-haiku-4-5",
			MaxTokens: 1024,
			Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow},
		})
		if !errors.Is(err, goodall.KindInvalidRequest) {
			t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
		}
		if !strings.Contains(err.Error(), "1024") {
			t.Errorf("err = %v, want it to name the minimum", err)
		}
	})

	t.Run("an effort with no budget ratio on a budget model", func(t *testing.T) {
		_, _, err := translateRequest(&goodall.Request{
			Model:     "claude-haiku-4-5",
			MaxTokens: 8000,
			Thinking:  goodall.ThinkingConfig{Effort: goodall.Effort("ultra")},
		})
		if !errors.Is(err, goodall.KindInvalidRequest) {
			t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
		}
		if !strings.Contains(err.Error(), "ultra") {
			t.Errorf("err = %v, want it to name the effort", err)
		}
	})
}

// styleWith is the style chosen for a model whose catalogue entry reports the
// given thinking style, which is the fact the name heuristic yields to.
func styleWith(model string, published goodall.ThinkingStyle) thinkingStyle {
	info := &goodall.ModelInfo{
		ID:           model,
		Provider:     ProviderName,
		Capabilities: goodall.Capabilities{ThinkingStyle: published},
	}
	return styleFor(model, info)
}

func TestTheCatalogueOverridesTheNameHeuristic(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		published goodall.ThinkingStyle
		want      thinkingStyle
	}{
		{"a catalogue adaptive beats a name that reads as budget", "claude-haiku-4-5", goodall.ThinkingAdaptive, styleAdaptive},
		{"a catalogue budget beats a name that reads as adaptive", "claude-opus-4-6", goodall.ThinkingBudget, styleBudget},
		{"a style nobody published leaves the heuristic in charge", "claude-haiku-4-5", goodall.ThinkingStyleUnknown, styleBudget},
		{"a style goodall does not define leaves the heuristic in charge", "claude-opus-4-6", goodall.ThinkingStyle("telepathy"), styleAdaptive},
		// The catalogue says which *form* the model takes, never that it
		// refuses to be turned off, so a name that says always-on keeps
		// saying it on top of a catalogue "adaptive".
		{"a catalogue adaptive does not turn fable off", "claude-fable-5-1", goodall.ThinkingAdaptive, styleAlwaysOn},
		{"a catalogue budget still overrides fable", "claude-fable-5-1", goodall.ThinkingBudget, styleBudget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := styleWith(c.model, c.published); got != c.want {
				t.Errorf("styleFor(%q, %q) = %q, want %q", c.model, c.published, got, c.want)
			}
		})
	}
	if got := styleFor("claude-haiku-4-5", nil); got != styleBudget {
		t.Errorf("styleFor with no catalogue entry = %q, want the heuristic's %q", got, styleBudget)
	}
}

func TestTheCatalogueStyleReachesTheWire(t *testing.T) {
	// The whole point of the field: a 4.5 model the catalogue says takes
	// adaptive thinking is sent adaptive thinking, not a derived budget.
	body := bodyJSON(t, &goodall.Request{
		Model:     "claude-haiku-4-5",
		MaxTokens: 8000,
		Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi"})},
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
		ModelInfo: &goodall.ModelInfo{
			ID:           "claude-haiku-4-5",
			Provider:     ProviderName,
			Capabilities: goodall.Capabilities{ThinkingStyle: goodall.ThinkingAdaptive},
		},
	})
	want := `"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"low"}`
	if !strings.Contains(body, want) {
		t.Errorf("got  %s\nwant it to contain %s", body, want)
	}
}
