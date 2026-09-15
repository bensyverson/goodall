package goodall_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// recorder is a tool that remembers every input it was actually called with,
// which is how these tests tell a tool that ran from one a hook stopped.
type recorder struct {
	mu     sync.Mutex
	inputs []string
}

// tool is the recorder as an agent tool, answering with the text it was given.
func (rec *recorder) tool(t *testing.T) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("echo", "Echo the text back to the model.", func(ctx context.Context, in struct {
		Text string `json:"text" desc:"the text to echo"`
	}) (goodall.ToolResult, error) {
		rec.mu.Lock()
		rec.inputs = append(rec.inputs, in.Text)
		rec.mu.Unlock()
		return goodall.TextResult("echo: " + in.Text), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// seen is the inputs the tool was called with, in the order the calls arrived.
func (rec *recorder) seen() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]string(nil), rec.inputs...)
}

// trace records hook calls in order, so a test can assert on the sequence the
// loop applied them in rather than on each hook alone.
type trace struct {
	mu    sync.Mutex
	steps []string
}

func (tr *trace) add(step string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.steps = append(tr.steps, step)
}

func (tr *trace) all() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]string(nil), tr.steps...)
}

// toolTurn is one scripted turn asking for the echo tool with the given ids.
func toolTurn(ids ...string) fake.Turn {
	blocks := make([]goodall.Block, len(ids))
	for i, id := range ids {
		blocks[i] = fake.Use(id, "echo", `{"text":"`+id+`"}`)
	}
	return fake.Answer(goodall.StopToolUse, blocks...)
}

// countEvents is how many events of a type a run emitted.
func countEvents(events []goodall.Event, want goodall.EventType) int {
	n := 0
	for _, ev := range events {
		if ev.Type() == want {
			n++
		}
	}
	return n
}

// TestHooksRunInOrderOverAToolRun is the whole shape of the hook set: each
// hook fires once per thing it watches, and they interleave in the order the
// loop does the work.
func TestHooksRunInOrderOverAToolRun(t *testing.T) {
	var tr trace
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, rec.tool(t))
	a.Hooks = goodall.Hooks{
		BeforeSend: func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
			tr.add("before_send")
			return nil
		},
		AfterReceive: func(ctx context.Context, resp *goodall.Response) error {
			tr.add("after_receive:" + string(resp.StopReason))
			return nil
		},
		BeforeToolCall: func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
			tr.add("before_tool:" + use.ID)
			return goodall.Allow(), nil
		},
		AfterToolCall: func(ctx context.Context, use goodall.ToolUse, result *goodall.ToolResult) error {
			tr.add("after_tool:" + use.ID)
			return nil
		},
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	terminalDone(t, events)

	want := []string{
		"before_send",
		"after_receive:tool_use",
		"before_tool:tu_1",
		"after_tool:tu_1",
		"before_send",
		"after_receive:end_turn",
	}
	if got := tr.all(); !reflect.DeepEqual(got, want) {
		t.Errorf("hook trace =\n\t%v\nwant\n\t%v", got, want)
	}
	if got := rec.seen(); len(got) != 1 {
		t.Errorf("the tool ran %d times, want once", len(got))
	}
}

