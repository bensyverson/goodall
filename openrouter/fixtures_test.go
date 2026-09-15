package openrouter

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// These tests read the recordings scripts/record-fixtures made from the live
// API (see testdata/README.md for the date, the models and what the recordings
// corrected). They are what makes the offline suite a statement about
// OpenRouter rather than about what the fixture author believed OpenRouter
// does: every byte in a live_* file came off the wire.
//
// They run offline. Nothing in this file makes a network call; the recordings
// are replayed through an httptest.Server, which is the same path a live
// response takes.

// The answers the recorded questions got, which are what the assertions below
// are worth asserting: a model that stopped reading its input would not
// produce them.
const (
	recordedImageColour = "orange"
	recordedPDFPhrase   = "Marmalade on the tortoise."
	recordedToolCity    = "Paris"
)

// openAIDialectPrefix marks the recordings made against api.openai.com rather
// than through OpenRouter. OpenAI reports no cost, sends no
// native_finish_reason and carries no reasoning_details, so the assertions
// that are about the router skip them by name.
const openAIDialectPrefix = "live_openai_"

// recordedFixtures lists the recordings matching a glob, by file name.
//
// The request bodies the recorder writes beside each response end in
// .request.json and are excluded: they are the input, not something a client
// can be handed as an answer.
func recordedFixtures(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", pattern))
	if err != nil {
		t.Fatalf("globbing %s: %v", pattern, err)
	}
	var names []string
	for _, path := range paths {
		name := filepath.Base(path)
		if strings.HasSuffix(name, ".request.json") {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatalf("no %s recordings; run scripts/record-fixtures -provider openrouter", pattern)
	}
	return names
}

// collectRecording replays one recorded stream through the client and returns
// what a consumer would have seen.
func collectRecording(t *testing.T, name string) *goodall.Response {
	t.Helper()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, name))
	})
	resp, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if err != nil {
		t.Fatalf("collecting %s: %v", name, err)
	}
	return resp
}

// completeRecording replays one recorded blocking response through the client.
func completeRecording(t *testing.T, name string) *goodall.Response {
	t.Helper()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, name))
	})
	resp, err := c.Complete(t.Context(), simpleRequest())
	if err != nil {
		t.Fatalf("completing %s: %v", name, err)
	}
	return resp
}

// thinkingOf returns the thinking blocks of a message, in order.
func thinkingOf(m goodall.Message) []goodall.Thinking {
	var out []goodall.Thinking
	for _, block := range m.Content {
		if tb, ok := block.(goodall.Thinking); ok {
			out = append(out, tb)
		}
	}
	return out
}

func TestEveryRecordedStreamCollectsWithoutAProtocolError(t *testing.T) {
	for _, name := range recordedFixtures(t, "live_*.sse") {
		t.Run(name, func(t *testing.T) {
			resp := collectRecording(t, name)
			if resp.ID == "" {
				t.Error("the collected response has no id, so no chunk was applied")
			}
			if resp.StopReason == goodall.StopNone {
				t.Error("the collected response has no stop reason, so the finish frame was not applied")
			}
			if resp.Message.Partial {
				t.Error("the collected message is marked partial, so the recording ended early")
			}
			if resp.Usage.Output == 0 {
				t.Error("the collected response reports no output tokens, so the usage frame was not applied")
			}
		})
	}
}

func TestEveryRecordedBlockingResponseDecodes(t *testing.T) {
	for _, name := range recordedFixtures(t, "live_*.json") {
		if name == "live_error.json" {
			// The error envelope is a recording of a failure; it is
			// asserted on where errors are.
			continue
		}
		t.Run(name, func(t *testing.T) {
			resp := completeRecording(t, name)
			if resp.ID == "" {
				t.Error("the blocking response has no id")
			}
			if resp.StopReason == goodall.StopNone {
				t.Error("the blocking response has no stop reason")
			}
		})
	}
}

