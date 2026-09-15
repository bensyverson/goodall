package main

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/fake"
)

// answerScript is one turn that answers in text and stops.
func answerScript(text string) []fake.Turn {
	return []fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: text})}
}

func TestIndexServesTheEmbeddedPage(t *testing.T) {
	agent, _ := agentFor(answerScript("hello"))
	srv, _ := serve(t, agent)

	resp, body := do(t, http.MethodGet, srv.URL+"/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / answered %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / answered Content-Type %q, want text/html", ct)
	}
	for _, want := range []string{"<!doctype html>", "EventSource", "/events"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Error("the page references an external URL; it must load no external assets")
	}
}

func TestCreateAnswersAnEmptyThreadView(t *testing.T) {
	agent, _ := agentFor(answerScript("hello"))
	srv, _ := serve(t, agent)

	view := createThread(t, srv)
	if view.ID == "" {
		t.Error("the created thread has no id")
	}
	if len(view.Messages) != 0 {
		t.Errorf("a new thread has %d messages, want none", len(view.Messages))
	}
	if view.Version != 1 {
		t.Errorf("a new thread is at version %d, want 1", view.Version)
	}
}

func TestViewAnswersTheStoredThread(t *testing.T) {
	agent, _ := agentFor(answerScript("Hello, world"))
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	resp, _ := sendText(t, srv, thread.ID, "hi")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send answered %d, want 200", resp.StatusCode)
	}
	// The view is polled rather than waited for on the event stream: a run
	// this short can finish between the send and the next request, and a
	// subscription to a thread with nothing in flight yields nothing. That
	// is the case the page handles by reading the view when its stream
	// closes without a terminal event.
	view := waitForMessages(t, srv, thread.ID, 2)
	if len(view.Messages) != 2 {
		t.Fatalf("the thread has %d messages, want 2", len(view.Messages))
	}
	if got := view.Messages[0].Content[0].Text; got != "hi" {
		t.Errorf("the first message reads %q, want %q", got, "hi")
	}
	if got := view.Messages[1].Content[0].Text; got != "Hello, world" {
		t.Errorf("the answer reads %q, want %q", got, "Hello, world")
	}
}

func TestViewOfAnUnknownThreadIsNotFound(t *testing.T) {
	agent, _ := agentFor(answerScript("hello"))
	srv, _ := serve(t, agent)

	resp, _ := do(t, http.MethodGet, srv.URL+"/threads/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET an unknown thread answered %d, want 404", resp.StatusCode)
	}
}

func TestSendAnswersTheRunID(t *testing.T) {
	agent, _ := agentFor(answerScript("Hello, world"))
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	resp, body := sendText(t, srv, thread.ID, "hi")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send answered %d, want 200: %s", resp.StatusCode, body)
	}
	var out struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decoding the send response %q: %v", body, err)
	}
	if out.RunID == "" {
		t.Errorf("the send response carries no run id: %s", body)
	}
	if strings.Contains(body, "conversation") || strings.Contains(body, "messages") {
		t.Errorf("the send response carries more than a run id: %s", body)
	}
}

func TestSendToAnUnknownThreadIsNotFound(t *testing.T) {
	agent, _ := agentFor(answerScript("hello"))
	srv, _ := serve(t, agent)

	resp, _ := sendText(t, srv, "nope", "hi")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("sending to an unknown thread answered %d, want 404", resp.StatusCode)
	}
}

func TestSendToABusyThreadIsConflict(t *testing.T) {
	tool, started, _ := gateTool(t)
	agent, _ := agentFor(gatedScript("checking", "done"), tool)
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	if resp, body := sendText(t, srv, thread.ID, "hi"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the first send answered %d, want 200: %s", resp.StatusCode, body)
	}
	waitFor(t, started, "the run to reach the gate tool")

	resp, _ := sendText(t, srv, thread.ID, "and again")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("sending to a busy thread answered %d, want 409", resp.StatusCode)
	}
}

func TestSendWithNoTextIsBadRequest(t *testing.T) {
	agent, _ := agentFor(answerScript("hello"))
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	resp, _ := sendText(t, srv, thread.ID, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("sending an empty line answered %d, want 400", resp.StatusCode)
	}
}

func TestStopOnAnIdleThreadIsConflict(t *testing.T) {
	agent, _ := agentFor(answerScript("hello"))
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	resp, _ := do(t, http.MethodPost, srv.URL+"/threads/"+thread.ID+"/stop", "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("stopping an idle thread answered %d, want 409", resp.StatusCode)
	}
}

func TestStopEndsTheRunWithAStoppedEvent(t *testing.T) {
	tool, started, _ := gateTool(t)
	agent, _ := agentFor(gatedScript("checking", "done"), tool)
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	sendText(t, srv, thread.ID, "hi")
	waitFor(t, started, "the run to reach the gate tool")
	stream := openEvents(t, srv, thread.ID)

	resp, body := do(t, http.MethodPost, srv.URL+"/threads/"+thread.ID+"/stop", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("stopping a running thread answered %d, want 204: %s", resp.StatusCode, body)
	}
	frames := stream.until("stopped")
	if last := frames[len(frames)-1]; !strings.Contains(last.Data, "cancelled") {
		t.Errorf("the terminal frame does not say the run was cancelled: %s", last.Data)
	}
	if rest := stream.rest(); len(rest) != 0 {
		t.Errorf("the stream carried %s after its terminal event", names(rest))
	}
}

