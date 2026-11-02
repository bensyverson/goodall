package openrouter_test

import (
	"context"
	json "encoding/json/v2"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
)

// These are the live tests: they call the real OpenRouter API, and one calls
// api.openai.com, with the keys in the repository's .env. They cost real
// money, so there are eight paid calls in the whole file. They skip, saying what
// they skipped and why, when the key is absent and under -short, which is what
// makes `go test -short ./...` the offline suite.
//
// What they are for is the half of the provider no recording can prove: that
// the bytes goodall sends are bytes these servers accept. The fixtures under
// testdata prove the decoding; only a live call proves the encoding.

// The .env entries holding the keys, named in every skip message so a reader
// knows what to add.
const (
	liveKeyName   = "OPENROUTER_API_KEY"
	openAIKeyName = "OPENAI_API_KEY"
)

// liveMaxTokens is the output cap on every live call here. It is well above
// what any of these questions needs and small enough that a runaway answer is
// cheap.
const liveMaxTokens = 2048

// liveCity is the city the live tool call asks about.
const liveCity = "Paris"

// liveKey reads one key out of the repository's .env, or skips the test saying
// which key in which file was missing. The value is never logged.
func liveKey(t *testing.T, name string) string {
	t.Helper()
	if testing.Short() {
		t.Skipf("skipping the live %s calls: -short is set, so nothing was sent to the API", name)
	}
	path, err := dotenv.RepoEnvFile()
	if err != nil {
		t.Skipf("skipping the live %s calls: %v", name, err)
	}
	key, ok := dotenv.Lookup(path, name)
	if !ok {
		t.Skipf("skipping the live %s calls: no %s in %s, so no call was made", name, name, path)
	}
	return key
}

// liveClient builds a client pointed at OpenRouter.
func liveClient(t *testing.T) *openrouter.Client {
	t.Helper()
	return openrouter.New(openrouter.WithAPIKey(liveKey(t, liveKeyName)))
}

// liveWeatherTool is the tool the live round trip offers. Its handler is never
// run: the test plays the loop's part and sends back a canned result.
func liveWeatherTool(t *testing.T) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("get_weather", "Look up the current weather in a city.",
		func(ctx context.Context, in struct {
			City string `json:"city" desc:"the city to look up, such as Paris"`
		},
		) (goodall.ToolResult, error) {
			return goodall.TextResult(""), nil
		})
	if err != nil {
		t.Fatalf("defining the tool: %v", err)
	}
	return tool
}

func TestLiveThinkingToolCallRoundTrip(t *testing.T) {
	client := liveClient(t)
	tool := liveWeatherTool(t)

	// The tool choice is left at auto and the question is made to need
	// arithmetic on purpose: reasoning is the model's to spend, and a
	// trivial question — or a forced tool_choice — produces a turn with no
	// reasoning at all (observed 2026-09-15 while recording the fixtures).
	// A failure here is most likely that this prompt no longer requires
	// reasoning.
	first := &goodall.Request{
		Model:     livemodel.OpenRouterClaude,
		MaxTokens: liveMaxTokens,
		// High, and a ride with real arithmetic in it: on 2026-10-28
		// claude-sonnet-5-5, whose effort levels are recalibrated from
		// Sonnet 5's, thought before the tool call on the old 40 km
		// question in 0 of 1 runs at low and medium and 4 of 5 at high;
		// on this one it thought in 15 of 15 at high.
		Thinking: goodall.ThinkingConfig{Effort: goodall.EffortHigh, Display: goodall.DisplaySummarized},
		Tools:    []goodall.Tool{tool},
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "A cyclist in " + liveCity + " will ride 137 km at 23.4 km/h with two 18-minute stops, " +
				"starting at 13:47. Work out to the minute when they finish, look up the weather there, " +
				"and tell me whether they should expect rain during the ride."},
		)},
	}

	resp, err := client.Stream(t.Context(), first).Collect()
	if err != nil {
		t.Fatalf("the first live turn failed: %v", err)
	}
	t.Logf("turn 1: stop=%s native=%s input=%d output=%d reasoning=%d cost=%s",
		resp.StopReason, resp.NativeStopReason, resp.Usage.Input, resp.Usage.Output,
		resp.Usage.Reasoning, resp.Cost.Amount)

	if resp.StopReason != goodall.StopToolUse {
		t.Fatalf("stop reason = %s, want %s; the model answered without calling the tool",
			resp.StopReason, goodall.StopToolUse)
	}
	if !resp.Cost.Reported {
		t.Error("OpenRouter reported no cost, though its usage frame carries one")
	}

	var signed goodall.Thinking
	for _, block := range resp.Message.Content {
		if tb, ok := block.(goodall.Thinking); ok && tb.Signature != "" {
			signed = tb
		}
	}
	if signed.Signature == "" {
		t.Fatal("the live turn produced no signed reasoning entry, so there is nothing to replay")
	}
	if len(signed.Raw) == 0 {
		t.Error("the reasoning entry carries no raw bytes, so it cannot be re-sent verbatim")
	}

	uses := resp.Message.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("the live turn made %d tool calls, want 1", len(uses))
	}
	var input struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(uses[0].Input, &input); err != nil {
		t.Fatalf("the live tool input did not parse: %v", err)
	}
	if input.City != liveCity {
		t.Errorf("tool input city = %q, want %q", input.City, liveCity)
	}

	// The second turn replays the assistant message exactly as it arrived,
	// reasoning entries and signature included, which is what the loop
	// does. A replay the upstream model rejects comes back as an error.
	result := goodall.TextResult(`{"city":"` + liveCity + `","temperature_c":18,"conditions":"light rain"}`)
	result.ToolUseID = uses[0].ID
	second := &goodall.Request{
		Model:     first.Model,
		MaxTokens: liveMaxTokens,
		Thinking:  first.Thinking,
		Tools:     first.Tools,
		Messages:  append(append([]goodall.Message(nil), first.Messages...), resp.Message, goodall.UserMessage(result)),
	}

	answer, err := client.Stream(t.Context(), second).Collect()
	if err != nil {
		t.Fatalf("the second live turn failed, so the replayed reasoning was not accepted: %v", err)
	}
	t.Logf("turn 2: stop=%s input=%d output=%d reasoning=%d cost=%s",
		answer.StopReason, answer.Usage.Input, answer.Usage.Output, answer.Usage.Reasoning, answer.Cost.Amount)

	if answer.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %s, want %s", answer.StopReason, goodall.StopEndTurn)
	}
	if answer.Message.Text() == "" {
		t.Error("the second live turn produced no text")
	}
}