func TestEveryOpenRouterRecordingCarriesTheCostAndTheUpstreamStopReason(t *testing.T) {
	// These are the two facts only a router reports: what the call cost in
	// money, and the upstream model's own finish string beside OpenRouter's
	// normalised one. A recording that lost either would leave goodall's
	// Cost and NativeStopReason untested against anything real.
	for _, name := range recordedFixtures(t, "live_*.sse") {
		if strings.HasPrefix(name, openAIDialectPrefix) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			resp := collectRecording(t, name)
			if !resp.Cost.Reported {
				t.Error("Cost.Reported is false, though OpenRouter's usage frame carries cost")
			}
			if resp.Cost.Currency != costCurrency {
				t.Errorf("Cost.Currency = %q, want %q", resp.Cost.Currency, costCurrency)
			}
			if resp.NativeStopReason == "" {
				t.Error("NativeStopReason is empty, though OpenRouter reports the upstream model's own finish reason")
			}
		})
	}
}

func TestTheRecordedThinkingTurnCarriesASignedEntryAndAParseableToolCall(t *testing.T) {
	resp := collectRecording(t, "live_thinking_tool_use.sse")
	if resp.StopReason != goodall.StopToolUse {
		t.Errorf("stop reason = %s, want %s", resp.StopReason, goodall.StopToolUse)
	}

	thinking := thinkingOf(resp.Message)
	if len(thinking) == 0 {
		t.Fatal("the recorded thinking turn produced no thinking block")
	}
	var signed bool
	for _, tb := range thinking {
		if tb.Text != "" && tb.Signature != "" {
			signed = true
		}
		if len(tb.Raw) == 0 {
			t.Error("a thinking block carries no raw entry, so it cannot be re-sent verbatim")
		}
	}
	if !signed {
		t.Error("no thinking block carries both text and a signature; a Claude reasoning_details entry carries both")
	}
	if resp.Usage.Reasoning == 0 {
		t.Error("the recorded thinking turn reported no reasoning tokens")
	}

	uses := resp.Message.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("the recorded turn made %d tool calls, want 1", len(uses))
	}
	if uses[0].Name != "get_weather" {
		t.Errorf("tool call name = %q, want get_weather", uses[0].Name)
	}
	var input struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(uses[0].Input, &input); err != nil {
		t.Fatalf("the tool input did not parse as JSON: %v", err)
	}
	if input.City != recordedToolCity {
		t.Errorf("tool input city = %q, want %q", input.City, recordedToolCity)
	}
}

func TestTheRecordedEncryptedReasoningTurnCarriesASummaryAndEncryptedBytes(t *testing.T) {
	// An OpenAI model's reasoning arrives as a summary and as encrypted
	// bytes rather than as signed text, which is the other shape a
	// reasoning_details entry takes and the one that proves goodall
	// re-sends an entry it cannot read.
	//
	// It is the *second* turn that carries it. Recorded 2026-09-15 at high
	// effort: gpt-6-astra through OpenRouter reported reasoning_tokens zero
	// and sent no reasoning_details on the turn that called the tool, and
	// sent them on the turn that answered. The tool-calling turn is
	// asserted on for what it does carry.
	callTurn := collectRecording(t, "live_encrypted_reasoning_tool_use.sse")
	if len(callTurn.Message.ToolUses()) == 0 {
		t.Error("the recorded OpenAI turn made no tool call, so there is no round trip to prove")
	}

	resp := collectRecording(t, "live_encrypted_reasoning_tool_use_turn2.sse")
	var summaries, encrypted int
	for _, block := range resp.Message.Content {
		switch b := block.(type) {
		case goodall.RedactedThinking:
			if len(b.Raw) == 0 {
				t.Error("a redacted thinking block carries no raw entry, so it cannot be re-sent verbatim")
			}
			if b.Data == "" {
				t.Error("a redacted thinking block carries no encrypted data")
			}
			encrypted++
		case goodall.Thinking:
			if len(b.Raw) == 0 {
				t.Error("a thinking block carries no raw entry, so it cannot be re-sent verbatim")
			}
			summaries++
		}
	}
	if summaries == 0 {
		t.Error("the answering turn carried no reasoning summary")
	}
	if encrypted == 0 {
		t.Error("the answering turn carried no encrypted reasoning, which is what goodall must re-send unread")
	}
	if resp.Usage.Reasoning == 0 {
		t.Error("the answering turn reported no reasoning tokens although it produced reasoning")
	}
}

