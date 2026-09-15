package chat_test

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
)

// TestNewThreadMintsASortableID is the uuid v7 decision: two threads minted in
// order sort in that order as strings, which is what makes a listing ordered
// by id a listing ordered by age.
func TestNewThreadMintsASortableID(t *testing.T) {
	first := chat.NewThread()
	second := chat.NewThread()
	if first.ID == "" || second.ID == "" {
		t.Fatalf("a new thread has no id: %q, %q", first.ID, second.ID)
	}
	if first.ID == second.ID {
		t.Fatalf("two new threads share the id %q", first.ID)
	}
	if first.ID >= second.ID {
		t.Errorf("ids %q and %q do not sort by age; uuid v7 ids must", first.ID, second.ID)
	}
	if first.Version != 0 {
		t.Errorf("a new thread is at version %d, want 0: it has not been stored yet", first.Version)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Errorf("a new thread has no timestamps: %v, %v", first.CreatedAt, first.UpdatedAt)
	}
}

// TestThreadRoundTripsThroughJSON is the persistence contract: a thread is
// plain data, so a store that keeps JSON keeps everything.
func TestThreadRoundTripsThroughJSON(t *testing.T) {
	amount, err := goodall.ParseDecimal("0.0375")
	if err != nil {
		t.Fatal(err)
	}
	want := &chat.Thread{
		ID: "01930000-0000-7000-8000-000000000001",
		Conversation: goodall.Conversation{}.Append(
			goodall.UserMessage(goodall.Text{Text: "say hi"}),
			goodall.AssistantMessage(
				goodall.Thinking{Text: "they want a greeting", Signature: "sig"},
				goodall.Text{Text: "hi"},
				goodall.ToolUse{ID: "tu_1", Name: "echo", Input: jsontext.Value(`{"text":"hi"}`)},
			),
			goodall.UserMessage(goodall.ToolResult{ToolUseID: "tu_1", Content: goodall.Blocks{goodall.Text{Text: "echo: hi"}}}),
		),
		Usage:     goodall.Usage{Input: 12, Output: 34, CacheRead: 5, CacheWrite: 6, Reasoning: 7},
		Cost:      goodall.Cost{Amount: amount, Currency: "USD", Reported: true},
		Version:   4,
		CreatedAt: time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 9, 15, 10, 5, 0, 0, time.UTC),
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshalling a thread: %v", err)
	}
	var got chat.Thread
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshalling a thread: %v", err)
	}
	again, err := json.Marshal(&got)
	if err != nil {
		t.Fatalf("re-marshalling a thread: %v", err)
	}
	if string(again) != string(encoded) {
		t.Errorf("a thread did not round-trip:\n%s\nwant\n%s", again, encoded)
	}
	if got.Conversation.Len() != want.Conversation.Len() {
		t.Errorf("the conversation came back with %d messages, want %d", got.Conversation.Len(), want.Conversation.Len())
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("timestamps came back as %v/%v, want %v/%v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}
}

// TestEmptyThreadMarshalsWithAnEmptyConversation pins the shape a front end
// receives for a thread nobody has spoken in: an array, never null.
func TestEmptyThreadMarshalsWithAnEmptyConversation(t *testing.T) {
	thread := &chat.Thread{ID: "t1"}
	encoded, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("marshalling a thread: %v", err)
	}
	var back chat.Thread
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("unmarshalling a thread: %v", err)
	}
	if back.Conversation.Len() != 0 {
		t.Errorf("the conversation came back with %d messages, want 0", back.Conversation.Len())
	}
	if !strings.Contains(string(encoded), `"conversation":[]`) {
		t.Errorf("an empty thread marshalled as %s, want an empty conversation array", encoded)
	}
}

// TestThreadCloneIsDeep guards what a store relies on: a cloned thread shares
// no history with its original.
func TestThreadCloneIsDeep(t *testing.T) {
	original := chat.NewThread()
	original.Conversation = goodall.Conversation{}.Append(goodall.UserMessage(goodall.Text{Text: "one"}))

	clone := original.Clone()
	clone.Conversation = clone.Conversation.Append(goodall.UserMessage(goodall.Text{Text: "two"}))
	clone.Usage.Input = 99
	clone.ID = "changed"

	if original.Conversation.Len() != 1 {
		t.Errorf("the original grew to %d messages", original.Conversation.Len())
	}
	if original.Usage.Input != 0 {
		t.Errorf("the original's usage changed to %d", original.Usage.Input)
	}
	if original.ID == "changed" {
		t.Error("the original's id changed")
	}
}
