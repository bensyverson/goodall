package main

import (
	"context"
	"fmt"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
)

// The OpenRouter recordings. They cover the same ground as the Anthropic ones
// — text streamed and blocking, a forced tool call both ways, thinking across
// a tool result, an image, a PDF and an error envelope — plus the two things
// only a router has: the upstream body OpenRouter sent on to Anthropic, and
// the same reasoning round trip through an OpenAI model, whose
// reasoning_details are encrypted or summarised rather than signed text.
//
// Every exchange here runs against the Claude model unless its name says
// otherwise, because that is the pairing the offline tests compare with the
// Anthropic recordings.

// openRouterUnknownModel is a model identifier OpenRouter does not route,
// which is the cheapest way to get a real error envelope: it is refused
// before any tokens are generated. OpenRouter answers an unrecognised id with
// HTTP 400 and "… is not a valid model ID", not with a 404.
const openRouterUnknownModel = "anthropic/claude-no-such-model"

// openRouterExchanges builds the recordings made through openrouter.ai. model
// is the Claude model; the OpenAI reasoning model is named by livemodel, since
// an exchange that must be an OpenAI model cannot take the -model override
// without ceasing to be the thing it records.
func openRouterExchanges(rec *recorder, key, model string) ([]exchange, error) {
	client := openrouter.New(
		openrouter.WithAPIKey(key),
		openrouter.WithHTTPClient(rec.httpClient()),
	)
	return chatCompletionExchanges(rec, client, model, livemodel.OpenRouterOpenAI)
}

// openAIExchanges builds the recordings made against api.openai.com itself,
// through the same package under the OpenAI dialect. They are the proof that
// the dialect's narrower request — max_completion_tokens rather than
// max_tokens, a top-level reasoning_effort, stream_options.include_usage, and
// none of OpenRouter's own members — is a request OpenAI accepts.
//
// There is no reasoning round trip here, and that is a fact about the endpoint
// rather than an omission. Observed 2026-09-15 on both gpt-6-astra and
// gpt-5.6-terra: /v1/chat/completions answers a request carrying function
// tools and any reasoning effort above "none" with HTTP 400, "Function tools
// with reasoning_effort are not supported … use /v1/responses or set
// reasoning_effort to 'none'". Chat Completions is the only format this
// package speaks, so a tool call and reasoning cannot be had in one turn
// against OpenAI directly; the reasoning round trip on an OpenAI model is
// recorded through OpenRouter instead, which routes upstream to /v1/responses.
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
	return []exchange{
		{
			name: "openai_text",
			what: "a one-line answer from api.openai.com under the OpenAI dialect, streamed",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "openai_text", textRequest(model))
				return err
			},
		},
		{
			name: "openai_tool_use",
			what: "a forced tool call, which this endpoint serves only with reasoning turned off",
			run: func(ctx context.Context) error {
				req := toolUseRequest(model, weather)
				req.Thinking = goodall.ThinkingConfig{Effort: goodall.EffortOff}
				_, err := rec.stream(ctx, client, "openai_tool_use", req)
				return err
			},
		},
		{
			name: "openai_reasoning",
			what: "a reasoning answer with no tools, the shape this endpoint does serve",
			run: func(ctx context.Context) error {
				req := &goodall.Request{
					Model:     model,
					MaxTokens: recordedMaxTokens,
					Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow},
					Messages: []goodall.Message{goodall.UserMessage(
						goodall.Text{Text: "A cyclist rides 40 km at 24 km/h, starting at 14:00. " +
							"Answer in one short sentence: when do they finish?"},
					)},
				}
				_, err := rec.stream(ctx, client, "openai_reasoning", req)
				return err
			},
		},
	}, nil
}