func TestTheRecordedReplaysOfReasoningWereAccepted(t *testing.T) {
	// The request fixture is half the proof: it is the body goodall sent,
	// and it carries an earlier turn's reasoning entries verbatim. The
	// response is the other half — OpenRouter rejects a malformed replay
	// rather than ignoring it, so a turn that answers is proof.
	//
	// The two models put their reasoning on different turns, so the turn
	// that replays differs: Claude thinks before the tool call, and the
	// OpenAI model thinks on the turn that answers.
	turns := []struct {
		name     string
		response string
		request  string
		entries  []string
	}{
		{
			name:     "a Claude signed entry, replayed with the tool result",
			response: "live_thinking_tool_use_turn2.sse",
			request:  "live_thinking_tool_use_turn2.request.json",
			entries:  []string{`"reasoning.text"`, `"signature"`},
		},
		{
			name:     "an OpenAI summary and encrypted entry, replayed on the next question",
			response: "live_encrypted_reasoning_tool_use_turn3.sse",
			request:  "live_encrypted_reasoning_tool_use_turn3.request.json",
			entries:  []string{`"reasoning.summary"`, `"reasoning.encrypted"`, `"openai-responses-v1"`},
		},
	}
	for _, turn := range turns {
		t.Run(turn.name, func(t *testing.T) {
			request := string(fixture(t, turn.request))
			if !strings.Contains(request, `"reasoning_details"`) {
				t.Fatal("the recorded turn replayed no reasoning_details, so it proves nothing about replay")
			}
			for _, entry := range turn.entries {
				if !strings.Contains(request, entry) {
					t.Errorf("the replayed entries do not include %s", entry)
				}
			}
			resp := collectRecording(t, turn.response)
			if resp.StopReason != goodall.StopEndTurn {
				t.Errorf("stop reason = %s, want %s: a turn whose replay was accepted answers",
					resp.StopReason, goodall.StopEndTurn)
			}
			if resp.Message.Text() == "" {
				t.Error("the turn produced no text")
			}
		})
	}
}

func TestTheRecordedMediaAnswersNameWhatWasAsked(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{"the generated PNG's colour", "live_image.sse", recordedImageColour},
		{"the generated PDF's one line", "live_pdf.sse", recordedPDFPhrase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectRecording(t, tt.fixture).Message.Text()
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tt.want)) {
				t.Errorf("the answer was %q, which does not contain %q", got, tt.want)
			}
		})
	}
}

func TestTheRecordedDebugEchoCarriesTheUpstreamBody(t *testing.T) {
	// debug.echo_upstream_body puts the body OpenRouter sent on to
	// Anthropic in a frame of its own. goodall has no place for it in the
	// neutral vocabulary, so it must arrive as an unknown event and leave
	// the rest of the stream intact — a frame that broke collection would
	// make the option unusable.
	raw := string(fixture(t, "live_debug_echo.sse"))
	for _, member := range []string{
		`"echo_upstream_body"`,
		// Members only Anthropic's Messages API has: OpenRouter's own
		// request carries neither, so finding them is what shows the
		// body was translated rather than passed through.
		`"thinking"`,
		`"stop_sequences"`,
	} {
		if !strings.Contains(raw, member) {
			t.Errorf("the debug recording's upstream body carries no %s", member)
		}
	}

	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, "live_debug_echo.sse"))
	})
	events, err := collectEvents(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("streaming the debug recording: %v", err)
	}
	if countEvents(events, goodall.EventUnknown) == 0 {
		t.Error("the debug frame produced no unknown event, so a consumer cannot see what arrived")
	}

	resp := collectRecording(t, "live_debug_echo.sse")
	if resp.Message.Text() == "" {
		t.Error("the debug frame swallowed the answer")
	}
}

