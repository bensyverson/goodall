package goodall

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"log/slog"
	"strings"
	"testing"
)

// unsupportedEverything is a model that refuses every input goodall checks
// for, so a test says what it sent rather than what the model allows.
func unsupportedEverything() *ModelInfo {
	return &ModelInfo{
		ID:       "text-only",
		Provider: "testprovider",
		Capabilities: Capabilities{
			ImageInput:   Unsupported,
			PDFInput:     Unsupported,
			Tools:        Unsupported,
			Thinking:     Unsupported,
			CacheControl: Unsupported,
		},
	}
}

// textRequest is a request carrying nothing a capability check could object
// to; each test adds the one thing it is about.
func textRequest() *Request {
	return &Request{
		Model:    "text-only",
		Messages: []Message{UserMessage(Text{Text: "hello"})},
	}
}

func TestCheckCapabilitiesRefusesEachUnsupportedInput(t *testing.T) {
	pdf := Document{Source: BytesSource("application/pdf", []byte("%PDF-1.4"))}
	tool := &stubTool{name: "echo"}

	tests := []struct {
		name string
		want Capability
		fill func(*Request)
	}{
		{"an image block", CapImageInput, func(r *Request) {
			r.Messages = append(r.Messages, UserMessage(Image{Source: BytesSource("image/png", []byte{1})}))
		}},
		{"an image inside a tool result", CapImageInput, func(r *Request) {
			r.Messages = append(r.Messages, UserMessage(ToolResult{
				ToolUseID: "tu_1",
				Content:   Blocks{Image{Source: BytesSource("image/png", []byte{1})}},
			}))
		}},
		{"a PDF document", CapPDFInput, func(r *Request) {
			r.Messages = append(r.Messages, UserMessage(pdf))
		}},
		{"a tool definition", CapTools, func(r *Request) {
			r.Tools = []Tool{tool}
		}},
		{"a thinking effort", CapThinking, func(r *Request) {
			r.Thinking = ThinkingConfig{Effort: EffortLow}
		}},
		{"a manual cache marker", CapCacheControl, func(r *Request) {
			r.Cache = CacheManual
			r.System = []Text{{Text: "be brief", Cache: &CacheControl{}}}
		}},
		{"a manual cache marker on a message block", CapCacheControl, func(r *Request) {
			r.Cache = CacheManual
			r.Messages = append(r.Messages, UserMessage(Text{Text: "again", Cache: &CacheControl{}}))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := textRequest()
			req.ModelInfo = unsupportedEverything()
			tt.fill(req)

			err := checkCapabilities(req)
			if err == nil {
				t.Fatalf("a request carrying %s was allowed through", tt.name)
			}
			if err.Capability != tt.want {
				t.Errorf("capability = %q, want %q", err.Capability, tt.want)
			}
			if err.Model != "text-only" || err.Provider != "testprovider" {
				t.Errorf("error names model %q on provider %q, want text-only on testprovider", err.Model, err.Provider)
			}
		})
	}
}

func TestCheckCapabilitiesLetsUnknownSupportThrough(t *testing.T) {
	// Invariant 11: unknown means try. Nobody has said anything about this
	// model, so nothing about it may refuse a call that would have worked.
	req := textRequest()
	req.ModelInfo = &ModelInfo{ID: "mystery", Provider: "testprovider"}
	req.Tools = []Tool{&stubTool{name: "echo"}}
	req.Thinking = ThinkingConfig{Effort: EffortHigh}
	req.Cache = CacheManual
	req.System = []Text{{Text: "be brief", Cache: &CacheControl{}}}
	req.Messages = append(req.Messages, UserMessage(
		Image{Source: BytesSource("image/png", []byte{1})},
		Document{Source: BytesSource("application/pdf", []byte("%PDF"))},
	))

	if err := checkCapabilities(req); err != nil {
		t.Fatalf("a model nobody has described refused the request: %v", err)
	}
}

func TestCheckCapabilitiesWithNoModelInfoChecksNothing(t *testing.T) {
	req := textRequest()
	req.Messages = append(req.Messages, UserMessage(Image{Source: BytesSource("image/png", []byte{1})}))
	if err := checkCapabilities(req); err != nil {
		t.Fatalf("a request with no catalog entry was refused: %v", err)
	}
}