// TestBeforeSendSeesOnlyTheNewTurn is the criterion: the hook is handed the
// parameters and the uncommitted turn, and no history at all, on the first
// turn and on the tool-results turn alike.
func TestBeforeSendSeesOnlyTheNewTurn(t *testing.T) {
	rec := &recorder{}
	a, p := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, rec.tool(t))

	type seen struct {
		messages int
		role     goodall.Role
		text     string
		blocks   int
		nilTurn  bool
	}
	var got []seen
	a.Hooks.BeforeSend = func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		s := seen{messages: len(req.Messages), nilTurn: newTurn == nil}
		if newTurn != nil {
			s.role, s.text, s.blocks = newTurn.Role, newTurn.Text(), len(newTurn.Content)
		}
		got = append(got, s)
		return nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	terminalDone(t, events)

	if len(got) != 2 {
		t.Fatalf("BeforeSend ran %d times, want twice", len(got))
	}
	for i, s := range got {
		if s.messages != 0 {
			t.Errorf("turn %d: the hook saw %d messages on the request, want none", i+1, s.messages)
		}
		if s.nilTurn {
			t.Errorf("turn %d: the hook saw no new turn, want one", i+1)
			continue
		}
		if s.role != goodall.RoleUser {
			t.Errorf("turn %d: the new turn is from %q, want the user", i+1, s.role)
		}
	}
	if got[0].text != "say hi" {
		t.Errorf("turn 1's new turn says %q, want the caller's input", got[0].text)
	}
	if got[1].blocks != 1 || got[1].text != "" {
		t.Errorf("turn 2's new turn = %d blocks saying %q, want the one tool result", got[1].blocks, got[1].text)
	}
	// The loop fills the messages in after the hook, so what was sent is
	// the whole history even though the hook saw none of it.
	if reqs := p.Requests(); len(reqs) != 2 || len(reqs[1].Messages) != 3 {
		t.Errorf("the second request carried %d messages, want the three of the history", len(p.Requests()[1].Messages))
	}
}

// TestBeforeSendSeesNoTurnOnAContinuation covers a Run with no input over a
// conversation that already ends in a user message: there is no new turn, and
// the hook is told so rather than being handed the committed last message.
func TestBeforeSendSeesNoTurnOnAContinuation(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	})
	var calls, nils int
	a.Hooks.BeforeSend = func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		calls++
		if newTurn == nil {
			nils++
		}
		return nil
	}

	conv := goodall.Conversation{}.Append(goodall.UserMessage(goodall.Text{Text: "already here"}))
	events := runEvents(t, a, conv)
	terminalDone(t, events)

	if calls != 1 || nils != 1 {
		t.Errorf("BeforeSend ran %d times with %d nil turns, want one of each", calls, nils)
	}
}

// TestBeforeSendCannotAlterAnEarlierMessage is the criterion: history is
// append-only (invariant 1), so a hook writing into the request's messages
// changes nothing that is sent or committed.
func TestBeforeSendCannotAlterAnEarlierMessage(t *testing.T) {
	rec := &recorder{}
	a, p := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, rec.tool(t))
	a.Hooks.BeforeSend = func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		req.Messages = append(req.Messages, goodall.UserMessage(goodall.Text{Text: "forged"}))
		if newTurn != nil {
			// Editing the new turn is allowed and must not reach back.
			newTurn.Content = append(newTurn.Content, goodall.Text{Text: ""})
		}
		return nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	conv := terminalDone(t, events).Result.Conversation

	for _, req := range p.Requests() {
		for i, m := range req.Messages {
			if strings.Contains(m.Text(), "forged") {
				t.Errorf("message %d of a sent request says %q, want nothing the hook wrote into Messages", i, m.Text())
			}
		}
	}
	if conv.At(0).Text() != "say hi" {
		t.Errorf("the committed first message says %q, want the caller's input untouched", conv.At(0).Text())
	}
}

// TestBeforeSendEditsTheNewTurn is the other half of the same rule: the turn
// that has not been committed yet is the hook's to shape, and what it shapes
// is what is sent and what is kept.
func TestBeforeSendEditsTheNewTurn(t *testing.T) {
	a, p := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	})
	a.Hooks.BeforeSend = func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		newTurn.Content = goodall.Blocks{goodall.Text{Text: "shaped by the hook"}}
		return nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "raw"})
	conv := terminalDone(t, events).Result.Conversation

	reqs := p.Requests()
	if len(reqs) != 1 || len(reqs[0].Messages) != 1 {
		t.Fatalf("the run sent %d requests, want one carrying the one message", len(reqs))
	}
	if got := reqs[0].Messages[0].Text(); got != "shaped by the hook" {
		t.Errorf("the sent message says %q, want the hook's edit", got)
	}
	if got := conv.At(0).Text(); got != "shaped by the hook" {
		t.Errorf("the committed message says %q, want the hook's edit", got)
	}
}