// chatCompletionExchanges is the OpenRouter set. It is a function of the
// client so that the dialect, the base URL and the key stay the caller's
// choice, and of both models because two of the recordings exist to show the
// difference between them.
func chatCompletionExchanges(rec *recorder, client *openrouter.Client, model, reasoningModel string) ([]exchange, error) {
	weather, err := weatherTool()
	if err != nil {
		return nil, err
	}
	png, err := tinyPNG()
	if err != nil {
		return nil, err
	}
	return []exchange{
		{
			name: "text",
			what: "a one-line answer, streamed",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "text", textRequest(model))
				return err
			},
		},
		{
			name: "text_blocking",
			what: "the same question through Complete, so the two paths can be compared",
			run: func(ctx context.Context) error {
				_, err := rec.complete(ctx, client, "text_blocking", textRequest(model))
				return err
			},
		},
		{
			name: "tool_use",
			what: "a forced tool call, streamed as arguments fragments keyed by index",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "tool_use", toolUseRequest(model, weather))
				return err
			},
		},
		{
			name: "tool_use_blocking",
			what: "the same forced tool call through Complete",
			run: func(ctx context.Context) error {
				_, err := rec.complete(ctx, client, "tool_use_blocking", toolUseRequest(model, weather))
				return err
			},
		},
		{
			name: "thinking_tool_use",
			what: "thinking plus a tool call on the Claude model, then a second turn replaying the reasoning_details with the tool result",
			run: func(ctx context.Context) error {
				return recordReasoningTurns(ctx, rec, client, "thinking_tool_use", model, goodall.EffortLow, 2, weather)
			},
		},
		{
			name: "encrypted_reasoning_tool_use",
			what: "the same round trip on an OpenAI reasoning model through OpenRouter, whose reasoning_details are encrypted or summarised rather than signed text",
			run: func(ctx context.Context) error {
				// Recorded at high effort, not low: at low this
				// model answered the same question with
				// reasoning_tokens zero and no reasoning_details
				// at all (2026-09-15), which would have made the
				// round trip this recording exists to prove a
				// round trip of nothing.
				return recordReasoningTurns(ctx, rec, client, "encrypted_reasoning_tool_use", reasoningModel, goodall.EffortHigh, 3, weather)
			},
		},
		{
			name: "image",
			what: "a generated PNG the model is asked to name the colour of",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "image", &goodall.Request{
					Model:     model,
					MaxTokens: recordedMaxTokens,
					Messages: []goodall.Message{goodall.UserMessage(
						goodall.Image{Source: goodall.BytesSource("image/png", png)},
						goodall.Text{Text: "Answer with one word: what colour is this image?"},
					)},
				})
				return err
			},
		},
		{
			name: "pdf",
			what: "a generated one-page PDF, read by the file-parser plugin's native engine",
			run: func(ctx context.Context) error {
				_, err := rec.stream(ctx, client, "pdf", &goodall.Request{
					Model:     model,
					MaxTokens: recordedMaxTokens,
					// A PDF reaches an upstream model through
					// OpenRouter's file-parser plugin. The native
					// engine hands the file to a model that reads
					// files itself, which is what makes this
					// recording the counterpart of the Anthropic
					// one rather than a recording of an OCR pass.
					Extensions: openrouter.Extensions{
						Plugins: []openrouter.Plugin{{
							ID:  openrouter.PluginFileParser,
							PDF: &openrouter.PDFOptions{Engine: openrouter.PDFEngineNative},
						}},
					},
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
			name: "debug_echo",
			what: "the same question with debug.echo_upstream_body, so the Anthropic-shaped body OpenRouter sent upstream is on disk",
			run: func(ctx context.Context) error {
				req := textRequest(model)
				req.Extensions = openrouter.Extensions{
					Debug: openrouter.Debug{EchoUpstreamBody: true},
				}
				_, err := rec.stream(ctx, client, "debug_echo", req)
				return err
			},
		},
		{
			name: "error",
			what: "an unknown model, so a real error envelope is on disk",
			run: func(ctx context.Context) error {
				return rec.failing(ctx, client, "error", &goodall.Request{
					Model:     openRouterUnknownModel,
					MaxTokens: recordedMaxTokens,
					Messages: []goodall.Message{goodall.UserMessage(
						goodall.Text{Text: "This request is never served."},
					)},
				})
			},
		},
	}, nil
}

// textRequest is the one-line question, built fresh per call: a Request is
// read-only once a provider holds it, and two exchanges must not be able to
// disturb each other's history.
func textRequest(model string) *goodall.Request {
	return &goodall.Request{
		Model:     model,
		MaxTokens: recordedMaxTokens,
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "Answer in one short sentence: what is the capital of France?"},
		)},
	}
}

// toolUseRequest is the forced weather call.
func toolUseRequest(model string, weather goodall.Tool) *goodall.Request {
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

// recordReasoningTurns records a reasoning tool call turn by turn: the turn
// that thinks and asks for the tool, the turn that answers once the result
// comes back, and — where turns is 3 — one more turn on top of that. They are
// written as <name>, <name>_turn2 and <name>_turn3.
//
// The turn that replays is the one worth recording. reasoning_details are the
// model-agnostic carrier of an Anthropic signature and of OpenAI's encrypted
// reasoning, and both are rejected rather than ignored when they are replayed
// wrongly — so a recorded turn that is not an error is proof that goodall's
// replay is accepted, which no hand-authored fixture can be.
//
// Which turn that is depends on the model. A Claude model thinks before it
// calls the tool, so the second turn replays its signed entry. An OpenAI model
// through OpenRouter does not: observed 2026-09-15 at high effort, gpt-6-astra
// returned reasoning_tokens zero and no reasoning_details on the tool-calling
// turn and 98 reasoning_details fragments on the turn that answered, so only a
// third turn puts its encrypted entries back on the wire.
func recordReasoningTurns(ctx context.Context, rec *recorder, client *openrouter.Client, name, model string, effort goodall.Effort, turns int, weather goodall.Tool) error {
	// The tool choice is left at auto and the question is made to need
	// arithmetic: a forced tool_choice buys determinism at the cost of the
	// one thing this recording exists to capture, since a model told which
	// tool to call has nothing left to think about. The prompt does the
	// forcing instead.
	first := &goodall.Request{
		Model:     model,
		MaxTokens: recordedMaxTokens,
		Thinking:  goodall.ThinkingConfig{Effort: effort, Display: goodall.DisplaySummarized},
		Tools:     []goodall.Tool{weather},
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "A cyclist in " + weatherCity + " will ride 40 km at 24 km/h, " +
				"starting at 14:00. Work out when they finish, look up the weather there, " +
				"and tell me whether they should expect rain during the ride."},
		)},
	}
	resp, err := rec.stream(ctx, client, name, first)
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
		Messages: append(append([]goodall.Message(nil), first.Messages...),
			resp.Message,
			goodall.UserMessage(result),
		),
	}
	answer, err := rec.stream(ctx, client, name+"_turn2", second)
	if err != nil || turns < 3 {
		return err
	}

	// A third turn, for the models that reason on the turn that answers
	// rather than on the turn that calls the tool. Its request replays the
	// second turn's reasoning entries, so it is where the replay of an
	// entry goodall cannot read is actually put to the server.
	third := &goodall.Request{
		Model:     model,
		MaxTokens: recordedMaxTokens,
		Thinking:  first.Thinking,
		Tools:     first.Tools,
		Messages: append(append([]goodall.Message(nil), second.Messages...),
			answer.Message,
			goodall.UserMessage(goodall.Text{Text: "Answer in one short sentence: what if they set off an hour later?"}),
		),
	}
	_, err = rec.stream(ctx, client, name+"_turn3", third)
	return err
}
