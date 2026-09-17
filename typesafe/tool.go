package typesafe

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bensyverson/goodall"
)

// ToolOption configures a judgment tool at construction. It is the option type
// of both [Tool] and [AuthoredTool]; [AskOption] configures one call to
// [Client.Ask] and is not interchangeable with it.
type ToolOption func(*toolConfig)

// toolConfig is the settled options of one tool.
type toolConfig struct {
	// model is the model every call this tool makes asks for, empty for
	// whatever the client's default is.
	model string
	// description replaces the tool's own description when set.
	description string
	// described records that a description was given, so that an empty one
	// is refused rather than read as "no option passed".
	described bool
}

// WithToolModel pins the model every call this tool makes is answered by,
// overriding [DefaultModel]. It is the tool-level spelling of [WithModel],
// which configures one call rather than a construction, and it is what a caller
// whose thresholds are tuned against one version reaches for: pinned once, at
// the tool, rather than at each of the calls a model decides to make.
func WithToolModel(model string) ToolOption {
	return func(cfg *toolConfig) { cfg.model = model }
}

// WithDescription replaces the description the model reads. On an
// [AuthoredTool] it replaces [DefaultAuthoredDescription], which is what it is
// for; on a [Tool], whose description is already a parameter, it overrides that
// parameter.
func WithDescription(text string) ToolOption {
	return func(cfg *toolConfig) { cfg.description, cfg.described = text, true }
}

// settleTool applies the options and returns the client's per-call options.
func settleTool(opts []ToolOption) (toolConfig, []AskOption) {
	var cfg toolConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.model == "" {
		return cfg, nil
	}
	return cfg, []AskOption{WithModel(cfg.model)}
}

// judgmentInput is the input schema of a [Tool]: the material to judge, and
// nothing else, because the questions are the developer's.
//
// The state is raw JSON rather than a string field beside an object field. A
// [jsontext.Value] infers to one required, described member that accepts a
// string, an object or an array, which is the sentence the API's own state
// member would write; two mutually exclusive members would ask the model to
// choose a field before it writes anything, and "exactly one of these" is a
// rule the schema both providers accept cannot state.
type judgmentInput struct {
	// State is the material to judge, as the model wrote it.
	State jsontext.Value `json:"state" desc:"the material to judge: a string of text, or a JSON object or array of records. Send only what the questions need — judgment degrades on detail that no question asks about"`
}

// Tool wraps a [Client] as a [goodall.Tool] whose questions are fixed by the
// developer: the model supplies the state and reads back one typed answer per
// question, as JSON that decodes into an [Answers].
//
// This is the shape TypeSafe recommends and the one to reach for first. The
// questions and the thresholds that read their answers stay in the calling
// code, where they can be reviewed and changed together, and the model's part
// is the one thing it is better at than the developer: deciding what material
// the judgment is about. [AuthoredTool] is the other way round, for the cases
// where the questions cannot be known in advance.
//
// The description is the consumer's alone — what this judgment decides, in the
// words of their own domain — and the tool adds nothing to it, because the
// model needs no advice about a question set it is not writing.
//
// A judgment the API refuses, and one this package refuses before sending, come
// back as a result with IsError set carrying the API's own message, so the model
// can correct the state and try again; a failure of the call itself is a Go
// error, which the loop turns into an error result too.
//
// What a judgment spent travels on the call. The tool implements
// [goodall.Reporter] for the accounting rather than for progress — one call is
// one round trip with nothing to show along the way — so the judge's own tokens
// reach a consumer on that call's [goodall.ToolCallEnd] and the cost stays
// unreported, since TypeSafe publishes its prices on its website rather than on
// the response.
//
// It returns an error for a nil client, for a question set [Client.Ask] would
// refuse, and for a definition no provider would accept — the naming rules are
// [goodall.NewTool]'s:
//
//	judge, err := typesafe.Tool(client, "triage_ticket",
//	    "Decide which team a support ticket belongs to and how urgent it is.",
//	    typesafe.Questions{ … })
func Tool(client *Client, name, description string, questions Questions, opts ...ToolOption) (goodall.Tool, error) {
	if client == nil {
		return nil, &toolError{name: name, err: errNoClient}
	}
	// The questions are the developer's, so a question set that could never
	// be sent is a mistake to report here rather than on every call.
	if err := questions.validate(); err != nil {
		return nil, &toolError{name: name, err: err}
	}
	cfg, ask := settleTool(opts)
	if cfg.described {
		description = cfg.description
	}
	fixed := make(Questions, len(questions))
	copy(fixed, questions)
	return goodall.NewReportingTool(name, description, func(ctx context.Context, in judgmentInput, _ func(goodall.Event)) (goodall.ToolOutcome, error) {
		return judge(ctx, client, in.State, fixed, ask...)
	})
}

// judge asks one question set about one state and turns the outcome into what
// the model reads and what the call declares it spent.
//
// It reports no events: one call to the judge is one round trip with nothing to
// show along the way, so the tool implements [goodall.Reporter] for the
// accounting alone. The tokens are the judge's own, on the call that spent
// them, and the cost is TypeSafe's silence rather than a zero — the prices are
// on its website and goodall keeps no price list, so an unreported cost is what
// a consumer must read as "not known". A refusal spent nothing: the API either
// declined the request or this package did, before it was sent.
func judge(ctx context.Context, client *Client, state any, questions Questions, opts ...AskOption) (goodall.ToolOutcome, error) {
	answers, err := client.Ask(ctx, state, questions, opts...)
	if err != nil {
		if text, ok := refusalText(err); ok {
			return goodall.ToolOutcome{Result: goodall.ErrorResult(text)}, nil
		}
		return goodall.ToolOutcome{}, err
	}
	result, err := goodall.JSONResult(answers)
	if err != nil {
		return goodall.ToolOutcome{}, err
	}
	return goodall.ToolOutcome{Result: result, Usage: answers.Usage, Cost: answers.Cost}, nil
}

// refusalText is what a model reads when the judgment was refused rather than
// broken, and reports whether this error is one of those.
//
// A refusal is the API's answer — a 422 naming the member it could not read, a
// 401, a rate limit — or this package's own answer to a request it would not
// send. The model reads those words and acts on them, so they are the API's and
// not a Go error's wrapping of them. Everything else, a lost connection or a
// body that made no sense, is a failure of the tool rather than of the call the
// model made, and the loop reports it.
func refusalText(err error) (string, bool) {
	if apiErr, ok := errors.AsType[*goodall.APIError](err); ok {
		if apiErr.Message != "" {
			return apiErr.Message, true
		}
		return apiErr.Error(), true
	}
	if errors.Is(err, goodall.KindInvalidRequest) {
		return err.Error(), true
	}
	return "", false
}

// toolError names the tool a definition mistake came from while keeping the
// underlying error reachable with errors.Is and errors.As. The inner message's
// own package prefix is dropped so the text reads as one sentence.
type toolError struct {
	name string
	err  error
}

// Error names the tool and what is wrong with its definition.
func (e *toolError) Error() string {
	return "typesafe: tool " + strconv.Quote(e.name) + ": " + strings.TrimPrefix(e.err.Error(), "typesafe: ")
}

// Unwrap keeps the underlying error, and its [goodall.ErrorKind], reachable.
func (e *toolError) Unwrap() error { return e.err }

// errNoClient is what every adapter here reports for a nil client. It carries
// [goodall.KindInvalidRequest] so that errors.Is places a mistake in the
// construction beside the requests the API would refuse.
var errNoClient = fmt.Errorf("%w: there is no client to ask", goodall.KindInvalidRequest)
