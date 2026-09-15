package anthropic_test

import (
	"context"
	json "encoding/json/v2"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
)

// These are the live tests: they call the real Anthropic API with the key in
// the repository's .env and cost real money, so there are four calls in the
// whole file. They skip, saying what they skipped and why, when the key is
// absent and under -short, which is what makes `go test -short ./...` the
// offline suite.
//
// What they are for is the half of the provider no recording can prove: that
// the bytes goodall sends are bytes Anthropic accepts. The fixtures under
// testdata prove the decoding; only a live call proves the encoding.

// liveKeyName is the .env entry holding the key, named in every skip message
// so a reader knows what to add.
const liveKeyName = "ANTHROPIC_API_KEY"

// liveMaxTokens is the output cap on every live call here. It is above the
// 1024-token minimum a thinking budget needs and small enough that a runaway
// answer is cheap.
const liveMaxTokens = 2048

// liveCity is the city the live tool call asks about.
const liveCity = "Paris"

// liveClient builds a client from the repository's .env, or skips the test
// saying which key in which file was missing.
func liveClient(t *testing.T) *anthropic.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the live Anthropic calls: -short is set, so nothing was sent to the API")
	}
	path, err := dotenv.RepoEnvFile()
	if err != nil {
		t.Skipf("skipping the live Anthropic calls: %v", err)
	}
	key, ok := dotenv.Lookup(path, liveKeyName)
	if !ok {
		t.Skipf("skipping the live Anthropic calls: no %s in %s, so no call was made", liveKeyName, path)
	}
	return anthropic.New(key)
}

// liveWeatherTool is the tool the live round trip offers. Its handler is
// never run: the test plays the loop's part and sends back a canned result.
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
	// arithmetic on purpose: adaptive thinking is the model's to spend,
	// and a trivial question — or a forced tool_choice — produces a turn
	// with no thinking block at all (observed 2026-09-15 while recording
	// the fixtures). A failure here is most likely that this prompt no
	// longer requires reasoning.
	first := &goodall.Request{
		Model:     livemodel.Anthropic,
		MaxTokens: liveMaxTokens,
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
		Tools:     []goodall.Tool{tool},
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "A cyclist in " + liveCity + " will ride 40 km at 24 km/h, " +
				"starting at 14:00. Work out when they finish, look up the weather there, " +
				"and tell me whether they should expect rain during the ride."},
		)},
	}

	resp, err := client.Stream(t.Context(), first).Collect()
	if err != nil {
		t.Fatalf("the first live turn failed: %v", err)
	}
	t.Logf("turn 1: stop=%s input=%d output=%d reasoning=%d",
		resp.StopReason, resp.Usage.Input, resp.Usage.Output, resp.Usage.Reasoning)

	if resp.StopReason != goodall.StopToolUse {
		t.Fatalf("stop reason = %s, want %s; the model answered without calling the tool",
			resp.StopReason, goodall.StopToolUse)
	}
	var signed goodall.Thinking
	for _, block := range resp.Message.Content {
		if tb, ok := block.(goodall.Thinking); ok && tb.Signature != "" {
			signed = tb
		}
	}
	if signed.Signature == "" {
		t.Fatal("the live turn produced no signed thinking block, so there is nothing to replay")
	}
	if resp.Usage.Reasoning == 0 {
		t.Error("the live turn reported no thinking tokens although it produced a thinking block")
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

	// The second turn replays the assistant message exactly as it
	// arrived, thinking block and signature included, which is what the
	// loop does. A replay Anthropic rejects comes back as an error.
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
		t.Fatalf("the second live turn failed, so the replayed thinking block was not accepted: %v", err)
	}
	t.Logf("turn 2: stop=%s input=%d output=%d reasoning=%d",
		answer.StopReason, answer.Usage.Input, answer.Usage.Output, answer.Usage.Reasoning)

	if answer.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %s, want %s", answer.StopReason, goodall.StopEndTurn)
	}
	if answer.Message.Text() == "" {
		t.Error("the second live turn produced no text")
	}
}

func TestLiveModelReportsImageAndPDFSupport(t *testing.T) {
	client := liveClient(t)

	info, err := client.Model(t.Context(), livemodel.Anthropic)
	if err != nil {
		t.Fatalf("the live catalog lookup failed: %v", err)
	}
	t.Logf("%s (%s): thinking=%s style=%s efforts=%v context=%d max_output=%d",
		info.ID, info.DisplayName, info.Capabilities.Thinking, info.Capabilities.ThinkingStyle,
		info.Capabilities.ThinkingEfforts, info.Capabilities.ContextWindow, info.Capabilities.MaxOutput)

	if info.ID != livemodel.Anthropic {
		t.Errorf("the catalog answered for %q, want %q", info.ID, livemodel.Anthropic)
	}
	if info.Capabilities.ImageInput != goodall.Supported {
		t.Errorf("image input = %s, want %s", info.Capabilities.ImageInput, goodall.Supported)
	}
	if info.Capabilities.PDFInput != goodall.Supported {
		t.Errorf("PDF input = %s, want %s", info.Capabilities.PDFInput, goodall.Supported)
	}
	if info.Capabilities.Thinking != goodall.Supported {
		t.Errorf("thinking = %s, want %s", info.Capabilities.Thinking, goodall.Supported)
	}
}

func TestLiveCompleteAnswersWithoutStreaming(t *testing.T) {
	client := liveClient(t)

	resp, err := client.Complete(t.Context(), &goodall.Request{
		Model:     livemodel.Anthropic,
		MaxTokens: liveMaxTokens,
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "Answer in one short sentence: what is the capital of France?"},
		)},
	})
	if err != nil {
		t.Fatalf("the live blocking call failed: %v", err)
	}
	t.Logf("complete: stop=%s input=%d output=%d", resp.StopReason, resp.Usage.Input, resp.Usage.Output)

	if resp.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %s, want %s", resp.StopReason, goodall.StopEndTurn)
	}
	if resp.Message.Text() == "" {
		t.Error("the live blocking call returned no text")
	}
	if resp.Usage.Output == 0 {
		t.Error("the live blocking call reported no output tokens")
	}
}
