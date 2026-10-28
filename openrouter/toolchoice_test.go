package openrouter

import (
	"errors"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestAForcedToolChoiceIsRefusedBeforeSending(t *testing.T) {
	cases := []struct {
		name  string
		model string
		info  *goodall.ModelInfo
	}{
		{"by name, with no catalog entry", "anthropic/claude-sonnet-5.5", nil},
		{"by the catalog entry, over a name that reads as accepting", "anthropic/claude-haiku-5.5", &goodall.ModelInfo{
			ID: "anthropic/claude-haiku-5.5", Provider: ProviderName,
			Capabilities: goodall.Capabilities{ForcedToolChoice: goodall.Unsupported},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:      c.model,
				Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
				Tools:      []goodall.Tool{everyFeatureRequest(t).Tools[0]},
				ToolChoice: goodall.ChooseTool(everyFeatureRequest(t).Tools[0].Name()),
				ModelInfo:  c.info,
			}
			_, err := translateRequest(req, OpenRouter, false)
			capErr, ok := errors.AsType[*goodall.CapabilityError](err)
			if !ok {
				t.Fatalf("err = %v, want a *goodall.CapabilityError", err)
			}
			if capErr.Capability != goodall.CapForcedToolChoice || capErr.Model != c.model || capErr.Provider != ProviderName {
				t.Errorf("err = %+v, want %s refused for %q on %s", capErr, goodall.CapForcedToolChoice, c.model, ProviderName)
			}
		})
	}
}