// TestBeforeSendErrorStopsTheRun checks the short-circuit the predecessor
// promised and did not do: the run ends on the hook's error, nothing is sent,
// and the turn the hook saw is committed so the conversation is still whole.
func TestBeforeSendErrorStopsTheRun(t *testing.T) {
	a, p := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "never sent"}),
	})
	a.Hooks.BeforeSend = func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		return errors.New("the hook said no")
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseHook {
		t.Errorf("the run stopped because %q, want %q", stopped.Cause, goodall.StopCauseHook)
	}
	if stopped.Message != "the hook said no" {
		t.Errorf("the stop message is %q, want the hook's error", stopped.Message)
	}
	if stopped.Kind != "" {
		t.Errorf("the stop carries kind %q, want none: a hook is not a provider failure", stopped.Kind)
	}
	if p.Calls() != 0 {
		t.Errorf("the provider was called %d times, want none", p.Calls())
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 1 || conv.At(0).Text() != "say hi" {
		t.Errorf("the conversation is %d messages, want the one input committed", conv.Len())
	}
}

// TestAfterReceiveErrorCommitsTheTurn ends a run between the answer and its
// tools: the assistant turn is kept, its calls get error results so nothing
// dangles (invariant 9), and no tool runs.
func TestAfterReceiveErrorCommitsTheTurn(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2"),
	}, rec.tool(t))
	a.Hooks.AfterReceive = func(ctx context.Context, resp *goodall.Response) error {
		return errors.New("the answer is unacceptable")
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseHook || stopped.Message != "the answer is unacceptable" {
		t.Errorf("the run stopped %q: %q, want the hook's error", stopped.Cause, stopped.Message)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Errorf("the tool ran %d times, want never", len(got))
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 3 {
		t.Fatalf("the conversation is %d messages, want the input, the turn and its results", conv.Len())
	}
	results := toolResults(conv.At(2))
	if len(results) != 2 {
		t.Fatalf("the results message carries %d results, want one per call", len(results))
	}
	for i, r := range results {
		if !r.IsError || r.ToolUseID == "" {
			t.Errorf("result %d = %+v, want an error result naming its call", i, r)
		}
	}
	if countEvents(events, goodall.EventToolCallStart) != 0 {
		t.Error("a tool call was announced, want none")
	}
}

// TestAfterReceiveShapesTheResponse: the hook holds the whole response by
// pointer, so what it changes is what the loop acts on and commits.
func TestAfterReceiveShapesTheResponse(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "my phone number is 555"}),
	})
	a.Hooks.AfterReceive = func(ctx context.Context, resp *goodall.Response) error {
		resp.Message.Content = goodall.Blocks{goodall.Text{Text: "redacted"}}
		return nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "hi"})
	result := terminalDone(t, events).Result

	if got := result.Conversation.At(1).Text(); got != "redacted" {
		t.Errorf("the committed answer says %q, want the shaped one", got)
	}
}

// TestBeforeToolCallDenySkipsTheTool: a denied call never runs, the model is
// told why in an error result, and the run carries on to the next turn.
func TestBeforeToolCallDenySkipsTheTool(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "understood"}),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		return goodall.Deny("that tool is off limits today"), nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	result := terminalDone(t, events).Result

	if got := rec.seen(); len(got) != 0 {
		t.Errorf("the tool ran %d times, want never", len(got))
	}
	results := toolResults(result.Conversation.At(2))
	if len(results) != 1 {
		t.Fatalf("the results message carries %d results, want one", len(results))
	}
	if !results[0].IsError || !strings.Contains(results[0].Text(), "off limits") {
		t.Errorf("the result is %+v, want an error result carrying the reason", results[0])
	}
	// A denied call was never about to run, so it is never announced; its
	// end still reports the result, which is what a UI draws.
	if n := countEvents(events, goodall.EventToolCallStart); n != 0 {
		t.Errorf("%d tool calls were announced, want none: nothing ran", n)
	}
	if n := countEvents(events, goodall.EventToolCallEnd); n != 1 {
		t.Errorf("%d tool call ends, want one carrying the denial", n)
	}
}

