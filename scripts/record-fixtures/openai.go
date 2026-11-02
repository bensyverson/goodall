package main

import (
	"context"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/openrouter"
)

// openAIUnknownModel is a model api.openai.com does not serve, the cheapest way to
// get a real error envelope.
const openAIUnknownModel = "gpt-no-such-model"

// openAIExchanges builds the recordings made against api.openai.com itself, through
// the openrouter package under the OpenAI dialect, so their conversation goes over
// /v1/responses. They differ from scripts/probe-responses, whose requests are built
// by hand: these are what the production client sends, so each one that answers is
// proof the API accepts it.
//
// They land in openrouter/testdata beside the OpenRouter recordings, prefixed
// openai_, because they are that package's tests to read.
func openAIExchanges(rec *recorder, key, model string) ([]exchange, error) {
	client := openrouter.New(
		openrouter.WithAPIKey(key),
		openrouter.WithBaseURL("https://api.openai.com/v1"),
		openrouter.WithDialect(openrouter.OpenAI),
		openrouter.WithHTTPClient(rec.httpClient()),
	)
	weather, err := weatherTool()
	if err != nil {
		return nil, err
	}
	strictWeather, err := strictWeatherTool()
	if err != nil {
		return nil, err
	}
	png, err := tinyPNG()
	if err != nil {
		return nil, err
	}
	return append([]exchange{
		{
			name: "openai_text",
			what: "a one-line answer on /v1/responses, streamed",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "openai_text", textRequest(model))
				return err
			},
		},
		{
			name: "openai_text_blocking",
			what: "the same question through Complete, so the two paths can be compared",
			run: func(ctx context.Context) error {
				_, err := rec.complete(ctx, client, "openai_text_blocking", textRequest(model))
				return err
			},
		},
		{
			name: "openai_tool_use",
			what: "a forced tool call, sent with strict false stated",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "openai_tool_use", toolUseRequest(model, weather))
				return err
			},
		},
		{
			name: "openai_strict_tool_use",
			what: "a strict tool call under tool_choice auto, every parameter required as OpenAI's strict mode demands",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "openai_strict_tool_use", strictToolRequest(model, strictWeather))
				return err
			},
		},
		{
			name: "openai_thinking_tool_use",
			what: "reasoning plus a tool call, then a second turn replaying the encrypted reasoning item with the tool result",
			run: func(ctx context.Context) error {
				// High, because at low effort a model can answer with no
				// reasoning item, leaving this a round trip of nothing.
				return recordReasoningTurns(ctx, rec, client, "openai_thinking_tool_use", model, goodall.EffortHigh, 2, weather)
			},
		},
		{
			name: "openai_image",
			what: "a generated PNG the model is asked to name the color of, sent as input_image",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "openai_image", &goodall.Request{
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
			name: "openai_pdf",
			what: "a generated one-page PDF, sent as input_file",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "openai_pdf", &goodall.Request{
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
			name: "openai_error",
			what: "an unknown model, so a real error envelope from this endpoint is on disk",
			run: func(ctx context.Context) error {
				return rec.failing(ctx, client, "openai_error", &goodall.Request{
					Model:     openAIUnknownModel,
					MaxTokens: recordedMaxTokens,
					Messages: []goodall.Message{goodall.UserMessage(
						goodall.Text{Text: "This request is never served."},
					)},
				})
			},
		},
	}, openAIDecisionExchanges(rec, client)...), nil
}
