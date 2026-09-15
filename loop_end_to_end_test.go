package goodall_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/openrouter"
)

// These are the loop's end-to-end tests: a whole Agent.Run, over a real
// provider client, against the bytes a provider actually sent — Anthropic's
// recordings in anthropic/testdata and OpenRouter's fixtures in
// openrouter/testdata, replayed through an httptest server. Everything
// between the agent and the socket is the shipping code: translation, the SSE
// decoder, the accumulator, the tool loop, the catalog lookup and the
// pre-flight.
//
// They run offline and make no network call.

// providerFixture reads a file out of a provider package's testdata. The root
// package's tests run in the module root, so the provider directory is part of
// the path.
func providerFixture(t *testing.T, provider, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(provider, "testdata", name))
	if err != nil {
		t.Fatalf("reading %s's fixture %s: %v", provider, name, err)
	}
	return data
}

// replay is a provider's API, played back from recordings: the catalog on
// one route and one scripted answer per completion call on the other. It
// records what it was sent, so a test can assert on the bytes the loop
// produced as well as on the result it returned.
type replay struct {
	mu        sync.Mutex
	catalog   []byte // the models body, served on every catalog call
	answers   [][]byte
	mediaType string // the content type the answers are served as

	lookups int
	bodies  []string
}

// models serves the catalog.
func (s *replay) models(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.lookups++
	body := s.catalog
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// complete serves the next scripted answer and records the request body.
func (s *replay) complete(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	s.mu.Lock()
	turn := len(s.bodies)
	s.bodies = append(s.bodies, string(body))
	var answer []byte
	if turn < len(s.answers) {
		answer = s.answers[turn]
	}
	s.mu.Unlock()
	if answer == nil {
		// The script ran out, which means the loop sent a turn the test
		// did not expect; say so rather than serving an empty stream.
		http.Error(w, "no recording for turn "+strconv.Itoa(turn+1), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", s.mediaType)
	w.Write(answer)
}

// turns is how many completion calls the loop made, and lookupCount how many
// catalog reads it cost.
func (s *replay) turns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func (s *replay) lookupCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookups
}

// body is the request body of one turn, counted from zero.
func (s *replay) body(t *testing.T, turn int) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn >= len(s.bodies) {
		t.Fatalf("the loop sent %d turns, so there is no turn %d to read", len(s.bodies), turn)
	}
	return s.bodies[turn]
}

// weatherTool answers the question the recorded turns asked, so the second
// recorded turn is answering a tool result rather than a placeholder.
func weatherTool(t *testing.T) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("get_weather", "Look up the current weather in a city.", func(ctx context.Context, in struct {
		City string `json:"city" desc:"the city to look up, such as Paris"`
		Unit string `json:"unit,omitzero" desc:"celsius or fahrenheit"`
	}) (goodall.ToolResult, error) {
		return goodall.TextResult("18 degrees, light rain after 15:30 in " + in.City), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestTheLoopRunsTwoRecordedAnthropicTurns(t *testing.T) {
	server := &replay{
		catalog:   providerFixture(t, "anthropic", "model.json"),
		mediaType: "text/event-stream",
		answers: [][]byte{
			providerFixture(t, "anthropic", "live_thinking_tool_use.sse"),
			providerFixture(t, "anthropic", "live_thinking_tool_use_turn2.sse"),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models/{id}", server.models)
	mux.HandleFunc("POST /v1/messages", server.complete)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	agent := &goodall.Agent{
		Provider:  anthropic.New("sk-ant-test", anthropic.WithBaseURL(srv.URL)),
		Model:     "claude-sonnet-5",
		System:    "You are a careful assistant.",
		Tools:     []goodall.Tool{weatherTool(t)},
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
		MaxTokens: 2048,
	}

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "Will the ride be rained on?"})
	result := terminalDone(t, events).Result

	if server.turns() != 2 {
		t.Fatalf("the loop sent %d turns, want 2", server.turns())
	}
	if server.lookupCount() != 1 {
		t.Errorf("the catalog was read %d times over two turns, want once", server.lookupCount())
	}
	if result.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %q, want end_turn", result.StopReason)
	}
	if result.Conversation.Len() != 4 {
		t.Fatalf("the conversation has %d messages, want user, assistant, tool results, assistant", result.Conversation.Len())
	}
	if text := result.Conversation.At(3).Text(); !strings.Contains(text, "rain") {
		t.Errorf("the final answer is %q, which does not answer the question that was recorded", text)
	}

	// The two recorded message_deltas, summed: a Result reports the whole
	// run, not the last turn.
	wantUsage := goodall.Usage{Input: 570 + 728, Output: 124 + 144, Reasoning: 50}
	if result.Usage != wantUsage {
		t.Errorf("summed usage = %+v, want %+v", result.Usage, wantUsage)
	}
	// Anthropic reports tokens and no money, so the run's cost is "not
	// known" rather than a figure of zero.
	if result.Cost.Reported {
		t.Errorf("cost = %+v, want Reported false: Anthropic publishes no price on a response", result.Cost)
	}
	if !result.Cost.Amount.IsZero() {
		t.Errorf("cost amount = %s, want zero", result.Cost.Amount)
	}

	// The thinking block came back signed and went out again on the next
	// turn, beside the tool result: that is the replay the recording was
	// made to prove.
	second := server.body(t, 1)
	for _, want := range []string{`"type":"thinking"`, `"signature"`, `"type":"tool_result"`, "light rain after 15:30"} {
		if !strings.Contains(second, want) {
			t.Errorf("the second turn's body does not carry %s:\n%s", want, second)
		}
	}
	// The cache policy is the agent's, applied by the translation on every
	// turn: CacheAuto is the zero value, so this request was never
	// configured for caching and is cached anyway.
	for turn := range 2 {
		if !strings.Contains(server.body(t, turn), `"cache_control"`) {
			t.Errorf("turn %d carries no cache_control, though the agent's policy is %s", turn, goodall.CacheAuto)
		}
	}
}

func TestTheCatalogTheLoopFetchedShapesTheRequest(t *testing.T) {
	// claude-haiku-4-5 reads as a budget-thinking model by name, and the
	// catalog this server publishes says the model takes adaptive
	// thinking. The body proves which one the translation believed, and so
	// proves the loop's lookup reached it.
	server := &replay{
		catalog:   providerFixture(t, "anthropic", "model.json"),
		mediaType: "text/event-stream",
		answers:   [][]byte{providerFixture(t, "anthropic", "live_text.sse")},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models/{id}", server.models)
	mux.HandleFunc("POST /v1/messages", server.complete)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	agent := &goodall.Agent{
		Provider:  anthropic.New("sk-ant-test", anthropic.WithBaseURL(srv.URL)),
		Model:     "claude-haiku-4-5",
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
		MaxTokens: 2048,
	}
	terminalDone(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "hello"}))

	body := server.body(t, 0)
	if want := `"thinking":{"type":"adaptive","display":"summarized"}`; !strings.Contains(body, want) {
		t.Errorf("the body does not carry %s, so the catalog did not reach the translation:\n%s", want, body)
	}
	if strings.Contains(body, "budget_tokens") {
		t.Errorf("the body derived a thinking budget, though the catalog says the model takes adaptive thinking:\n%s", body)
	}
}

// openRouterSecondTurn is the answer the fixture's tool calls lead to. It is
// authored rather than recorded: openrouter/testdata carries the tool-calling
// turn, and the reply that follows two tool results has no fixture of its own.
// The shape is the fixture's, one chunk of content and a final chunk carrying
// finish_reason and the usage.
const openRouterSecondTurn = `data: {"id":"gen-fixture-2","object":"chat.completion.chunk","created":1789473700,"model":"anthropic/claude-haiku-4.5","provider":"Amazon Bedrock","choices":[{"index":0,"delta":{"content":"Both cities are mild.","role":"assistant"},"finish_reason":null,"native_finish_reason":null}]}

data: {"id":"gen-fixture-2","object":"chat.completion.chunk","created":1789473700,"model":"anthropic/claude-haiku-4.5","provider":"Amazon Bedrock","choices":[{"index":0,"delta":{"content":"","role":"assistant"},"finish_reason":"stop","native_finish_reason":"end_turn"}],"usage":{"prompt_tokens":812,"completion_tokens":21,"total_tokens":833,"cost":0.00031,"is_byok":false}}

data: [DONE]

`

func TestTheLoopRunsTwoOpenRouterTurnsAndReportsTheCost(t *testing.T) {
	server := &replay{
		catalog:   providerFixture(t, "openrouter", "models.json"),
		mediaType: "text/event-stream",
		answers: [][]byte{
			providerFixture(t, "openrouter", "stream-reasoning-tools.txt"),
			[]byte(openRouterSecondTurn),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /models", server.models)
	mux.HandleFunc("POST /chat/completions", server.complete)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	agent := &goodall.Agent{
		Provider: openrouter.New(openrouter.WithAPIKey("test-key"), openrouter.WithBaseURL(srv.URL)),
		Model:    "anthropic/claude-haiku-4.5",
		Tools:    []goodall.Tool{weatherTool(t)},
	}

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "Paris and Tokyo?"})
	result := terminalDone(t, events).Result

	if server.turns() != 2 {
		t.Fatalf("the loop sent %d turns, want 2", server.turns())
	}
	if server.lookupCount() != 1 {
		t.Errorf("the catalog was read %d times over two turns, want once", server.lookupCount())
	}
	// The fixture calls the tool twice in one turn, so the results come
	// back as one message carrying both.
	if result.Conversation.Len() != 4 {
		t.Fatalf("the conversation has %d messages, want 4", result.Conversation.Len())
	}
	if got := len(toolResults(result.Conversation.At(2))); got != 2 {
		t.Errorf("the tool turn carries %d results, want one for each of the recorded calls", got)
	}

	// The recorded usage frame, plus the second turn's: cached tokens are
	// reported separately and taken out of Input.
	wantUsage := goodall.Usage{Input: 580 + 812, Output: 167 + 21, CacheRead: 24, Reasoning: 75}
	if result.Usage != wantUsage {
		t.Errorf("summed usage = %+v, want %+v", result.Usage, wantUsage)
	}
	// OpenRouter charges and says what it charged, so the run's cost is a
	// reported figure: the two turns' costs, added exactly.
	if !result.Cost.Reported {
		t.Error("cost is not Reported, though both turns carried one")
	}
	if got, want := result.Cost.Amount.String(), "0.00173461"; got != want {
		t.Errorf("cost amount = %s, want %s", got, want)
	}
	if result.Cost.Currency != "USD" {
		t.Errorf("cost currency = %q, want USD", result.Cost.Currency)
	}
}

func TestTheLoopRefusesAnImageTheOpenRouterCatalogRejects(t *testing.T) {
	// ibm-granite/granite-4.2-8b publishes "text" as its only input
	// modality, which is a refusal rather than a silence — so the image
	// never leaves the process.
	server := &replay{catalog: providerFixture(t, "openrouter", "models.json"), mediaType: "text/event-stream"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /models", server.models)
	mux.HandleFunc("POST /chat/completions", server.complete)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	agent := &goodall.Agent{
		Provider: openrouter.New(openrouter.WithAPIKey("test-key"), openrouter.WithBaseURL(srv.URL)),
		Model:    "ibm-granite/granite-4.2-8b",
	}

	events := runEvents(t, agent, goodall.Conversation{},
		goodall.Text{Text: "What color is this?"},
		goodall.Image{Source: goodall.BytesSource("image/png", []byte{0x89, 'P', 'N', 'G'})},
	)
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseError {
		t.Errorf("cause = %s, want %s", stopped.Cause, goodall.StopCauseError)
	}
	if stopped.Kind != goodall.KindUnsupportedInput {
		t.Errorf("kind = %s, want %s", stopped.Kind, goodall.KindUnsupportedInput)
	}
	if !strings.Contains(stopped.Message, "image_input") || !strings.Contains(stopped.Message, "granite") {
		t.Errorf("message = %q, want it to name the model and the capability", stopped.Message)
	}
	if server.turns() != 0 {
		t.Errorf("the loop sent %d completion requests; a refused input must never be sent", server.turns())
	}
	// Invariant 10: the turn the run was working on comes back with the
	// refusal, so the caller can fix it and send again.
	if result := stopped.Result; result.Conversation.Len() != 1 {
		t.Errorf("the result carries %d messages, want the user turn that was refused", result.Conversation.Len())
	}
}
