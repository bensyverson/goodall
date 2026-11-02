// OpenAI's decision wire: the same three questions as System One, spelled
// differently. Questions and answers are arrays rather than objects keyed by id,
// a distribution is an array rather than a map, and the state has rules of its
// own. Everything that spelling touches stops at this file.
//
// The rules for a state and for a predicate's criteria were chosen by measurement,
// recorded in project/2026-11-02-decision-models-findings.md.

package openrouter

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// The type values of this wire's questions and answers. They are stated here, not
// borrowed from the neutral package, so a rename there cannot move what is sent or
// how it is read.
const (
	decisionsPredicate = "predicate"
	decisionsChoice    = "choice"
	decisionsScore     = "score"
	decisionsRefusal   = "refusal"
)

// The parts of the one message a JSON state travels in.
const (
	decisionsMessageType = "message"
	decisionsUserRole    = "user"
	decisionsJSONPart    = "input_json"
)

// wireDecisionsRequest is the body of POST /decisions. Its member order is fixed
// and nothing in it is a Go map, so two identical requests are identical bytes
// (invariant 8); the state is marshaled deterministically before it arrives here.
type wireDecisionsRequest struct {
	// Model is the model that should answer.
	Model string `json:"model"`
	// Input is the state, as a string or as a list of one message.
	Input jsontext.Value `json:"input"`
	// Questions are the questions, in the author's order.
	Questions []wireDecisionsQuestion `json:"questions"`
}

// wireDecisionsMessage is the one message a JSON state travels in.
type wireDecisionsMessage struct {
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []wireDecisionsPart `json:"content"`
}

// wireDecisionsPart is the input_json part of that message.
type wireDecisionsPart struct {
	Type string         `json:"type"`
	Data jsontext.Value `json:"data"`
}

// wireDecisionsQuestion is one question of any type, each carrying only the member
// of its own. Name is always sent, because an answer is matched by it.
type wireDecisionsQuestion struct {
	Type         string                `json:"type"`
	Name         string                `json:"name"`
	Instructions string                `json:"instructions"`
	Choices      []wireDecisionsOption `json:"choices,omitzero"`
	Levels       []wireDecisionsLevel  `json:"levels,omitzero"`
}

// wireDecisionsOption is one choice as sent. Booleans are valid values on this
// wire, and goodall has only strings to send.
type wireDecisionsOption struct {
	Value       string `json:"value"`
	Description string `json:"description,omitzero"`
}

// wireDecisionsLevel is one rung of a score. A level is a single string in the
// neutral form, and the label is the required member, so it goes there.
type wireDecisionsLevel struct {
	Label string `json:"label"`
}

// encodeDecisionsRequest writes req as the request body, or refuses a state this
// wire cannot carry.
func encodeDecisionsRequest(req *decision.Request) ([]byte, error) {
	state, err := json.Marshal(req.State, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("%w: the state cannot be marshaled as JSON: %w", goodall.KindInvalidRequest, err)
	}
	input, err := decisionsInput(state)
	if err != nil {
		return nil, err
	}
	questions := make([]wireDecisionsQuestion, 0, len(req.Questions))
	for _, nq := range req.Questions {
		question, err := decisionsQuestion(nq)
		if err != nil {
			return nil, err
		}
		questions = append(questions, question)
	}
	return json.Marshal(&wireDecisionsRequest{Model: req.Model, Input: input, Questions: questions})
}

// decisionsInput applies the state rule to a marshaled state. It goes by what the
// state marshals to rather than its Go type, since that is what the server reads:
// text is input, and an object or a list of objects is an input_json part. Every
// other shape draws a 400 from the server (observed 2026-11-02), and there is no
// spelling of it that would not be this package deciding how the model reads the
// caller's data, so it is refused here, with the way out, before any call.
func decisionsInput(state jsontext.Value) (jsontext.Value, error) {
	switch state.Kind() {
	case '"':
		return state, nil
	case '{':
	case '[':
		if err := objectsOnly(state); err != nil {
			return nil, err
		}
	default:
		return nil, refusedState(kindName(state.Kind()))
	}
	return json.Marshal([]wireDecisionsMessage{{
		Type:    decisionsMessageType,
		Role:    decisionsUserRole,
		Content: []wireDecisionsPart{{Type: decisionsJSONPart, Data: state}},
	}})
}

// objectsOnly refuses a list holding anything but objects, naming the first item
// that is not one.
func objectsOnly(list jsontext.Value) error {
	dec := jsontext.NewDecoder(bytes.NewReader(list))
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	for i := 0; dec.PeekKind() != ']'; i++ {
		if kind := dec.PeekKind(); kind != '{' {
			return refusedState("a list whose item " + strconv.Itoa(i) + " is " + kindName(kind))
		}
		if err := dec.SkipValue(); err != nil {
			return err
		}
	}
	return nil
}

