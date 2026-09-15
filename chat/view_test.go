package chat_test

import (
	"encoding/base64"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/fake"
)

// The strings a front end must never be handed. Each one is planted in the
// thread the tests below build, and every assertion about redaction is "this
// string is nowhere in the serialized view".
const (
	secretToolInput  = "SECRET-INPUT"
	secretToolResult = "SECRET-RESULT"
	secretToolError  = "SECRET-FAILURE"
	secretPixels     = "SECRET-PIXELS"
	secretSignature  = "SECRET-SIGNATURE"
	secretRedacted   = "SECRET-REDACTED"
	secretUnknown    = "SECRET-UNKNOWN"
	secretSystem     = "SECRET-SYSTEM-PROMPT"
)

// viewThread is a thread carrying one of every block kind, each with a secret
// planted wherever the view is supposed to withhold one.
func viewThread(id string) *chat.Thread {
	created := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	return &chat.Thread{
		ID: id,
		Conversation: goodall.Conversation{}.Append(
			goodall.UserMessage(
				goodall.Text{Text: "What is in this picture?"},
				goodall.Image{Source: goodall.BytesSource("image/png", []byte(secretPixels))},
				goodall.Image{Source: goodall.URLSource("https://example.com/cat.png")},
				goodall.Document{Source: goodall.FileSource("file_123"), Title: "Report"},
			),
			goodall.AssistantMessage(
				goodall.Thinking{Text: "weighing it", Signature: secretSignature},
				goodall.Text{Text: "Let me look it up."},
				goodall.ToolUse{ID: "toolu_1", Name: "search", Input: jsontext.Value(`{"query":"` + secretToolInput + `"}`)},
			),
			goodall.UserMessage(
				goodall.ToolResult{ToolUseID: "toolu_1", Content: goodall.Blocks{goodall.Text{Text: secretToolResult}}},
				goodall.ToolResult{ToolUseID: "toolu_2", IsError: true, Content: goodall.Blocks{goodall.Text{Text: secretToolError}}},
			),
			goodall.Message{Role: goodall.RoleAssistant, Partial: true, Content: goodall.Blocks{
				goodall.Text{Text: "Here is what I fou"},
				goodall.RedactedThinking{Data: secretRedacted},
				goodall.Unknown{Type: "server_tool_use", Raw: jsontext.Value(`{"type":"server_tool_use","id":"` + secretUnknown + `"}`)},
			}},
		),
		Usage:     goodall.Usage{Input: 120, Output: 45},
		Version:   3,
		CreatedAt: created,
		UpdatedAt: created.Add(time.Minute),
	}
}

// displayedOptions are the view options of an agent with a system prompt that
// shows its thinking.
var displayedOptions = chat.ViewOptions{HasSystemPrompt: true, ThinkingDisplay: goodall.DisplaySummarized}

