package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/examples/internal/render"
	"github.com/bensyverson/goodall/internal/fake"
)

// syncBuffer is an io.Writer a test can read while the loop is still writing
// to it, which is how a test waits for the answer to reach a known point.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write appends to the buffer under the lock.
func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// String is everything written so far.
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFor blocks until the output contains want, or fails the test.
func (s *syncBuffer) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if strings.Contains(s.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the output never contained %q after ten seconds; it was:\n%s", want, s.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// loopService is a service over the scripted provider and the example's own
// tool, plus the id of a thread to talk on.
func loopService(t *testing.T, script []fake.Turn) (*chat.Service, string) {
	t.Helper()
	tool, err := newDiceTool()
	if err != nil {
		t.Fatalf("newDiceTool: %v", err)
	}
	agent := &goodall.Agent{Provider: &fake.Provider{Script: script}, Model: "fake-model", Tools: []goodall.Tool{tool}}
	svc := chat.NewService(agent, chat.NewMemoryStore())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.Shutdown(ctx); err != nil {
			t.Errorf("shutting the service down: %v", err)
		}
	})
	thread, err := svc.Create(t.Context())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return svc, thread.ID
}

// options are the loop's options for a test, with an interrupt channel the
// test triggers where a user would press Ctrl-C.
func options(threadID string, interrupt <-chan struct{}) Options {
	return Options{ThreadID: threadID, Prompt: "> ", Interrupt: interrupt}
}

func TestLoopStreamsThinkingTextAndUsage(t *testing.T) {
	script := []fake.Turn{
		fake.Answer(goodall.StopEndTurn,
			goodall.Thinking{Text: "weighing it up", Signature: "sig"},
			goodall.Text{Text: "hello there"},
		).Using(goodall.Usage{Input: 12, Output: 34}),
	}
	svc, threadID := loopService(t, script)

	out := &syncBuffer{}
	if err := Loop(t.Context(), svc, strings.NewReader("hi\n"), out, options(threadID, nil)); err != nil {
		t.Fatalf("Loop: %v", err)
	}

	got := out.String()
	// The transcript under -v is this example's preview: the rendering is
	// the thing being built, and a human should be able to look at it.
	t.Logf("transcript:\n%s", got)
	for _, want := range []string{"> ", render.ThinkingLabel, "weighing it up", "hello there", "12 in", "34 out"} {
		if !strings.Contains(got, want) {
			t.Errorf("the output does not contain %q; it was:\n%s", want, got)
		}
	}
	if !strings.Contains(got, render.ThinkingPrefix+"weighing it up") {
		t.Errorf("the thinking text is not marked with %q; the output was:\n%s", render.ThinkingPrefix, got)
	}
}

func TestLoopShowsToolCallsAsTheyStartAndEnd(t *testing.T) {
	script := []fake.Turn{
		fake.Answer(goodall.StopToolUse,
			goodall.Text{Text: "rolling"},
			fake.Use("tu_1", "roll_dice", `{"sides":6,"count":2}`),
		),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "there you go"}),
	}
	svc, threadID := loopService(t, script)

	out := &syncBuffer{}
	if err := Loop(t.Context(), svc, strings.NewReader("roll two dice\n"), out, options(threadID, nil)); err != nil {
		t.Fatalf("Loop: %v", err)
	}

	got := out.String()
	t.Logf("transcript:\n%s", got)
	for _, want := range []string{"roll_dice", `{"sides":6,"count":2}`, string(render.StatusOK), "there you go"} {
		if !strings.Contains(got, want) {
			t.Errorf("the output does not contain %q; it was:\n%s", want, got)
		}
	}
}

func TestLoopSendsEveryLineOnOneThread(t *testing.T) {
	script := []fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "first answer"}),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "second answer"}),
	}
	svc, threadID := loopService(t, script)

	out := &syncBuffer{}
	if err := Loop(t.Context(), svc, strings.NewReader("one\ntwo\n"), out, options(threadID, nil)); err != nil {
		t.Fatalf("Loop: %v", err)
	}

	thread, err := svc.Get(t.Context(), threadID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got, want := thread.Conversation.Len(), 4; got != want {
		t.Fatalf("the thread has %d messages, want %d: %v", got, want, texts(thread.Conversation))
	}
	if got := texts(thread.Conversation); got[0] != "one" || got[2] != "two" {
		t.Errorf("the thread's user messages are %q, want the two lines that were typed", got)
	}
}

func TestLoopStopsMidAnswerAndTheNextTurnContinues(t *testing.T) {
	script := []fake.Turn{
		fake.Stalled(goodall.Text{Text: "starting to answer"}),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "the second answer"}),
	}
	svc, threadID := loopService(t, script)

	reader, writer := io.Pipe()
	out := &syncBuffer{}
	interrupt := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- Loop(t.Context(), svc, reader, out, options(threadID, interrupt)) }()

	if _, err := io.WriteString(writer, "first\n"); err != nil {
		t.Fatalf("writing the first line: %v", err)
	}
	// Wait until the stalled turn has really reached the provider, so the
	// stop cancels a run that is under way rather than one about to start.
	out.waitFor(t, "starting to answer")
	interrupt <- struct{}{}
	out.waitFor(t, render.CutShort)

	if _, err := io.WriteString(writer, "second\n"); err != nil {
		t.Fatalf("writing the second line: %v", err)
	}
	out.waitFor(t, "the second answer")
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the input: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Loop: %v", err)
	}

	thread, err := svc.Get(t.Context(), threadID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var partial *goodall.Message
	for msg := range thread.Conversation.All() {
		if msg.Role == goodall.RoleAssistant && msg.Partial {
			partial = &msg
			break
		}
	}
	if partial == nil {
		t.Fatalf("the thread carries no partial assistant message: %q", texts(thread.Conversation))
	}
	if got := partial.Text(); got != "starting to answer" {
		t.Errorf("the partial message says %q, want the text that had arrived when the run was stopped", got)
	}
	last, _ := thread.Conversation.Last()
	if last.Role != goodall.RoleAssistant || last.Partial || last.Text() != "the second answer" {
		t.Errorf("the thread ends in %s %q (partial %v), want the complete second answer", last.Role, last.Text(), last.Partial)
	}
}

func TestLoopEndsOnAnInterruptAtThePrompt(t *testing.T) {
	svc, threadID := loopService(t, nil)

	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	out := &syncBuffer{}
	interrupt := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- Loop(t.Context(), svc, reader, out, options(threadID, interrupt)) }()

	out.waitFor(t, "> ")
	interrupt <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Loop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not end ten seconds after the interrupt at the prompt")
	}
}

// texts is every message's text, for a failure message that reads.
func texts(conv goodall.Conversation) []string {
	var out []string
	for msg := range conv.All() {
		out = append(out, msg.Text())
	}
	return out
}
