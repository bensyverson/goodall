package goodall

import (
	json "encoding/json/v2"
	"reflect"
	"slices"
	"testing"
)

// TestConversationZeroValue checks the zero Conversation is usable.
func TestConversationZeroValue(t *testing.T) {
	var c Conversation
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
	if _, ok := c.Last(); ok {
		t.Error("Last() reported a message in an empty conversation")
	}
	if got := c.Messages(); len(got) != 0 {
		t.Errorf("Messages() = %#v, want empty", got)
	}
	for range c.All() {
		t.Error("All() yielded a message in an empty conversation")
	}
	if got := c.Append(UserMessage(Text{Text: "hi"})); got.Len() != 1 {
		t.Errorf("Append on the zero value gave Len() = %d, want 1", got.Len())
	}
}

// TestConversationAppendDoesNotAlias is the append-only invariant in its
// sharpest form: two conversations grown from the same value must never write
// into one backing array.
func TestConversationAppendDoesNotAlias(t *testing.T) {
	// Forking after every length, because a naive append only shares its
	// array once the growth policy has left spare capacity behind.
	base := Conversation{}
	for n := range 8 {
		base = base.Append(UserMessage(Text{Text: "base"}))
		if got, want := cap(base.messages), base.Len(); got != want {
			t.Fatalf("after %d appends cap = %d, len = %d: spare capacity is an array two conversations could both write into", n+1, got, want)
		}
		left := base.Append(UserMessage(Text{Text: "left"}))
		right := base.Append(UserMessage(Text{Text: "right"}))
		if base.Len() != n+1 {
			t.Fatalf("base Len() = %d, want %d", base.Len(), n+1)
		}
		if got := left.At(n + 1).Text(); got != "left" {
			t.Errorf("after %d appends the left tail = %q, want left", n+1, got)
		}
		if got := right.At(n + 1).Text(); got != "right" {
			t.Errorf("after %d appends the right tail = %q, want right", n+1, got)
		}
	}
}

// TestConversationAppendCopiesTheCallersMessage checks stored history cannot
// be edited through the slice the caller kept.
func TestConversationAppendCopiesTheCallersMessage(t *testing.T) {
	msg := UserMessage(Text{Text: "original"})
	c := Conversation{}.Append(msg)
	msg.Content[0] = Text{Text: "tampered"}
	if got := c.At(0).Text(); got != "original" {
		t.Errorf("stored text = %q, want original", got)
	}
}

// TestConversationAccessorsReturnCopies checks every read path hands back
// history a caller cannot write through.
func TestConversationAccessorsReturnCopies(t *testing.T) {
	c := Conversation{}.Append(
		UserMessage(Text{Text: "one"}),
		AssistantMessage(ToolResult{ToolUseID: "t", Content: Blocks{Text{Text: "nested"}}}),
	)

	t.Run("At", func(t *testing.T) {
		got := c.At(0)
		got.Content[0] = Text{Text: "tampered"}
		if s := c.At(0).Text(); s != "one" {
			t.Errorf("stored text = %q, want one", s)
		}
	})
	t.Run("Last", func(t *testing.T) {
		got, ok := c.Last()
		if !ok {
			t.Fatal("Last() reported no message")
		}
		got.Content[0] = Text{Text: "tampered"}
		if _, ok := c.At(1).Content[0].(ToolResult); !ok {
			t.Errorf("stored block is %T, want ToolResult", c.At(1).Content[0])
		}
	})
	t.Run("Messages", func(t *testing.T) {
		got := c.Messages()
		got[0] = SystemMessage(Text{Text: "tampered"})
		if c.At(0).Role != RoleUser {
			t.Errorf("stored role = %q, want user", c.At(0).Role)
		}
		got = c.Messages()
		got[0].Content[0] = Text{Text: "tampered"}
		if s := c.At(0).Text(); s != "one" {
			t.Errorf("stored text = %q, want one", s)
		}
	})
	t.Run("All", func(t *testing.T) {
		for m := range c.All() {
			m.Content[0] = Text{Text: "tampered"}
		}
		if s := c.At(0).Text(); s != "one" {
			t.Errorf("stored text = %q, want one", s)
		}
	})
	t.Run("nested tool result content", func(t *testing.T) {
		got, ok := c.At(1).Content[0].(ToolResult)
		if !ok {
			t.Fatalf("block is %T, want ToolResult", c.At(1).Content[0])
		}
		got.Content[0] = Text{Text: "tampered"}
		stored, ok := c.At(1).Content[0].(ToolResult)
		if !ok {
			t.Fatalf("stored block is %T, want ToolResult", c.At(1).Content[0])
		}
		if s, ok := stored.Content[0].(Text); !ok || s.Text != "nested" {
			t.Errorf("stored nested block = %#v, want Text{nested}", stored.Content[0])
		}
	})
}

// TestConversationAll checks the iterator yields every message in order and
// honours an early break.
func TestConversationAll(t *testing.T) {
	c := Conversation{}.Append(
		UserMessage(Text{Text: "a"}),
		AssistantMessage(Text{Text: "b"}),
		UserMessage(Text{Text: "c"}),
	)
	var got []string
	for m := range c.All() {
		got = append(got, m.Text())
	}
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("All() yielded %q", got)
	}

	got = got[:0]
	for m := range c.All() {
		got = append(got, m.Text())
		if len(got) == 2 {
			break
		}
	}
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("All() with a break yielded %q", got)
	}
}

// TestConversationHasNoMutatingMethod walks the method set and fails on any
// method beyond the append-only vocabulary. Invariant 1 is worth a test that
// notices a new method nobody discussed.
func TestConversationHasNoMutatingMethod(t *testing.T) {
	allowed := map[string]bool{
		"Append":            true,
		"All":               true,
		"At":                true,
		"Last":              true,
		"Len":               true,
		"Messages":          true,
		"MarshalJSONTo":     true,
		"UnmarshalJSONFrom": true,
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[Conversation](), reflect.TypeFor[*Conversation]()} {
		for method := range typ.Methods() {
			name := method.Name
			if !allowed[name] {
				t.Errorf("%s has method %s, which is not in the append-only vocabulary", typ, name)
			}
		}
	}
}

// TestConversationJSON checks a conversation is a JSON array of messages and
// survives a round trip whole.
func TestConversationJSON(t *testing.T) {
	c := Conversation{}.Append(
		UserMessage(Text{Text: "hi"}),
		AssistantMessage(ToolUse{ID: "toolu_1", Name: "now"}),
		UserMessage(ToolResult{ToolUseID: "toolu_1", Content: Blocks{Text{Text: "noon"}}}),
	)
	const want = `[` +
		`{"role":"user","content":[{"type":"text","text":"hi"}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"now"}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"noon"}]}]}` +
		`]`
	got, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("Marshal =\n\t%s\nwant\n\t%s", got, want)
	}

	var back Conversation
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back.Messages(), c.Messages()) {
		t.Errorf("round trip = %#v, want %#v", back.Messages(), c.Messages())
	}
}

// TestConversationEmptyJSON checks the zero value encodes as an empty array
// rather than null, so a stored thread is always an array.
func TestConversationEmptyJSON(t *testing.T) {
	got, err := json.Marshal(Conversation{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != "[]" {
		t.Errorf("Marshal = %s, want []", got)
	}
	var back Conversation
	if err := json.Unmarshal([]byte("[]"), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Len() != 0 {
		t.Errorf("Len() = %d, want 0", back.Len())
	}
}