func TestTheRecordedErrorDecodesToAnAPIError(t *testing.T) {
	body := fixture(t, "live_error.json")
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(body)
	})

	_, err := c.Complete(t.Context(), simpleRequest())
	if err == nil {
		t.Fatal("a recorded 400 produced no error")
	}
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("the error was %T, want *goodall.APIError", err)
	}
	if apiErr.Kind != goodall.KindInvalidRequest {
		t.Errorf("kind = %s, want %s", apiErr.Kind, goodall.KindInvalidRequest)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", apiErr.Status, http.StatusBadRequest)
	}
	if apiErr.Message == "" {
		t.Error("the recorded envelope carries a message; the error dropped it")
	}
}

func TestTheRecordedBlockingAnswersAgreeWithTheirStreamedSiblings(t *testing.T) {
	// The two recordings of a pair are two separate live calls, so their
	// ids, token counts and wording differ. What must agree is everything
	// the two code paths decide: the stop reason, the model that answered,
	// and the shape of the message.
	pairs := []struct {
		streamed string
		blocking string
	}{
		{"live_text.sse", "live_text_blocking.json"},
		{"live_tool_use.sse", "live_tool_use_blocking.json"},
	}
	for _, pair := range pairs {
		t.Run(pair.blocking, func(t *testing.T) {
			streamed := collectRecording(t, pair.streamed)
			blocking := completeRecording(t, pair.blocking)

			if blocking.StopReason != streamed.StopReason {
				t.Errorf("stop reason = %s blocking, %s streamed; the two paths must agree",
					blocking.StopReason, streamed.StopReason)
			}
			if blocking.Model != streamed.Model {
				t.Errorf("model = %q blocking, %q streamed", blocking.Model, streamed.Model)
			}
			if got, want := blockKinds(blocking.Message), blockKinds(streamed.Message); got != want {
				t.Errorf("block kinds = %q blocking, %q streamed", got, want)
			}
			if blocking.Cost.Reported != streamed.Cost.Reported {
				t.Errorf("Cost.Reported = %v blocking, %v streamed", blocking.Cost.Reported, streamed.Cost.Reported)
			}
		})
	}
}

func TestTheRecordedRequestsShowWhatEachDialectSent(t *testing.T) {
	// The request fixtures are the other half of what a recording proves:
	// that the body goodall built for each dialect is a body the server
	// accepted. OpenRouter takes the reasoning object; api.openai.com takes
	// the top-level reasoning_effort string and nothing else.
	openRouter := string(fixture(t, "live_thinking_tool_use.request.json"))
	if !strings.Contains(openRouter, `"reasoning"`) {
		t.Error("the OpenRouter thinking request sent no reasoning object")
	}
	if strings.Contains(openRouter, `"reasoning_effort"`) {
		t.Error("the OpenRouter thinking request sent reasoning_effort, which belongs to the OpenAI dialect")
	}

	openAI := string(fixture(t, "live_openai_reasoning.request.json"))
	for _, member := range []string{
		// Each of these three was a bug the live recording found:
		// api.openai.com refuses max_tokens on a reasoning model,
		// refuses OpenRouter's reasoning object, and streams no usage
		// at all unless the request asks for it.
		`"max_completion_tokens"`,
		`"reasoning_effort"`,
		`"stream_options"`,
	} {
		if !strings.Contains(openAI, member) {
			t.Errorf("the OpenAI-dialect request sent no %s", member)
		}
	}
	for _, member := range []string{`"max_tokens"`, `"reasoning":`} {
		if strings.Contains(openAI, member) {
			t.Errorf("the OpenAI-dialect request sent %s, which api.openai.com refuses", member)
		}
	}
	if strings.Contains(string(fixture(t, "live_thinking_tool_use.request.json")), `"stream_options"`) {
		t.Error("the OpenRouter request asked for stream_options, which OpenRouter documents as a no-op")
	}
}

// blockKinds renders the block types of a message in order, which is the part
// of two separate recordings of the same question that must match. The Block
// interface is sealed and carries no tag method, so the Go type is the name.
func blockKinds(m goodall.Message) string {
	kinds := make([]string, 0, len(m.Content))
	for _, block := range m.Content {
		kinds = append(kinds, fmt.Sprintf("%T", block))
	}
	return strings.Join(kinds, ",")
}