// TestThreadViewKeepsWhatAFrontEndNeeds is the shape of the view: every block
// arrives, in order, with the fields a UI renders.
func TestThreadViewKeepsWhatAFrontEndNeeds(t *testing.T) {
	view := chat.NewThreadView(viewThread("thread-1"), displayedOptions)

	if view.ID != "thread-1" || view.Version != 3 {
		t.Errorf("the view is for thread %q at version %d, want thread-1 at 3", view.ID, view.Version)
	}
	if view.Usage != (goodall.Usage{Input: 120, Output: 45}) {
		t.Errorf("the view reports usage %+v, want the thread's", view.Usage)
	}
	if view.CreatedAt.IsZero() || view.UpdatedAt.IsZero() {
		t.Errorf("the view lost the thread's timestamps: %+v", view)
	}
	if len(view.Messages) != 4 {
		t.Fatalf("the view has %d messages, want 4", len(view.Messages))
	}

	user := view.Messages[0]
	if user.Role != goodall.RoleUser {
		t.Errorf("the first message is a %s message, want user", user.Role)
	}
	wantUser := []chat.BlockView{
		{Kind: chat.BlockViewText, Text: "What is in this picture?"},
		{Kind: chat.BlockViewImage, Media: &chat.MediaView{Source: goodall.SourceBytes, MediaType: "image/png", Placeholder: user.Content[1].Media.Placeholder}},
		{Kind: chat.BlockViewImage, Media: &chat.MediaView{Source: goodall.SourceURL, URL: "https://example.com/cat.png"}},
		{Kind: chat.BlockViewDocument, Media: &chat.MediaView{Source: goodall.SourceFile, FileID: "file_123", Title: "Report"}},
	}
	if !reflect.DeepEqual(user.Content, wantUser) {
		t.Errorf("the user turn is\n\t%+v\nwant\n\t%+v", user.Content, wantUser)
	}
	if user.Content[1].Media.Placeholder == "" {
		t.Error("the inline image has no placeholder id")
	}

	answer := view.Messages[1]
	if answer.Role != goodall.RoleAssistant || answer.Partial {
		t.Errorf("the second message is %s (partial=%v), want a complete assistant turn", answer.Role, answer.Partial)
	}
	if got := answer.Content[0]; got.Kind != chat.BlockViewThinking || got.Text != "weighing it" {
		t.Errorf("the thinking block is %+v, want its summary text", got)
	}
	call := answer.Content[2]
	if call.Kind != chat.BlockViewToolCall || call.ToolName != "search" || call.ToolUseID != "toolu_1" {
		t.Errorf("the tool call is %+v, want a named call with its id", call)
	}
	if call.Placeholder == "" {
		t.Error("the tool call has no placeholder id standing in for its input")
	}

	results := view.Messages[2].Content
	if len(results) != 2 {
		t.Fatalf("the tool results turn has %d blocks, want 2", len(results))
	}
	if results[0].Kind != chat.BlockViewToolResult || results[0].Status != chat.ToolResultOK || results[0].ToolUseID != "toolu_1" {
		t.Errorf("the successful result is %+v, want an ok result for toolu_1", results[0])
	}
	if results[1].Status != chat.ToolResultError {
		t.Errorf("the failed result has status %q, want %q", results[1].Status, chat.ToolResultError)
	}
	for i, r := range results {
		if r.Placeholder == "" {
			t.Errorf("tool result %d has no placeholder id", i)
		}
	}

	partial := view.Messages[3]
	if !partial.Partial {
		t.Error("the interrupted assistant turn is not marked partial")
	}
	if got := partial.Content[1]; got.Kind != chat.BlockViewRedactedThinking || got.Text != "" {
		t.Errorf("the redacted thinking block is %+v, want a marker with no text", got)
	}
	if got := partial.Content[2]; got.Kind != chat.BlockViewUnknown || got.BlockType != "server_tool_use" {
		t.Errorf("the unknown block is %+v, want a marker naming its type", got)
	}
}

// TestThreadViewWithholdsEverySecret is the criterion: nothing the back end
// owns — tool input, tool results, inline media, thinking signatures,
// provider bytes — reaches the serialized view.
func TestThreadViewWithholdsEverySecret(t *testing.T) {
	view := chat.NewThreadView(viewThread("thread-1"), displayedOptions)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshalling the view: %v", err)
	}
	for _, secret := range []string{
		secretToolInput, secretToolResult, secretToolError, secretPixels,
		secretSignature, secretRedacted, secretUnknown,
		base64.StdEncoding.EncodeToString([]byte(secretPixels)),
	} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the serialized view carries %q:\n%s", secret, encoded)
		}
	}
}

// TestThreadViewPlacesTheSystemPromptWithoutItsText is the other half of the
// redaction: the prompt is announced by a placeholder, and an agent without
// one announces nothing.
func TestThreadViewPlacesTheSystemPromptWithoutItsText(t *testing.T) {
	with := chat.NewThreadView(viewThread("thread-1"), displayedOptions)
	if with.System == "" {
		t.Error("an agent with a system prompt produced no system placeholder")
	}
	without := chat.NewThreadView(viewThread("thread-1"), chat.ViewOptions{})
	if without.System != "" {
		t.Errorf("an agent with no system prompt produced the placeholder %q", without.System)
	}
}