func TestLiveModelReportsImageAndPDFSupportAndAPrice(t *testing.T) {
	client := liveClient(t)

	info, err := client.Model(t.Context(), livemodel.OpenRouterClaude)
	if err != nil {
		t.Fatalf("the live catalog lookup failed: %v", err)
	}
	t.Logf("%s: image=%s pdf=%s tools=%s thinking=%s efforts=%v context=%d max_output=%d",
		info.ID, info.Capabilities.ImageInput, info.Capabilities.PDFInput, info.Capabilities.Tools,
		info.Capabilities.Thinking, info.Capabilities.ThinkingEfforts,
		info.Capabilities.ContextWindow, info.Capabilities.MaxOutput)

	if info.ID != livemodel.OpenRouterClaude {
		t.Errorf("the catalog answered for %q, want %q", info.ID, livemodel.OpenRouterClaude)
	}
	if info.Capabilities.ImageInput != goodall.Supported {
		t.Errorf("image input = %s, want %s", info.Capabilities.ImageInput, goodall.Supported)
	}
	if info.Capabilities.PDFInput != goodall.Supported {
		t.Errorf("PDF input = %s, want %s: OpenRouter spells PDF support as the \"file\" input modality",
			info.Capabilities.PDFInput, goodall.Supported)
	}
	if info.Capabilities.Tools != goodall.Supported {
		t.Errorf("tools = %s, want %s", info.Capabilities.Tools, goodall.Supported)
	}

	// The prices are decimal strings on the wire and must survive as exact
	// decimals: a price of "0.000005" rounded through a float64 could not be
	// printed back.
	if info.Pricing == nil {
		t.Fatal("the catalog entry carried no pricing")
	}
	t.Logf("pricing: input=%s output=%s cache_read=%s %s",
		info.Pricing.Input, info.Pricing.Output, info.Pricing.CacheRead, info.Pricing.Currency)
	if info.Pricing.Input.IsZero() {
		t.Error("the input price is zero; this model is not free")
	}
	if info.Pricing.Output.IsZero() {
		t.Error("the output price is zero; this model is not free")
	}
	if info.Pricing.Currency == "" {
		t.Error("the pricing names no currency")
	}
}

func TestLiveCompleteAnswersWithoutStreaming(t *testing.T) {
	client := liveClient(t)

	resp, err := client.Complete(t.Context(), &goodall.Request{
		Model:     livemodel.OpenRouterClaude,
		MaxTokens: liveMaxTokens,
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "Answer in one short sentence: what is the capital of France?"},
		)},
	})
	if err != nil {
		t.Fatalf("the live blocking call failed: %v", err)
	}
	t.Logf("complete: stop=%s native=%s input=%d output=%d cost=%s",
		resp.StopReason, resp.NativeStopReason, resp.Usage.Input, resp.Usage.Output, resp.Cost.Amount)

	if resp.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %s, want %s", resp.StopReason, goodall.StopEndTurn)
	}
	if resp.Message.Text() == "" {
		t.Error("the live blocking call returned no text")
	}
	if resp.Usage.Output == 0 {
		t.Error("the live blocking call reported no output tokens")
	}
	if !resp.Cost.Reported {
		t.Error("the live blocking call reported no cost")
	}
}