// TestBeforeToolCallModifyKeepsTheModelsCall: the tool runs on the hook's
// input while the conversation keeps what the model actually said
// (invariant 1).
func TestBeforeToolCallModifyKeepsTheModelsCall(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		return goodall.Modify(jsontext.Value(`{"text":"sanitized"}`)), nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	result := terminalDone(t, events).Result

	if got := rec.seen(); len(got) != 1 || got[0] != "sanitized" {
		t.Errorf("the tool saw %q, want the hook's input", got)
	}
	uses := result.Conversation.At(1).ToolUses()
	if len(uses) != 1 || string(uses[0].Input) != `{"text":"tu_1"}` {
		t.Errorf("the committed call is %+v, want the model's own input", uses)
	}
	for _, ev := range events {
		if start, ok := ev.(goodall.ToolCallStart); ok {
			if string(start.ToolUse.Input) != `{"text":"sanitized"}` {
				t.Errorf("ToolCallStart carried %s, want the input that is really running", start.ToolUse.Input)
			}
		}
	}
	if got := toolResults(result.Conversation.At(2))[0].Text(); got != "echo: sanitized" {
		t.Errorf("the result says %q, want the modified call's answer", got)
	}
}

// TestBeforeToolCallDeferPausesTheTurn: one deferred call holds the whole
// turn, because approval is a pause at a turn boundary and a UI approves the
// turn rather than one call of it.
func TestBeforeToolCallDeferPausesTheTurn(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2"),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		if use.ID == "tu_2" {
			return goodall.Defer(), nil
		}
		return goodall.Allow(), nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseDeferred {
		t.Errorf("the run stopped because %q, want %q", stopped.Cause, goodall.StopCauseDeferred)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Errorf("the tool ran %d times, want never: one deferred call holds the turn", len(got))
	}
	pending := stopped.Result.Pending
	if len(pending) != 2 || pending[0].ID != "tu_1" || pending[1].ID != "tu_2" {
		t.Fatalf("pending = %+v, want both calls in the order the model made them", pending)
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 2 {
		t.Fatalf("the conversation is %d messages, want the input and the assistant turn with no results", conv.Len())
	}
	if len(conv.At(1).ToolUses()) != 2 {
		t.Errorf("the committed turn carries %d calls, want both", len(conv.At(1).ToolUses()))
	}
	if n := countEvents(events, goodall.EventToolCallStart) + countEvents(events, goodall.EventToolCallEnd); n != 0 {
		t.Errorf("%d tool call events, want none: a deferred call has not happened", n)
	}
}

// TestBeforeToolCallErrorRunsNothing is the criterion: a hook error ends the
// run with no tool executed, and leaves a conversation that can still be sent.
func TestBeforeToolCallErrorRunsNothing(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2"),
	}, rec.tool(t))
	var asked []string
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		asked = append(asked, use.ID)
		return goodall.Allow(), errors.New("the approver is unreachable")
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseHook || stopped.Message != "the approver is unreachable" {
		t.Errorf("the run stopped %q: %q, want the hook's error", stopped.Cause, stopped.Message)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Errorf("the tool ran %d times, want never", len(got))
	}
	if len(asked) != 1 {
		t.Errorf("the hook was asked about %v, want only the first call: the first error ends it", asked)
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 3 {
		t.Fatalf("the conversation is %d messages, want the input, the turn and its results", conv.Len())
	}
	results := toolResults(conv.At(2))
	if len(results) != 2 {
		t.Fatalf("the results message carries %d results, want one per call", len(results))
	}
	for i, r := range results {
		if !r.IsError {
			t.Errorf("result %d = %+v, want an error result", i, r)
		}
	}
	if results[0].ToolUseID != "tu_1" || results[1].ToolUseID != "tu_2" {
		t.Errorf("the results name %q and %q, want the two calls in order", results[0].ToolUseID, results[1].ToolUseID)
	}
}

