package main

import (
	"context"
	"fmt"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
)

// The Anthropic recordings. Each exchange is one question chosen so that the
// answer is short, the path through the provider is unambiguous, and the
// fixture can be asserted on offline without the assertion being a guess
// about what a model felt like saying.

// recordedMaxTokens caps every recorded answer. It is well above what any of
// these questions needs and above the 1024-token minimum a thinking budget
// requires, so a recording is never truncated and never refused for lack of
// room to think.
const recordedMaxTokens = 2048

// weatherCity is the city every weather recording asks about, so the offline
// tests can assert on the tool input the model produced.
const weatherCity = "Paris"

// unknownModel is a model identifier Anthropic does not serve, which is the
// cheapest way to get a real error envelope: it is refused before any tokens
// are generated.
const unknownModel = "claude-no-such-model-20260101"

// anthropicExchanges builds the Anthropic recordings against one model.
func anthropicExchanges(rec *recorder, key, model string) ([]exchange, error) {
	client := anthropic.New(key, anthropic.WithHTTPClient(rec.httpClient()))

	weather, err := weatherTool()
	if err != nil {
		return nil, err
	}
	png, err := tinyPNG()
	if err != nil {
		return nil, err
	}

	// Each request is built fresh per call rather than shared, because a
	// Request is read-only once a provider holds it and two exchanges must
	// not be able to disturb each other's history.
	text := func() *goodall.Request {
		return &goodall.Request{
			Model:     model,
			MaxTokens: recordedMaxTokens,
			Messages: []goodall.Message{goodall.UserMessage(
				goodall.Text{Text: "Answer in one short sentence: what is the capital of France?"},
			)},
		}
	}
	toolUse := func() *goodall.Request {
		return &goodall.Request{
			Model:      model,
			MaxTokens:  recordedMaxTokens,
			Tools:      []goodall.Tool{weather},
			ToolChoice: goodall.ChooseTool(weather.Name()),
			Messages: []goodall.Message{goodall.UserMessage(
				goodall.Text{Text: "What is the weather in " + weatherCity + " right now?"},
			)},
		}
	}

	return []exchange{
		{
			name: "text",
			what: "a one-line answer, streamed",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "text", text())
				return err
			},
		},
		{
			name: "text_blocking",
			what: "the same question through Complete, so the two paths can be compared",
			run: func(ctx context.Context) error {
				_, err := rec.complete(ctx, client, "text_blocking", text())
				return err
			},
		},
		{
			name: "tool_use",
			what: "a forced tool call, streamed as input_json_delta fragments",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "tool_use", toolUse())
				return err
			},
		},
		{
			name: "tool_use_blocking",
			what: "the same forced tool call through Complete",
			run: func(ctx context.Context) error {
				_, err := rec.complete(ctx, client, "tool_use_blocking", toolUse())
				return err
			},
		},
		{
			name: "thinking_tool_use",
			what: "thinking plus a tool call, then a second turn replaying the thinking block with the tool result",
			run: func(ctx context.Context) error {
				return recordThinkingTurns(ctx, rec, client, model, weather)
			},
		},
		{
			name: "image",
			what: "a generated PNG the model is asked to name the color of",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "image", &goodall.Request{
					Model:     model,
					MaxTokens: recordedMaxTokens,
					Messages: []goodall.Message{goodall.UserMessage(
						goodall.Image{Source: goodall.BytesSource("image/png", png)},
						goodall.Text{Text: "Answer with one word: what color is this image?"},
					)},
				})
				return err
			},
		},
		{
			name: "pdf",
			what: "a generated one-page PDF the model is asked to quote",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "pdf", &goodall.Request{
					Model:     model,
					MaxTokens: recordedMaxTokens,
					Messages: []goodall.Message{goodall.UserMessage(
						goodall.Document{
							Source: goodall.BytesSource("application/pdf", tinyPDF()),
							Title:  "A one-line document",
						},
						goodall.Text{Text: "Reply with the single line of text in this document, exactly, and nothing else."},
					)},
				})
				return err
			},
		},
		{
			name: "error",
			what: "an unknown model, so a real error envelope is on disk",
			run: func(ctx context.Context) error {
				return rec.failing(ctx, client, "error", &goodall.Request{
					Model:     unknownModel,
					MaxTokens: recordedMaxTokens,
					Messages: []goodall.Message{goodall.UserMessage(
						goodall.Text{Text: "This request is never served."},
					)},
				})
			},
		},
	}, nil
}

// recordThinkingTurns records the two halves of a thinking tool call: the
// turn that thinks and asks for the tool, and the turn that answers once the
// result comes back.
//
// The second turn is the one worth recording. A thinking block carries a
// signature binding it to the prefix that produced it, and a conversation
// that replays it wrongly is rejected by the API rather than being quietly
// ignored — so a recorded second turn that is not an error is proof that
// goodall's replay is accepted, which no hand-authored fixture can be.
func recordThinkingTurns(ctx context.Context, rec *recorder, client *anthropic.Client, model string, weather goodall.Tool) error {
	// The tool choice is left at auto, unlike the tool_use recording.
	// Recorded 2026-09-15: claude-sonnet-5 accepts adaptive thinking
	// alongside a forced tool_choice, and then produces no thinking block
	// at all — thinking_tokens comes back zero and the message opens
	// straight onto the tool call. Forcing the call would buy determinism
	// at the cost of the one thing this recording exists to capture, so
	// the prompt does the forcing instead.
	first := &goodall.Request{
		Model:     model,
		MaxTokens: recordedMaxTokens,
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
		Tools:     []goodall.Tool{weather},
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "A cyclist in " + weatherCity + " will ride 40 km at 24 km/h, " +
				"starting at 14:00. Work out when they finish, look up the weather there, " +
				"and tell me whether they should expect rain during the ride."},
		)},
	}
	resp, err := rec.stream(ctx, client, "thinking_tool_use", first)
	if err != nil {
		return err
	}
	uses := resp.Message.ToolUses()
	if len(uses) == 0 {
		return fmt.Errorf("the model answered without calling %s, so there is no tool result to send back", weather.Name())
	}

	result := goodall.TextResult(`{"city":"` + weatherCity + `","temperature_c":18,"conditions":"light rain"}`)
	result.ToolUseID = uses[0].ID

	second := &goodall.Request{
		Model:     model,
		MaxTokens: recordedMaxTokens,
		Thinking:  first.Thinking,
		Tools:     first.Tools,
		// The second turn leaves the tool choice at auto: the model has
		// its result and should answer, and forcing the tool again
		// would only produce a second call.
		Messages: append(append([]goodall.Message(nil), first.Messages...),
			resp.Message,
			goodall.UserMessage(result),
		),
	}
	_, err = rec.stream(ctx, client, "thinking_tool_use_turn2", second)
	return err
}

// weatherTool is the tool every tool-use recording offers. Its schema is
// inferred from the handler's input type, so the fixture shows the same
// schema a consumer's tool would send.
func weatherTool() (goodall.Tool, error) {
	return goodall.NewTool("get_weather", "Look up the current weather in a city.",
		func(ctx context.Context, in struct {
			City string `json:"city" desc:"the city to look up, such as Paris"`
			Unit string `json:"unit,omitzero" desc:"celsius or fahrenheit" enum:"celsius,fahrenheit"`
		},
		) (goodall.ToolResult, error) {
			// The recorder never runs the tool: it records the call the
			// model made and sends back a canned result.
			return goodall.TextResult(""), nil
		})
}
