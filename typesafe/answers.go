package typesafe

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"slices"

	"github.com/bensyverson/goodall"
)

// Answers is the body of one successful [Client.Ask]: one typed answer per
// question, under the ids the questions were sent with.
//
// The answers are read through the accessor for the type that was asked —
// [Answers.Noul], [Answers.Choice], [Answers.Score] — which is what turns a
// mismatch into an error naming the id rather than a zero value. It round-trips
// through JSON: encoding an Answers and decoding it again gives the same
// answers in the same order, which is what lets a tool hand a set of judgments
// to a model and read it back.
type Answers struct {
	// Model is the versioned model id that answered, which is reported even
	// when an alias was sent. Log it: an alias moves when a release ships.
	Model string
	// Usage counts the tokens the call consumed. TypeSafe reports input and
	// output tokens and nothing else, so the cache and reasoning fields are
	// always zero. Output tokens are not charged for.
	Usage goodall.Usage
	// Cost is what the call cost, and is never reported: TypeSafe publishes
	// its prices on its website rather than on this response, and goodall
	// keeps no price list. A zero amount with Reported false means "not
	// known", never "free".
	Cost goodall.Cost

	// entries are the answers in the order they arrived, which is the order
	// the questions were sent in.
	entries []NamedAnswer
}

// NamedAnswer is one answer and the id it came back under.
type NamedAnswer struct {
	// ID is the id the matching question was sent under.
	ID string
	// Answer is the answer itself.
	Answer Answer
}

// Answer is one typed answer. The interface is sealed: [NoulAnswer],
// [ChoiceAnswer], [ScoreAnswer] and [UnknownAnswer] are the only
// implementations, so a type switch over an Answer is exhaustive once it
// handles Unknown.
type Answer interface {
	// AnswerType names the answer's wire type, which matches the type of
	// the question that asked for it.
	AnswerType() QuestionType
	// MarshalJSONTo writes the answer as the JSON object the API sent.
	MarshalJSONTo(enc *jsontext.Encoder) error
	// isAnswer seals the interface.
	isAnswer()
}

// AnswerError is an answer that could not be read as the type asked for: the id
// is not in the response at all, or it is and carries another type. It is
// always used as a pointer, so errors.As reaches it.
type AnswerError struct {
	// ID is the question id that was read.
	ID string
	// Want is the type the accessor asked for.
	Want QuestionType
	// Got is the type the response carried, empty when there was no answer
	// under this id.
	Got QuestionType
}

// Error names the id and both types, because a caller holding twenty answers
// has nothing else to go on.
func (e *AnswerError) Error() string {
	if e.Got == "" {
		return fmt.Sprintf("typesafe: no answer for question %q, so there is no %s to read",
			e.ID, e.Want)
	}
	return fmt.Sprintf("typesafe: question %q was answered as %s, not as %s",
		e.ID, e.Got, e.Want)
}

// All lists the answers in the order they arrived, which is the order the
// questions were sent in. The slice is the caller's to keep; the answers in it
// are values.
func (a *Answers) All() []NamedAnswer { return slices.Clone(a.entries) }

// IDs lists the question ids in the order their answers arrived.
func (a *Answers) IDs() []string {
	out := make([]string, 0, len(a.entries))
	for _, entry := range a.entries {
		out = append(out, entry.ID)
	}
	return out
}

// Lookup returns the answer under id, whatever its type, and reports whether
// there was one. It is the escape hatch for a caller holding questions it built
// dynamically; a caller that knows what it asked uses the typed accessors.
func (a *Answers) Lookup(id string) (Answer, bool) {
	for _, entry := range a.entries {
		if entry.ID == id {
			return entry.Answer, true
		}
	}
	return nil, false
}

// Noul reads the answer to a [Noul] question. A missing id, or an answer of
// another type, is an *[AnswerError] naming the id and both types.
func (a *Answers) Noul(id string) (NoulAnswer, error) {
	return answerAs[NoulAnswer](a, id, TypeNoul)
}

// Choice reads the answer to a [Choice] question. A missing id, or an answer of
// another type, is an *[AnswerError] naming the id and both types.
func (a *Answers) Choice(id string) (ChoiceAnswer, error) {
	return answerAs[ChoiceAnswer](a, id, TypeChoice)
}

// Score reads the answer to a [Score] question. A missing id, or an answer of
// another type, is an *[AnswerError] naming the id and both types.
func (a *Answers) Score(id string) (ScoreAnswer, error) {
	return answerAs[ScoreAnswer](a, id, TypeScore)
}

// answerAs is the one body the three accessors share.
func answerAs[T Answer](a *Answers, id string, want QuestionType) (T, error) {
	var zero T
	answer, ok := a.Lookup(id)
	if !ok {
		return zero, &AnswerError{ID: id, Want: want}
	}
	typed, ok := answer.(T)
	if !ok {
		return zero, &AnswerError{ID: id, Want: want, Got: answer.AnswerType()}
	}
	return typed, nil
}

// MarshalJSONTo writes the answer set as the API sends it, with the answers in
// the order they arrived.
func (a *Answers) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeStringMember(enc, "model", a.Model); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.String("answers")); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, entry := range a.entries {
		if err := enc.WriteToken(jsontext.String(entry.ID)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, entry.Answer); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.String("usage")); err != nil {
		return err
	}
	if err := json.MarshalEncode(enc, &wireUsage{Input: a.Usage.Input, Output: a.Usage.Output}); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// UnmarshalJSONFrom reads one response body. The answers object is read member
// by member so their order — the order the questions were asked in — survives,
// which a map-typed field could not do.
func (a *Answers) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w wireResponse
	if err := json.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	a.Model = w.Model
	a.Usage = goodall.Usage{Input: w.Usage.Input, Output: w.Usage.Output}
	a.Cost = goodall.Cost{}
	a.entries = w.Answers
	return nil
}

// wireResponse is the body of POST /v1/systemone.
type wireResponse struct {
	Model   string       `json:"model"`
	Answers namedAnswers `json:"answers,omitzero"`
	Usage   wireUsage    `json:"usage,omitzero"`
}

// namedAnswers decodes the answers object in document order.
type namedAnswers []NamedAnswer

// UnmarshalJSONFrom reads the answers object member by member, dispatching each
// value on its type member.
func (n *namedAnswers) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("typesafe: answers must be a JSON object, got %v", tok.Kind())
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
			return fmt.Errorf("typesafe: answer %q: %w", id, err)
		}
		out = append(out, NamedAnswer{ID: id, Answer: answer})
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	*n = out
	return nil
}

// unmarshalAnswer rebuilds one answer from its JSON object. A type this version
// does not model becomes an [UnknownAnswer] rather than an error: the request
// asked something the API answered, and dropping it would lose a judgment that
// was paid for.
func unmarshalAnswer(raw jsontext.Value) (Answer, error) {
	var probe wireQuestionType
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case TypeNoul:
		var answer NoulAnswer
		if err := json.Unmarshal(raw, &answer); err != nil {
			return nil, err
		}
		return answer, nil
	case TypeChoice:
		var answer ChoiceAnswer
		if err := json.Unmarshal(raw, &answer); err != nil {
			return nil, err
		}
		return answer, nil
	case TypeScore:
		var answer ScoreAnswer
		if err := json.Unmarshal(raw, &answer); err != nil {
			return nil, err
		}
		return answer, nil
	}
	return UnknownAnswer{Type: probe.Type, Raw: raw}, nil
}
