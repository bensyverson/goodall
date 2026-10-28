package anthropic

import (
	"errors"
	"testing"

	"github.com/bensyverson/goodall"
)

// forcedRequest is a request that obliges the model to call its one tool.
func forcedRequest(model string, info *goodall.ModelInfo) *goodall.Request {
	return &goodall.Request{
		Model:      model,
		MaxTokens:  1024,
		Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi"})},
		Tools:      []goodall.Tool{weatherTool()},
		ToolChoice: goodall.ChooseAny(),
		ModelInfo:  info,
	}
}

func TestAForcedToolChoiceIsRefusedBeforeSending(t *testing.T) {
	cases := []struct {
		name  string
		model string
		info  *goodall.ModelInfo
	}{
		{"by name, with no catalog entry", "claude-opus-5-5", nil},
		{"by the catalog entry, over a name that reads as accepting", "claude-haiku-5-5", &goodall.ModelInfo{
			ID: "claude-haiku-5-5", Provider: ProviderName,
			Capabilities: goodall.Capabilities{ForcedToolChoice: goodall.Unsupported},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := translateRequest(forcedRequest(c.model, c.info))
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
