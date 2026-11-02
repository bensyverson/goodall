package systemone

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// wireResponse is the response body.
type wireResponse struct {
	Model   string       `json:"model"`
	Answers namedAnswers `json:"answers,omitzero"`
	Usage   wireUsage    `json:"usage,omitzero"`
}

// wireUsage is the usage object of a response, as far as every server on this
// wire reports it.
type wireUsage struct {
	// Input is the input tokens the request consumed.
	Input int `json:"input_tokens,omitzero"`
	// Output is the output tokens.
	Output int `json:"output_tokens,omitzero"`
}

// DecodeAnswers reads one response body. The cost is left unreported, because
// the wire's common shape carries none and a server that adds one is the calling
// client's to read.
//
// The error names the answer it could not read and no server; a client wraps it
// as its own protocol error.
func DecodeAnswers(data []byte) (*decision.Answers, error) {
	var w wireResponse
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	answers := decision.NewAnswers(w.Answers)
	answers.Model = w.Model
	answers.Usage = goodall.Usage{Input: w.Usage.Input, Output: w.Usage.Output}
	return answers, nil
}

// namedAnswers decodes the answers object in document order, which a map-typed
// field could not do, so the order the questions were asked in survives.
type namedAnswers []decision.NamedAnswer

// UnmarshalJSONFrom reads the answers object member by member, dispatching each
// value on its type member.
func (n *namedAnswers) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("systemone: answers must be a JSON object, got %v", tok.Kind())
	}
	out := make(namedAnswers, 0, len(*n))
	for dec.PeekKind() != '}' {
		nameTok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		// The token is voided by the next read, so the id is copied out
		// before the answer's own value is decoded.
		id := nameTok.String()
		raw, err := dec.ReadValue()
		if err != nil {
			return err
		}
		answer, err := unmarshalAnswer(raw.Clone())
		if err != nil {
			return fmt.Errorf("systemone: answer %q: %w", id, err)
		}
		out = append(out, decision.NamedAnswer{ID: id, Answer: answer})
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	*n = out
	return nil
}

// wirePredicateAnswer is a predicate's answer on this wire. The value is a
// pointer so that a missing one is told apart from a probability of 0, which
// would read as a confident no.
type wirePredicateAnswer struct {
	Noul *float64 `json:"noul"`
}

// wireChoiceAnswer is a choice's answer on this wire. It is a type of its own
// rather than [decision.ChoiceAnswer] so that a change to the neutral JSON form
// cannot move what this wire is read as.
type wireChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities,omitzero"`
	Confidence    float64            `json:"confidence"`
}

// wireScoreAnswer is a score's answer on this wire. The value is a pointer for
// the same reason a predicate's is: 0 is the lowest level, not "no score".
type wireScoreAnswer struct {
	Score         *float64           `json:"score"`
	Legend        map[string]string  `json:"legend,omitzero"`
	Probabilities map[string]float64 `json:"probabilities,omitzero"`
	Confidence    float64            `json:"confidence"`
}

// unmarshalAnswer rebuilds one answer from its JSON object. A type this version
// does not model becomes a [decision.UnknownAnswer] rather than an error: the
// request asked something the server answered, and dropping it would lose a
// judgment that was paid for. A modeled type with no value is an error, since a
// zero would state a judgment nobody made.
func unmarshalAnswer(raw jsontext.Value) (decision.Answer, error) {
	var probe wireType
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case WirePredicate:
		var w wirePredicateAnswer
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		if w.Noul == nil {
			return nil, fmt.Errorf("is a %s with no value", WirePredicate)
		}
		return decision.PredicateAnswer{Probability: *w.Noul}, nil
	case wireChoice:
		var w wireChoiceAnswer
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		if w.Choice == "" {
			return nil, fmt.Errorf("is a %s with no pick", wireChoice)
		}
		return decision.ChoiceAnswer{Choice: w.Choice, Probabilities: w.Probabilities, Confidence: w.Confidence}, nil
	case wireScore:
		var w wireScoreAnswer
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		if w.Score == nil {
			return nil, fmt.Errorf("is a %s with no value", wireScore)
		}
		return decision.ScoreAnswer{Score: *w.Score, Legend: w.Legend, Probabilities: w.Probabilities, Confidence: w.Confidence}, nil
	case wireRefusal:
		return decision.RefusalAnswer{}, nil
	}
	return decision.UnknownAnswer{Type: decision.QuestionType(probe.Type), Raw: raw}, nil
}