// TestLiveOpenAIDialectRoundTrip takes one thinking tool call through two turns on
// /v1/responses. goodall sends no include member, on the evidence that the
// ciphertext arrives anyway, so the assertion on encrypted_content is what makes a
// change to that loud. The server would accept a second turn with the reasoning
// lost, so the second turn only shows the request is well formed; the replay's bytes
// are held by the offline tests.
func TestLiveOpenAIDialectRoundTrip(t *testing.T) {
	client := openrouter.New(
		openrouter.WithAPIKey(liveKey(t, openAIKeyName)),
		openrouter.WithBaseURL("https://api.openai.com/v1"),
		openrouter.WithDialect(openrouter.OpenAI),
	)
	weather, err := goodall.NewTool("get_weather", "Look up the current weather in a city.",
		func(_ context.Context, _ struct {
			City string `json:"city"`
		}) (goodall.ToolResult, error) {
			return goodall.TextResult(""), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	// High, and arithmetic first: at low effort a plain question can draw no reasoning
	// item at all, which would leave nothing here to assert on.
	first := &goodall.Request{
		Model:     livemodel.OpenAI,
		MaxTokens: liveMaxTokens,
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortHigh, Display: goodall.DisplaySummarized},
		Tools:     []goodall.Tool{weather},
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "A cyclist in Paris will ride 137 km at 23.4 km/h " +
			"with two 18-minute stops, starting at 13:47. Work out to the minute when they finish, look up the weather there, " +
			"and tell me whether they should expect rain during the ride."})},
	}
	resp, err := client.Stream(t.Context(), first).Collect()
	if err != nil {
		t.Fatalf("the live OpenAI-dialect call failed: %v", err)
	}
	t.Logf("openai turn 1: model=%s stop=%s input=%d output=%d reasoning=%d",
		livemodel.OpenAI, resp.StopReason, resp.Usage.Input, resp.Usage.Output, resp.Usage.Reasoning)

	if resp.Cost.Reported {
		t.Error("a cost was reported, though api.openai.com sends no figure")
	}
	if resp.Usage.Output == 0 {
		t.Error("the call reported no output tokens")
	}
	var encrypted int
	for _, blk := range resp.Message.Content {
		thinking, ok := blk.(goodall.Thinking)
		if !ok {
			continue
		}
		var item struct {
			Encrypted string `json:"encrypted_content"`
		}
		if err := json.Unmarshal(thinking.Raw, &item); err != nil {
			t.Fatalf("a reasoning item did not decode: %v", err)
		}
		encrypted += len(item.Encrypted)
	}
	t.Logf("openai turn 1: encrypted_content %d characters", encrypted)
	if encrypted == 0 {
		t.Fatal("no reasoning item carried encrypted_content, so nothing could be replayed")
	}
	uses := resp.Message.ToolUses()
	if len(uses) == 0 {
		t.Fatalf("the model answered without the tool: %q", resp.Message.Text())
	}

	var results goodall.Blocks
	for _, use := range uses {
		result := goodall.TextResult(`{"conditions":"light rain"}`)
		result.ToolUseID = use.ID
		results = append(results, result)
	}
	second := *first
	second.Messages = append(append([]goodall.Message{}, first.Messages...), resp.Message, goodall.UserMessage(results...))
	answer, err := client.Stream(t.Context(), &second).Collect()
	if err != nil {
		t.Fatalf("the replaying turn was refused: %v", err)
	}
	t.Logf("openai turn 2: stop=%s input=%d output=%d reasoning=%d",
		answer.StopReason, answer.Usage.Input, answer.Usage.Output, answer.Usage.Reasoning)
	if answer.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %s, want %s", answer.StopReason, goodall.StopEndTurn)
	}
	if answer.Message.Text() == "" {
		t.Error("the replaying turn returned no text")
	}
}

// TestLiveThinkingOffIsAccepted asks each 5.5 Claude for no reasoning the way
// the agent does, with the catalog entry attached. On 2026-10-28 OpenRouter
// answered effort "none" on Opus 5.5 and Sonnet 5.5 with a 400 ("Reasoning is
// mandatory for this endpoint and cannot be disabled").
func TestLiveThinkingOffIsAccepted(t *testing.T) {
	client := liveClient(t)
	for _, model := range []string{"anthropic/claude-opus-5.5", "anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5"} {
		t.Run(model, func(t *testing.T) {
			info, err := client.Model(t.Context(), model)
			if err != nil {
				t.Fatalf("the catalog lookup failed: %v", err)
			}
			resp, err := client.Complete(t.Context(), &goodall.Request{
				Model:     model,
				MaxTokens: liveMaxTokens,
				Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Reply with the single word: ok"})},
				Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortOff},
				ModelInfo: info,
			})
			if err != nil {
				t.Fatalf("thinking off (%s) was refused: %v", info.Capabilities.ThinkingOff, err)
			}
			t.Logf("%s: off=%s stop=%s output=%d", model, info.Capabilities.ThinkingOff, resp.StopReason, resp.Usage.Output)
			if resp.Message.Text() == "" {
				t.Error("the call returned no text")
			}
		})
	}
}
