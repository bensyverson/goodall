package goodall

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"reflect"
	"testing"
)

// TestMessageConstructors checks the role each constructor stamps.
func TestMessageConstructors(t *testing.T) {
	cases := []struct {
		name string
		got  Message
		want Role
	}{
		{"user", UserMessage(Text{Text: "hi"}), RoleUser},
		{"assistant", AssistantMessage(Text{Text: "hi"}), RoleAssistant},
		{"system", SystemMessage(Text{Text: "hi"}), RoleSystem},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got.Role != c.want {
				t.Errorf("Role = %q, want %q", c.got.Role, c.want)
			}
			if len(c.got.Content) != 1 {
				t.Errorf("Content has %d blocks, want 1", len(c.got.Content))
			}
			if c.got.Partial {
				t.Error("Partial = true, want false")
			}
		})
	}
}

// TestMessageJSON asserts the message shape, including that Partial is absent
// unless the turn was interrupted.
func TestMessageJSON(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "user text",
			msg:  UserMessage(Text{Text: "hi"}),
			want: `{"role":"user","content":[{"type":"text","text":"hi"}]}`,
		},
		{
			name: "interrupted assistant turn",
			msg:  Message{Role: RoleAssistant, Content: Blocks{Text{Text: "part"}}, Partial: true},
			want: `{"role":"assistant","content":[{"type":"text","text":"part"}],"partial":true}`,
		},
		{
			name: "tool results travel in a user message",
			msg:  UserMessage(ToolResult{ToolUseID: "toolu_1", Content: Blocks{Text{Text: "ok"}}}),
			want: `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"ok"}]}]}`,
		},
		{
			name: "mid-conversation system message",
			msg:  SystemMessage(Text{Text: "be brief"}),
			want: `{"role":"system","content":[{"type":"text","text":"be brief"}]}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.msg)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("Marshal =\n\t%s\nwant\n\t%s", got, c.want)
			}
			var back Message
			if err := json.Unmarshal([]byte(c.want), &back); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(back, c.msg) {
				t.Errorf("round trip = %#v, want %#v", back, c.msg)
			}
		})
	}
}

// TestMessageText checks that Text concatenates the text blocks and ignores
// everything else.
func TestMessageText(t *testing.T) {
	m := AssistantMessage(
		Thinking{Text: "hmm"},
		Text{Text: "one"},
		Image{Source: URLSource("https://example.com/a.png")},
		Text{Text: "two"},
	)
	if got := m.Text(); got != "onetwo" {
		t.Errorf("Text() = %q, want %q", got, "onetwo")
	}
	if got := (Message{}).Text(); got != "" {
		t.Errorf("empty Text() = %q, want empty", got)
	}
}

// TestMessageToolUses checks that ToolUses returns the tool calls in order.
func TestMessageToolUses(t *testing.T) {
	m := AssistantMessage(
		Text{Text: "calling"},
		ToolUse{ID: "a", Name: "one", Input: jsontext.Value(`{}`)},
		ToolUse{ID: "b", Name: "two", Input: jsontext.Value(`{}`)},
	)
	got := m.ToolUses()
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("ToolUses() = %#v", got)
	}
	if got := (Message{}).ToolUses(); got != nil {
		t.Errorf("empty ToolUses() = %#v, want nil", got)
	}
}
