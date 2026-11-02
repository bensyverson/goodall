package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestEveryToolStatesStrict(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model:    "gpt-6-astra",
		Tools:    []goodall.Tool{weatherLookup(t), weatherLookup(t, goodall.WithStrictness(goodall.Strict))},
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	})
	var body struct {
		Tools []map[string]jsontext.Value `json:"tools"`
	}
	if err := json.Unmarshal([]byte(got), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(body.Tools))
	}
	for i, want := range []string{"false", "true"} {
		if s := string(body.Tools[i]["strict"]); s != want {
			t.Errorf("tool %d strict = %q, want %s, because an absent member lets OpenAI rewrite the schema", i, s, want)
		}
		if string(body.Tools[i]["type"]) != `"function"` || string(body.Tools[i]["name"]) != `"get_weather"` {
			t.Errorf("tool %d is not flat: %v", i, body.Tools[i])
		}
		if _, nested := body.Tools[i]["function"]; nested {
			t.Errorf("tool %d nests a function member, which is the Chat shape", i)
		}
	}
}

func TestAStrictToolOutsideOpenAIRulesIsStillRefused(t *testing.T) {
	c := New(WithDialect(OpenAI))
	_, _, err := c.requestBody(strictRequest("gpt-6-astra", strictLookup[optionalInput](t)), false)
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("err = %v, want it to wrap KindInvalidRequest", err)
	}
}

// recordedMember reads one member of a request the probe recorded, which is what
// the live API accepted, so a test against it pins the spelling to an observation.
func recordedMember(t *testing.T, name, member string) string {
	t.Helper()
	var body map[string]jsontext.Value
	if err := json.Unmarshal(probeRecording(t, name+".request.json"), &body); err != nil {
		t.Fatal(err)
	}
	return string(body[member])
}

func TestToolChoiceIsSpelledAsRecorded(t *testing.T) {
	for _, tc := range []struct {
		recording string
		choice    goodall.ToolChoice
	}{
		{"tool_choice_required", goodall.ChooseAny()},
		{"tool_choice_none", goodall.ToolChoice{Mode: goodall.ToolChoiceNone}},
		{"tool_choice_named", goodall.ChooseTool("get_weather")},
	} {
		t.Run(tc.recording, func(t *testing.T) {
			got := sentBody(t, &goodall.Request{
				Model:      "gpt-6-luna",
				Tools:      []goodall.Tool{weatherLookup(t)},
				ToolChoice: tc.choice,
				Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
			})
			var recorded jsontext.Value = jsontext.Value(recordedMember(t, tc.recording, "tool_choice"))
			if err := recorded.Compact(); err != nil {
				t.Fatal(err)
			}
			if want := `"tool_choice":` + string(recorded); !strings.Contains(got, want) {
				t.Errorf("body %s lacks the recorded %s", got, want)
			}
		})
	}
}

func TestAnAutoToolChoiceSendsNoMember(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model:    "gpt-6-luna",
		Tools:    []goodall.Tool{weatherLookup(t)},
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	})
	if strings.Contains(got, "tool_choice") || strings.Contains(got, "parallel_tool_calls") {
		t.Errorf("a request that never chose sent a choice: %s", got)
	}
}

func TestNoParallelSendsParallelToolCallsFalseAsRecorded(t *testing.T) {
	if got := recordedMember(t, "parallel_false", "parallel_tool_calls"); got != "false" {
		t.Fatalf("the recording sent %q", got)
	}
	got := sentBody(t, &goodall.Request{
		Model:      "gpt-6-luna",
		Tools:      []goodall.Tool{weatherLookup(t)},
		ToolChoice: goodall.ChooseAuto().Serial(),
		Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	})
	if !strings.Contains(got, `"parallel_tool_calls":false`) {
		t.Errorf("body %s lacks parallel_tool_calls false", got)
	}
}

func TestStopSequencesAreRefusedBecauseTheWireHasNoMember(t *testing.T) {
	for _, name := range []string{"stop_member", "stop_sequences_member"} {
		if body := string(probeRecording(t, name+".response.json")); !strings.Contains(body, "unknown_parameter") {
			t.Errorf("%s no longer records a refusal: %s", name, body)
		}
	}
	c := New(WithDialect(OpenAI))
	_, _, err := c.requestBody(&goodall.Request{
		Model:         "gpt-6-luna",
		StopSequences: []string{"five"},
		Messages:      []goodall.Message{goodall.UserMessage(goodall.Text{Text: "count"})},
	}, false)
	capErr, ok := errors.AsType[*goodall.CapabilityError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *goodall.CapabilityError rather than a dropped stop", err, err)
	}
	if capErr.Capability != goodall.CapStopSequences || capErr.Model != "gpt-6-luna" {
		t.Errorf("refusal = %+v", capErr)
	}
}