func TestCheckCapabilitiesIgnoresWhatTheRequestDoesNotCarry(t *testing.T) {
	tests := []struct {
		name string
		fill func(*Request)
	}{
		{"plain text and no tools", func(*Request) {}},
		{"the default thinking effort", func(r *Request) { r.Thinking = ThinkingConfig{Display: DisplaySummarized} }},
		{"thinking turned off", func(r *Request) { r.Thinking = ThinkingConfig{Effort: EffortOff} }},
		{"a non-PDF document", func(r *Request) {
			r.Messages = append(r.Messages, UserMessage(Document{Source: BytesSource("text/plain", []byte("hi"))}))
		}},
		{"cache markers under the automatic policy", func(r *Request) {
			r.Cache = CacheAuto
			r.System = []Text{{Text: "be brief", Cache: &CacheControl{}}}
		}},
		{"an empty tool list", func(r *Request) { r.Tools = []Tool{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := textRequest()
			req.ModelInfo = unsupportedEverything()
			tt.fill(req)
			if err := checkCapabilities(req); err != nil {
				t.Fatalf("a request carrying %s was refused: %v", tt.name, err)
			}
		})
	}
}

func TestCheckCapabilitiesNamesTheFirstFactTheModelRefuses(t *testing.T) {
	// Two refusals in one request report the request-level fact first, so
	// the message is stable rather than depending on message order.
	req := textRequest()
	req.ModelInfo = unsupportedEverything()
	req.Tools = []Tool{&stubTool{name: "echo"}}
	req.Messages = append(req.Messages, UserMessage(Image{Source: BytesSource("image/png", []byte{1})}))

	err := checkCapabilities(req)
	if err == nil {
		t.Fatal("the request was allowed through")
	}
	if err.Capability != CapTools {
		t.Errorf("capability = %q, want %q", err.Capability, CapTools)
	}
	if !strings.Contains(err.Error(), "does not support tools") {
		t.Errorf("error = %q, which does not say what was refused", err)
	}
}

// --- the loop's use of the check ---

func TestRunRefusesAnUnsupportedInputBeforeAnyRequest(t *testing.T) {
	p := &listingProvider{info: unsupportedEverything()}
	agent := &Agent{Provider: p, Model: "text-only"}

	events, err := drain(agent.Run(t.Context(), Conversation{}, Image{Source: BytesSource("image/png", []byte{1})}))
	if err != nil {
		t.Fatalf("the run stream yielded an error: %v", err)
	}
	stopped, ok := events[len(events)-1].(Stopped)
	if !ok {
		t.Fatalf("the run ended with %T, want a Stopped", events[len(events)-1])
	}
	if stopped.Cause != StopCauseError {
		t.Errorf("cause = %s, want %s", stopped.Cause, StopCauseError)
	}
	if stopped.Kind != KindUnsupportedInput {
		t.Errorf("kind = %s, want %s", stopped.Kind, KindUnsupportedInput)
	}
	if !strings.Contains(stopped.Message, string(CapImageInput)) {
		t.Errorf("message = %q, which does not name the missing capability", stopped.Message)
	}
	if p.streams != 0 {
		t.Errorf("the provider was asked to stream %d times; a refused request must never be sent", p.streams)
	}
	// Invariant 10: the turn the run was working on is in the result.
	conv := stopped.Result.Conversation
	if conv.Len() != 1 || conv.At(0).Role != RoleUser {
		t.Errorf("the result carries %d messages, want the user turn that was refused", conv.Len())
	}
}

func TestRunProceedsWhenSupportIsUnknown(t *testing.T) {
	p := &listingProvider{info: &ModelInfo{ID: "mystery", Provider: "testprovider"}}
	agent := &Agent{Provider: p, Model: "mystery"}

	events, err := drain(agent.Run(t.Context(), Conversation{}, Image{Source: BytesSource("image/png", []byte{1})}))
	if err != nil {
		t.Fatalf("the run stream yielded an error: %v", err)
	}
	if _, ok := events[len(events)-1].(Done); !ok {
		t.Fatalf("the run ended with %T, want a Done", events[len(events)-1])
	}
	if p.streams != 1 {
		t.Errorf("the provider streamed %d times, want 1", p.streams)
	}
}

func TestRunFetchesTheCatalogOnceAndPutsItOnEveryRequest(t *testing.T) {
	p := &listingProvider{info: &ModelInfo{ID: "mystery", Provider: "testprovider"}, turns: 2}
	agent := &Agent{Provider: p, Model: "mystery", Tools: []Tool{&stubTool{name: "echo"}}}

	if _, err := drain(agent.Run(t.Context(), Conversation{}, Text{Text: "hi"})); err != nil {
		t.Fatalf("the run stream yielded an error: %v", err)
	}
	if p.lookups != 1 {
		t.Errorf("the catalog was read %d times, want once for the whole run", p.lookups)
	}
	if p.streams != 2 {
		t.Fatalf("the provider streamed %d times, want 2", p.streams)
	}
	for i, req := range p.seen {
		if req.ModelInfo != p.info {
			t.Errorf("request %d carries ModelInfo %v, want the catalog entry the run fetched", i, req.ModelInfo)
		}
	}
}

func TestRunProceedsWhenTheCatalogCannotBeRead(t *testing.T) {
	// A catalog can lag the API, so a model it has never heard of is a
	// warning and a run with no capability facts, not a failed run.
	var logged bytes.Buffer
	p := &listingProvider{err: &APIError{Provider: "testprovider", Kind: KindNotFound, Message: "no such model"}}
	agent := &Agent{
		Provider: p,
		Model:    "brand-new",
		Logger:   slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	events, err := drain(agent.Run(t.Context(), Conversation{}, Image{Source: BytesSource("image/png", []byte{1})}))
	if err != nil {
		t.Fatalf("the run stream yielded an error: %v", err)
	}
	if _, ok := events[len(events)-1].(Done); !ok {
		t.Fatalf("the run ended with %T, want a Done", events[len(events)-1])
	}
	if p.streams != 1 {
		t.Errorf("the provider streamed %d times, want 1", p.streams)
	}
	if req := p.seen[0]; req.ModelInfo != nil {
		t.Errorf("the request carries ModelInfo %v, want none after a failed lookup", req.ModelInfo)
	}
	if !strings.Contains(logged.String(), "WARN") || !strings.Contains(logged.String(), "no such model") {
		t.Errorf("the failed lookup logged %q, want a warning naming the provider's error", logged.String())
	}
}

func TestRunWithoutAModelListerSkipsTheCheck(t *testing.T) {
	// A provider that publishes no catalog gets no pre-flight at all,
	// rather than a refusal it never asked for.
	p := &textProvider{}
	agent := &Agent{Provider: p, Model: "mystery"}

	events, err := drain(agent.Run(t.Context(), Conversation{}, Image{Source: BytesSource("image/png", []byte{1})}))
	if err != nil {
		t.Fatalf("the run stream yielded an error: %v", err)
	}
	if _, ok := events[len(events)-1].(Done); !ok {
		t.Fatalf("the run ended with %T, want a Done", events[len(events)-1])
	}
	if p.seen[0].ModelInfo != nil {
		t.Error("the request carries a ModelInfo though the provider lists no models")
	}
}

// --- the providers these tests run against ---

// drain reads a stream to its end, returning the events and the first error.
// It returns rather than failing so the caller reports: a t.Fatal inside the
// body of a range-over-func unwinds the iterator from the wrong place.
func drain(s Stream) ([]Event, error) {
	var out []Event
	for ev, err := range s {
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// textProvider answers every turn with one line of text and records the
// requests it was handed. It lists no models, so a run against it has no
// capability facts to work from.
type textProvider struct {
	streams int
	seen    []*Request
	// turns is how many turns end in a tool call before the last one
	// answers; zero answers immediately.
	turns int
}

func (p *textProvider) Stream(ctx context.Context, req *Request) Stream {
	p.streams++
	p.seen = append(p.seen, req)
	callTool := p.streams < p.turns
	return func(yield func(Event, error) bool) {
		events := []Event{
			MessageStart{ID: "msg_stub", Model: req.Model},
			BlockStart{Index: 0, Block: Text{}},
			TextDelta{Index: 0, Text: "here you are"},
			BlockStop{Index: 0},
		}
		if callTool {
			events = append(events,
				BlockStart{Index: 1, Block: ToolUse{ID: "tu_1", Name: "echo"}},
				ToolInputDelta{Index: 1, PartialJSON: `{"text":"hi"}`},
				BlockStop{Index: 1},
				MessageDelta{StopReason: StopToolUse},
			)
		} else {
			events = append(events, MessageDelta{StopReason: StopEndTurn})
		}
		events = append(events, MessageStop{})
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// listingProvider is a textProvider that also publishes a catalog, which is
// what turns the pre-flight on.
type listingProvider struct {
	textProvider
	info    *ModelInfo
	err     error
	lookups int
}

func (p *listingProvider) Model(ctx context.Context, id string) (*ModelInfo, error) {
	p.lookups++
	if p.err != nil {
		return nil, p.err
	}
	return p.info, nil
}

// stubTool is a tool definition with a trivial schema, for the cases where
// only the presence of a tool matters.
type stubTool struct{ name string }

func (t *stubTool) Name() string        { return t.name }
func (t *stubTool) Description() string { return "a tool" }
func (t *stubTool) Schema() *Schema {
	return &Schema{Type: SchemaObject, Properties: Properties{{
		Name:   "text",
		Schema: Schema{Type: SchemaString},
	}}}
}

func (t *stubTool) Execute(ctx context.Context, input jsontext.Value) (ToolResult, error) {
	return TextResult("ok"), nil
}