// refusedState is the sentence a caller reads, and a model too when the state was
// the model's to write, so it says what would be accepted.
func refusedState(found string) error {
	return fmt.Errorf("openrouter: %w: the %s decision wire reads a state only as a string, an object or a list of objects, and this one is %s, so nothing was sent; send it as a string of text, or as an object whose key says what the value is",
		goodall.KindInvalidRequest, OpenAI, found)
}

// kindName words a JSON kind for that sentence.
func kindName(kind jsontext.Kind) string {
	switch kind {
	case '"':
		return "a string"
	case '0':
		return "a number"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	case '[':
		return "an array"
	}
	return "an object"
}

// decisionsQuestion spells one question. The switch is exhaustive because
// [decision.Question] is sealed; what reaches the end is a nil question, which a
// validated request does not carry.
func decisionsQuestion(nq decision.NamedQuestion) (wireDecisionsQuestion, error) {
	switch question := nq.Question.(type) {
	case decision.Predicate:
		return wireDecisionsQuestion{Type: decisionsPredicate, Name: nq.ID, Instructions: foldCriteria(question)}, nil
	case decision.Choice:
		options := make([]wireDecisionsOption, 0, len(question.Options))
		for _, opt := range question.Options {
			options = append(options, wireDecisionsOption{Value: opt.Key, Description: opt.Description})
		}
		return wireDecisionsQuestion{Type: decisionsChoice, Name: nq.ID, Instructions: question.Instructions, Choices: options}, nil
	case decision.Score:
		levels := make([]wireDecisionsLevel, 0, len(question.Levels))
		for _, level := range question.Levels {
			levels = append(levels, wireDecisionsLevel{Label: level})
		}
		return wireDecisionsQuestion{Type: decisionsScore, Name: nq.ID, Instructions: question.Instructions, Levels: levels}, nil
	}
	return wireDecisionsQuestion{}, fmt.Errorf("openrouter: question %q carries %T, which this wire has no spelling for", nq.ID, nq.Question)
}

// foldCriteria writes a predicate's descriptions into its instructions, since this
// wire has no member for them. The spelling is a prompt, chosen because it answered
// as the same question through OpenRouter does where nearby spellings did not, in
// the findings doc: changing it invalidates those figures.
func foldCriteria(p decision.Predicate) string {
	out := p.Instructions
	separator := "\n\n"
	if p.True != "" {
		out += separator + "True: " + p.True
		separator = "\n"
	}
	if p.False != "" {
		out += separator + "False: " + p.False
	}
	return out
}

// wireDecisionsResponse is a response body, or the error object a 200 may carry
// instead. Answers stays undecoded until each one's type is known.
type wireDecisionsResponse struct {
	Model   string            `json:"model"`
	Answers []jsontext.Value  `json:"answers"`
	Usage   wireDecisionUsage `json:"usage,omitzero"`
	Error   *wireError        `json:"error,omitzero"`
}

// wireDecisionUsage is the usage object of this wire, whose details are the ones
// Chat Completions names under other parents.
type wireDecisionUsage struct {
	InputTokens         int                      `json:"input_tokens,omitzero"`
	InputTokensDetails  *promptTokensDetails     `json:"input_tokens_details,omitzero"`
	OutputTokens        int                      `json:"output_tokens,omitzero"`
	OutputTokensDetails *completionTokensDetails `json:"output_tokens_details,omitzero"`
}

// wireDecisionsHead is what every answer shares, which decides how the rest is
// read. The name is a pointer because a null one is its own surprise.
type wireDecisionsHead struct {
	Type string  `json:"type"`
	Name *string `json:"name"`
}

// wireDecisionsPredicate is a predicate's answer. The value is a pointer so that a
// missing one is told apart from a probability of 0, a confident no.
type wireDecisionsPredicate struct {
	Probability *float64 `json:"probability"`
}

// wireDecisionsPick is a choice's answer.
type wireDecisionsPick struct {
	Choice        decisionsValue `json:"choice"`
	Confidence    float64        `json:"confidence"`
	Probabilities []struct {
		Value       decisionsValue `json:"value"`
		Probability float64        `json:"probability"`
	} `json:"probabilities,omitzero"`
}

// wireDecisionsRating is a score's answer. Both pointers are for the same reason a
// predicate's is: 0 is the lowest level, not a missing one.
type wireDecisionsRating struct {
	Score         *float64 `json:"score"`
	Confidence    float64  `json:"confidence"`
	Probabilities []struct {
		Value       *int    `json:"value"`
		Label       string  `json:"label"`
		Probability float64 `json:"probability"`
	} `json:"probabilities,omitzero"`
}

// decisionsValue is a choice's value as read back. This wire allows a boolean, and
// goodall only sends strings, so a boolean coming back is a server answering a
// question nobody asked, which stays loud rather than reading as the text "true".
type decisionsValue string

// UnmarshalJSONFrom reads a string and refuses anything else, by name.
func (v *decisionsValue) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	kind := dec.PeekKind()
	if kind != '"' {
		return fmt.Errorf("a choice value is %s, and every value this client sends is a string", kindName(kind))
	}
	var text string
	if err := json.UnmarshalDecode(dec, &text); err != nil {
		return err
	}
	*v = decisionsValue(text)
	return nil
}