// TestThreadViewOmitsThinkingTextWhenTheDisplayIsOmitted keeps the view
// honest about the agent's own setting: the block is still there, because the
// UI shows that the model thought, but its text is not.
func TestThreadViewOmitsThinkingTextWhenTheDisplayIsOmitted(t *testing.T) {
	view := chat.NewThreadView(viewThread("thread-1"), chat.ViewOptions{ThinkingDisplay: goodall.DisplayOmitted})
	got := view.Messages[1].Content[0]
	if got.Kind != chat.BlockViewThinking {
		t.Fatalf("the first block of the answer is %q, want a thinking block", got.Kind)
	}
	if got.Text != "" {
		t.Errorf("the thinking block carries %q, want no text under DisplayOmitted", got.Text)
	}
}

// TestThreadViewIdsAreStableUniqueAndThreadScoped is the criterion for the
// placeholders: two computations of one thread agree, no two blocks share an
// id, and an identical thread under another id shares none of them.
func TestThreadViewIdsAreStableUniqueAndThreadScoped(t *testing.T) {
	first := chat.NewThreadView(viewThread("thread-1"), displayedOptions)
	second := chat.NewThreadView(viewThread("thread-1"), displayedOptions)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two computations of one thread differ:\n\t%+v\n\t%+v", first, second)
	}

	ids := placeholders(first)
	if len(ids) < 5 {
		t.Fatalf("the view has %d placeholders, want one for the prompt, the inline image, the tool call and both results", len(ids))
	}
	seen := make(map[string]int, len(ids))
	for _, id := range ids {
		if len(id) != 32 || strings.TrimLeft(id, "0123456789abcdef") != "" {
			t.Errorf("the placeholder %q is not a truncated hex digest", id)
		}
		seen[id]++
	}
	if len(seen) != len(ids) {
		t.Errorf("two blocks of one thread share a placeholder id: %v", ids)
	}

	other := placeholders(chat.NewThreadView(viewThread("thread-2"), displayedOptions))
	for _, id := range other {
		if seen[id] > 0 {
			t.Errorf("the id %q appears in both threads, so it leaks that their content matches", id)
		}
	}
}

// TestThreadViewOfNothing is the nil case, which mirrors Thread.Clone.
func TestThreadViewOfNothing(t *testing.T) {
	if got := chat.NewThreadView(nil, displayedOptions); got != nil {
		t.Errorf("the view of a nil thread is %+v, want nil", got)
	}
}

// TestServiceViewRedactsTheAgentsSystemPrompt drives the whole path a front
// end uses: a real agent with a real system prompt, a real run, and a view
// that carries neither the prompt nor the tool traffic.
func TestServiceViewRedactsTheAgentsSystemPrompt(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, goodall.Text{Text: "checking"}, fake.Use("tu_1", "echo", `{"text":"`+secretToolInput+`"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "all done"}),
	}, echoTool(t))
	agent.System = secretSystem
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	waitForVersion(t, svc, thread.ID, 2)

	view, err := svc.View(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.System == "" {
		t.Error("the agent has a system prompt and the view announces none")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshalling the view: %v", err)
	}
	for _, secret := range []string{secretSystem, secretToolInput, "echo: " + secretToolInput} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the serialized view carries %q:\n%s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "all done") {
		t.Errorf("the view lost the answer itself:\n%s", encoded)
	}

	if _, err := svc.View(t.Context(), "no-such-thread"); !errors.Is(err, chat.ErrThreadNotFound) {
		t.Errorf("View of a missing thread gave %v, want ErrThreadNotFound", err)
	}
}

// placeholders collects every opaque id in a view, so a test can assert they
// are unique and thread-scoped.
func placeholders(view *chat.ThreadView) []string {
	var out []string
	if view.System != "" {
		out = append(out, view.System)
	}
	for _, m := range view.Messages {
		for _, b := range m.Content {
			if b.Placeholder != "" {
				out = append(out, b.Placeholder)
			}
			if b.Media != nil && b.Media.Placeholder != "" {
				out = append(out, b.Media.Placeholder)
			}
		}
	}
	return out
}
