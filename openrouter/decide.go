// The decision wires: a Client answering typed questions rather than a
// conversation. Most servers take the System One shape the shared codec knows, and
// what the codec leaves alone is what OpenRouter adds to that shape, which is read
// here. OpenAI's own spelling is in decide_openai.go.

package openrouter

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/systemone"
	"github.com/bensyverson/goodall/internal/transport"
)

// Client implements the seam every decision model sits behind.
var _ decision.Decider = (*Client)(nil)

// Decide evaluates the request's state against its questions and returns one
// answer per question, implementing [decision.Decider]. The dialect chooses the
// wire: [OpenRouter], [LMStudio] and [Generic] take System One, relative to the
// base URL, and [OpenAI] takes its own POST /decisions, relative to the base URL
// too, so a client reaches it with WithBaseURL("https://api.openai.com/v1").
//
// Under the OpenRouter and OpenAI dialects a request with no model is refused
// before any call. Thresholds do not travel between models and OpenRouter's own
// guidance is never to pick one from memory, so this client has no default to
// offer.
//
// A model's own limit, such as a question type it does not take, arrives as a 400
// whose sentence is the message of the *[goodall.APIError], since the catalog
// says nothing a table of capabilities could be built from. Answers report the
// cost in USD where the dialect's server charges one, the generation id and the
// serving host.
//
// Under the OpenAI dialect a state must marshal to a string, an object or a list
// of objects; anything else is refused before any call, because the server would
// refuse it too. A [decision.Predicate]'s True and False have no member on that
// wire and are written into its instructions, in a form measured against the same
// question through OpenRouter. No cost is stated there, so [decision.Answers].Cost
// stays unreported, meaning not known.
//
// A local server takes no key: New(WithBaseURL(local), WithDialect(LMStudio)) sends
// no Authorization header, and a request with no model leaves that member out, so
// a server with one model loaded uses it. Its error sentence reaches the
// *[goodall.APIError] in whichever shape it words one, on any status.
//
// A retried call may be billed twice, as on any call whose response was lost
// after the server evaluated it; [WithMaxRetries] with a negative value gives
// at-most-once behavior.
func (c *Client) Decide(ctx context.Context, req *decision.Request) (*decision.Answers, error) {
	answers, err := c.decide(ctx, req)
	return answers, c.named(err)
}

// decide is [Client.Decide] before the error is named.
func (c *Client) decide(ctx context.Context, req *decision.Request) (*decision.Answers, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	quirks := c.dialect.Quirks()
	if req.Model == "" && quirks.DecisionModelRequired {
		hint := ""
		if c.dialect == OpenRouter {
			hint = "; DecisionModels lists them"
		}
		return nil, fmt.Errorf("openrouter: %w: a decision request needs a model under the %s dialect, because thresholds do not travel between models%s",
			goodall.KindInvalidRequest, c.dialect, hint)
	}
	if quirks.DecisionWire == DecisionWireDecisions {
		body, err := encodeDecisionsRequest(req)
		if err != nil {
			return nil, err
		}
		raw, header, err := c.postDecision(ctx, pathDecisions, body)
		if err != nil {
			return nil, err
		}
		return decodeDecisions(raw, header)
	}
	body, err := systemone.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: %w", err)
	}
	raw, header, err := c.postDecision(ctx, pathSystemOne, body)
	if err != nil {
		return nil, err
	}
	return decodeDecision(raw, header, quirks)
}

// postDecision makes the call either wire makes, returning the whole body.
func (c *Client) postDecision(ctx context.Context, path string, body []byte) ([]byte, http.Header, error) {
	resp, err := c.http.Do(ctx, transport.Request{
		Method: http.MethodPost,
		URL:    c.baseURL + path,
		Header: c.header(),
		Body:   body,
	})
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return raw, resp.Header, nil
}