// decodeDecisions reads one response. HTTP 200 is not success until the body says
// so, and a body that is not an answer set is a *[goodall.ProtocolError].
//
// The cost stays unreported, because this server states none, and the zero means
// "not known". The body names no id, so the generation is the request id from the
// header, which is what this server's logs and support go by.
func decodeDecisions(raw []byte, header http.Header) (*decision.Answers, error) {
	var w wireDecisionsResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, decisionsProtocolError(err)
	}
	if w.Error.said() {
		return nil, inBodyError(w.Error, header, raw)
	}
	if w.Answers == nil {
		return nil, decisionsProtocolError(errors.New("it has no answers"))
	}
	entries := make([]decision.NamedAnswer, 0, len(w.Answers))
	for i, value := range w.Answers {
		entry, err := decisionsAnswer(i, value)
		if err != nil {
			return nil, decisionsProtocolError(err)
		}
		entries = append(entries, entry)
	}
	answers := decision.NewAnswers(entries)
	answers.Model = w.Model
	answers.Generation = requestID(header)
	answers.Usage, _ = translateUsage(&usage{
		PromptTokens:            w.Usage.InputTokens,
		PromptTokensDetails:     w.Usage.InputTokensDetails,
		CompletionTokens:        w.Usage.OutputTokens,
		CompletionTokensDetails: w.Usage.OutputTokensDetails,
	})
	return answers, nil
}

// decisionsProtocolError is the error a malformed response comes back as.
func decisionsProtocolError(err error) error {
	return &goodall.ProtocolError{Reason: "the decision response could not be decoded: " + err.Error()}
}

// decisionsAnswer rebuilds one answer. goodall names every question, so an answer
// with no name is one this client's request could not have drawn. A type this
// version does not model stays whole as a [decision.UnknownAnswer], since it was
// paid for; a modeled type with no value is an error, since a zero would state a
// judgment nobody made.
func decisionsAnswer(index int, raw jsontext.Value) (decision.NamedAnswer, error) {
	var head wireDecisionsHead
	if err := json.Unmarshal(raw, &head); err != nil {
		return decision.NamedAnswer{}, fmt.Errorf("answer %d: %w", index, err)
	}
	if head.Name == nil {
		return decision.NamedAnswer{}, fmt.Errorf("answer %d has no name, and every question this client sends has one", index)
	}
	answer, err := decisionsAnswerBody(head.Type, raw)
	if err != nil {
		return decision.NamedAnswer{}, fmt.Errorf("answer %q: %w", *head.Name, err)
	}
	return decision.NamedAnswer{ID: *head.Name, Answer: answer}, nil
}

// decisionsAnswerBody reads an answer by its type.
func decisionsAnswerBody(kind string, raw jsontext.Value) (decision.Answer, error) {
	switch kind {
	case decisionsPredicate:
		var w wireDecisionsPredicate
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		if w.Probability == nil {
			return nil, errors.New("is a predicate with no probability")
		}
		return decision.PredicateAnswer{Probability: *w.Probability}, nil
	case decisionsChoice:
		var w wireDecisionsPick
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		if w.Choice == "" {
			return nil, errors.New("is a choice with no pick")
		}
		answer := decision.ChoiceAnswer{Choice: string(w.Choice), Confidence: w.Confidence}
		if len(w.Probabilities) > 0 {
			answer.Probabilities = make(map[string]float64, len(w.Probabilities))
		}
		for _, entry := range w.Probabilities {
			answer.Probabilities[string(entry.Value)] = entry.Probability
		}
		return answer, nil
	case decisionsScore:
		return decisionsScoreAnswer(raw)
	case decisionsRefusal:
		return decision.RefusalAnswer{}, nil
	}
	return decision.UnknownAnswer{Type: decision.QuestionType(kind), Raw: raw}, nil
}

// decisionsScoreAnswer reads a score into the neutral form, whose legend and
// probabilities are keyed by level index as decimal strings.
func decisionsScoreAnswer(raw jsontext.Value) (decision.Answer, error) {
	var w wireDecisionsRating
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	if w.Score == nil {
		return nil, errors.New("is a score with no value")
	}
	answer := decision.ScoreAnswer{Score: *w.Score, Confidence: w.Confidence}
	if len(w.Probabilities) > 0 {
		answer.Legend = make(map[string]string, len(w.Probabilities))
		answer.Probabilities = make(map[string]float64, len(w.Probabilities))
	}
	for i, entry := range w.Probabilities {
		if entry.Value == nil {
			return nil, fmt.Errorf("has a level with no index at position %d", i)
		}
		key := strconv.Itoa(*entry.Value)
		answer.Legend[key] = entry.Label
		answer.Probabilities[key] = entry.Probability
	}
	return answer, nil
}