// TestAfterToolCallShapesTheResult: the hook holds the result by pointer, so
// truncation or redaction there is what the consumer sees and what the model
// is told.
func TestAfterToolCallShapesTheResult(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, rec.tool(t))
	a.Hooks.AfterToolCall = func(ctx context.Context, use goodall.ToolUse, result *goodall.ToolResult) error {
		result.Content = goodall.Blocks{goodall.Text{Text: "shortened"}}
		return nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	result := terminalDone(t, events).Result

	for _, ev := range events {
		if end, ok := ev.(goodall.ToolCallEnd); ok {
			if end.Result.Text() != "shortened" {
				t.Errorf("ToolCallEnd carried %q, want the shaped result", end.Result.Text())
			}
		}
	}
	if got := toolResults(result.Conversation.At(2))[0].Text(); got != "shortened" {
		t.Errorf("the committed result says %q, want the shaped one", got)
	}
}

// TestAfterToolCallErrorKeepsWhatFinished: the run ends, the results that had
// already passed the hook are kept, and every call the loop could not account
// for still gets a result so the conversation stays sendable.
func TestAfterToolCallErrorKeepsWhatFinished(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2", "tu_3"),
	}, rec.tool(t))
	seen := 0
	a.Hooks.AfterToolCall = func(ctx context.Context, use goodall.ToolUse, result *goodall.ToolResult) error {
		seen++
		if seen == 2 {
			return errors.New("the result store is down")
		}
		return nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseHook || stopped.Message != "the result store is down" {
		t.Errorf("the run stopped %q: %q, want the hook's error", stopped.Cause, stopped.Message)
	}
	if got := rec.seen(); len(got) != 3 {
		t.Errorf("the tool ran %d times, want all three: the run ends after the turn's tools finish", len(got))
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 3 {
		t.Fatalf("the conversation is %d messages, want the input, the turn and its results", conv.Len())
	}
	results := toolResults(conv.At(2))
	if len(results) != 3 {
		t.Fatalf("the results message carries %d results, want one per call", len(results))
	}
	kept, dropped := 0, 0
	for _, r := range results {
		if r.IsError {
			dropped++
		} else {
			kept++
		}
	}
	if kept != 1 || dropped != 2 {
		t.Errorf("%d results kept and %d replaced, want the one that passed the hook kept", kept, dropped)
	}
	if n := countEvents(events, goodall.EventToolCallEnd); n != 1 {
		t.Errorf("%d tool call ends, want only the one that passed the hook", n)
	}
}

// TestDecisionString names each decision for a log line, and is the only
// thing that tells one printed Decision from another: the kind and its
// payload are unexported.
func TestDecisionString(t *testing.T) {
	cases := map[string]goodall.Decision{
		"allow":  goodall.Allow(),
		"deny":   goodall.Deny("no"),
		"modify": goodall.Modify(jsontext.Value(`{}`)),
		"defer":  goodall.Defer(),
	}
	for want, decision := range cases {
		if got := decision.String(); got != want {
			t.Errorf("Decision.String() = %q, want %q", got, want)
		}
	}
	if got := (goodall.Decision{}).String(); got != "allow" {
		t.Errorf("the zero Decision prints as %q, want %q", got, "allow")
	}
}

// TestDenyWithoutAReasonStillTellsTheModel: the result text is what the model
// reads and acts on next turn, so a refusal that says nothing at all would
// leave it guessing.
func TestDenyWithoutAReasonStillTellsTheModel(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "understood"}),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		return goodall.Deny(""), nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	result := terminalDone(t, events).Result

	results := toolResults(result.Conversation.At(2))
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("the results message carries %+v, want one error result", results)
	}
	if results[0].Text() == "" {
		t.Error("the refusal has no text at all, want it to say the call was refused")
	}
}

// TestAllowIsTheZeroDecision keeps the zero value honest: a hook that returns
// a Decision it never built behaves like no hook at all.
func TestAllowIsTheZeroDecision(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		return goodall.Decision{}, nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	terminalDone(t, events)
	if got := rec.seen(); len(got) != 1 {
		t.Errorf("the tool ran %d times, want once: the zero decision allows", len(got))
	}
}