// wireDecisionExtras is what OpenRouter adds to a System One response, and the
// error object a 200 may carry instead of answers.
type wireDecisionExtras struct {
	// ID is the generation id.
	ID string `json:"id,omitzero"`
	// Provider is the host that served the call.
	Provider string `json:"provider,omitzero"`
	// Usage holds the one usage member the common wire does not have.
	Usage struct {
		// Cost is what the call was charged, in USD. It decodes straight
		// into a Decimal from the JSON number, never through a float64.
		Cost *goodall.Decimal `json:"cost,omitzero"`
	} `json:"usage,omitzero"`
	// Error is a failure reported inside an HTTP 200.
	Error *wireError `json:"error,omitzero"`
}

// decodeDecision reads one successful response. HTTP 200 is not success on this
// API, and a local server may answer a path it does not route with one, so an
// error in the body comes back as what it is, and a body that is not an answer
// set is a *[goodall.ProtocolError].
func decodeDecision(raw []byte, header http.Header, quirks Quirks) (*decision.Answers, error) {
	var extras wireDecisionExtras
	if err := json.Unmarshal(raw, &extras); err != nil {
		return nil, &goodall.ProtocolError{Reason: "the decision response could not be decoded: " + err.Error()}
	}
	if extras.Error.said() {
		return nil, inBodyError(extras.Error, header, raw)
	}
	answers, err := systemone.DecodeAnswers(raw)
	if errors.Is(err, systemone.ErrNoAnswers) {
		return nil, noAnswersError(header, raw)
	}
	if err != nil {
		return nil, &goodall.ProtocolError{Reason: "the decision response could not be decoded: " + err.Error()}
	}
	answers.Generation = extras.ID
	answers.ServedBy = extras.Provider
	if extras.Usage.Cost != nil && quirks.UsageCost {
		answers.Cost = goodall.Cost{Amount: *extras.Usage.Cost, Currency: costCurrency, Reported: true}
	}
	return answers, nil
}

// DecisionModel is one row of the decision catalog. It is not a
// [goodall.ModelInfo], which answers what a conversational model can do; the
// catalog says nothing about which questions a decision model takes.
type DecisionModel struct {
	// ID is what [decision.Request] names it by. The rows include "~" aliases
	// and ":free" variants, which name a model also listed by its own id; pin
	// the concrete id wherever a threshold depends on it.
	ID string
	// Name is the display name.
	Name string
	// ContextLength is the window in tokens, zero when unpublished.
	ContextLength int
	// InputModalities is what the model reads, as the catalog words it, such
	// as "text" and "image". Nil when unpublished, which is not the same fact
	// as reading nothing.
	InputModalities []string
	// Pricing is the published price per token, nil when unpublished, which
	// is not the same fact as free.
	Pricing *goodall.Pricing
}

// DecisionModels lists the decision models, in the order the catalog returned
// them. They are left out of the default model list, which is why they have a
// call of their own and why [Client.Model] goes on answering about conversational
// models only: nothing read here reaches its memo. Each call reads the catalog
// afresh, since it is growing and a list written down goes stale.
//
// It is refused on any other dialect than [OpenRouter], since a server that
// ignored the filter would answer with conversational models under this name.
func (c *Client) DecisionModels(ctx context.Context) ([]DecisionModel, error) {
	if c.dialect != OpenRouter {
		return nil, fmt.Errorf("openrouter: %w: the decision catalog is OpenRouter's, and this client speaks the %s dialect", goodall.KindInvalidRequest, c.dialect)
	}
	read, err := c.readCatalog(ctx, c.baseURL+pathModels+"?"+queryDecisionModels)
	if err != nil {
		return nil, err
	}
	out := make([]DecisionModel, 0, len(read.list.Data))
	for _, entry := range read.list.Data {
		row := DecisionModel{
			ID:            entry.ID,
			Name:          entry.Name,
			ContextLength: entry.ContextLength,
			Pricing:       pricing(entry.Pricing),
		}
		if arch := entry.Architecture; arch != nil && len(arch.InputModalities) > 0 {
			row.InputModalities = make([]string, 0, len(arch.InputModalities))
			for _, m := range arch.InputModalities {
				row.InputModalities = append(row.InputModalities, string(m))
			}
		}
		out = append(out, row)
	}
	return out, nil
}