func TestEventsOnAnIdleThreadEndsAtOnce(t *testing.T) {
	agent, _ := agentFor(answerScript("hello"))
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	resp, body := do(t, http.MethodGet, srv.URL+"/threads/"+thread.ID+"/events", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the events route answered %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("the events route answered Content-Type %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("the events route answered Cache-Control %q, want no-cache", cc)
	}
	if body != "" {
		t.Errorf("a thread with no run in flight streamed %q, want nothing", body)
	}
}

// TestEventsReplayTheBacklogAndKeepStreaming is the leaf's first criterion,
// at the layer the page uses: a client that arrives after the answer has
// begun is handed what it missed and then goes on receiving.
func TestEventsReplayTheBacklogAndKeepStreaming(t *testing.T) {
	tool, started, release := gateTool(t)
	agent, _ := agentFor(gatedScript("Let me check.", "Both cities are mild."), tool)
	srv, _ := serve(t, agent)
	thread := createThread(t, srv)

	sendText(t, srv, thread.ID, "what is the weather?")
	waitFor(t, started, "the run to reach the gate tool")

	// Nobody was listening while the first turn streamed, so everything
	// read before the gate is released is backlog.
	stream := openEvents(t, srv, thread.ID)
	backlog := stream.until("tool_call_start")
	if backlog[0].Name != "turn_start" {
		t.Errorf("the replay starts with %q, want turn_start: %s", backlog[0].Name, names(backlog))
	}
	if !containsFrame(backlog, "message_start") {
		t.Errorf("the replay carries no message_start: %s", names(backlog))
	}
	if got := textOf(t, backlog); got != "Let me check." {
		t.Errorf("the replayed text is %q, want %q", got, "Let me check.")
	}
	if last := backlog[len(backlog)-1].body(t); last.ToolUse.Name != "gate" {
		t.Errorf("the tool call names %q, want gate", last.ToolUse.Name)
	}

	// The same connection then carries the rest of the run live.
	release()
	rest := stream.until("done")
	if !containsFrame(rest, "tool_call_end") {
		t.Errorf("the live tail carries no tool_call_end: %s", names(rest))
	}
	if got := textOf(t, rest); got != "Both cities are mild." {
		t.Errorf("the live text is %q, want %q", got, "Both cities are mild.")
	}
	if tail := stream.rest(); len(tail) != 0 {
		t.Errorf("the stream carried %s after done, so an EventSource would never close", names(tail))
	}

	// What the page shows after the reload it triggers on done.
	view := waitForMessages(t, srv, thread.ID, 4)
	if got := view.Messages[3].Content[0].Text; got != "Both cities are mild." {
		t.Errorf("the stored answer reads %q, want %q", got, "Both cities are mild.")
	}
}

// TestNoRouteCarriesTheSystemPrompt is the leaf's second criterion: the
// prompt reaches the provider and reaches no response body, over a whole
// exchange including a tool call.
func TestNoRouteCarriesTheSystemPrompt(t *testing.T) {
	const prompt = "canary-system-prompt-do-not-leak"
	tool, started, release := gateTool(t)
	agent, provider := agentFor(gatedScript("Let me check.", "Sunny."), tool)
	agent.System = prompt
	srv, _ := serve(t, agent)

	var bodies []string
	record := func(what, body string) {
		if strings.Contains(body, prompt) {
			t.Errorf("the response of %s carries the system prompt: %s", what, body)
		}
		bodies = append(bodies, body)
	}

	_, page := do(t, http.MethodGet, srv.URL+"/", "")
	record("GET /", page)

	resp, created := do(t, http.MethodPost, srv.URL+"/threads", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /threads answered %d: %s", resp.StatusCode, created)
	}
	record("POST /threads", created)
	id := decodeView(t, created).ID

	_, sent := sendText(t, srv, id, "what is the weather?")
	record("POST /threads/{id}/send", sent)
	waitFor(t, started, "the run to reach the gate tool")

	stream := openEvents(t, srv, id)
	stream.until("tool_call_start")
	release()
	frames := stream.until("done")
	for _, f := range frames {
		record("a "+f.Name+" frame", f.Data)
	}

	view := waitForMessages(t, srv, id, 4)
	_, stored := do(t, http.MethodGet, srv.URL+"/threads/"+id, "")
	record("GET /threads/{id}", stored)
	if view.System == "" {
		t.Error("the view announces no system placeholder, so the page cannot tell there is a prompt")
	}
	if len(bodies) < 5 {
		t.Fatalf("only %d response bodies were checked; the exchange did not happen", len(bodies))
	}

	// The prompt is withheld from the page, not missing: the provider saw it.
	requests := provider.Requests()
	if len(requests) == 0 {
		t.Fatal("the provider was never called")
	}
	if len(requests[0].System) == 0 || requests[0].System[0].Text != prompt {
		t.Errorf("the provider was sent system %v, want the prompt", requests[0].System)
	}
}

// containsFrame reports whether a frame of that name was read.
func containsFrame(frames []sseFrame, name string) bool {
	for _, f := range frames {
		if f.Name == name {
			return true
		}
	}
	return false
}

// textOf is the text the frames' deltas add up to, which is what the page
// folds them into.
func textOf(t *testing.T, frames []sseFrame) string {
	t.Helper()
	var b strings.Builder
	for _, f := range frames {
		if f.Name == "text_delta" {
			b.WriteString(f.body(t).Text)
		}
	}
	return b.String()
}

// waitForMessages polls the view route until the thread has n messages, which
// is how a test waits for a run to persist: the run is the service's and
// nothing in the HTTP surface blocks until it has.
func waitForMessages(t *testing.T, srv *httptest.Server, threadID string, n int) chat.ThreadView {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	for {
		_, body := do(t, http.MethodGet, srv.URL+"/threads/"+threadID, "")
		view := decodeView(t, body)
		if len(view.Messages) >= n {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("the thread has %d messages after %s, want %d: %s", len(view.Messages), readTimeout, n, body)
		}
		time.Sleep(time.Millisecond)
	}
}
