package decision

import (
	"encoding/json/jsontext"
	"fmt"
	"slices"

	"github.com/bensyverson/goodall"
)

// Answers is the body of one successful [Decider.Decide]: one typed answer per
// question, under the ids the questions were sent with.
//
// The answers are read through the accessor for the type that was asked,
// [Answers.Predicate], [Answers.Choice] or [Answers.Score], which is what turns
// a mismatch into an error naming the id rather than a zero value. It round-trips
// through JSON in a form of its own, documented on [Answers.MarshalJSONTo]:
// encoding an Answers and decoding it again gives the same answers in the same
// order, which is what lets a tool hand a set of judgments to a model and read
// it back.
//
// A [Decider] in another package builds one with [NewAnswers] and then sets the
// exported fields.
type Answers struct {
	// Model is the model id that answered, which a server may report as a
	// versioned id even when an alias was sent. Log it: an alias moves when a
	// release ships.
	Model string
	// Usage counts the tokens the call consumed, as far as the server breaks
	// them out.
	Usage goodall.Usage
	// Cost is what the call cost. A zero amount with Reported false means
	// "not known", never "free": a server that publishes its prices elsewhere
	// reports none here, and goodall keeps no price list.
	Cost goodall.Cost
	// Generation is the server's own id for this call, for finding it in that
	// server's logs and billing. Empty when the server names none.
	Generation string
	// ServedBy is the host that actually ran the model, where a server routes
	// one model id to several. Two calls to one id may land on two hosts, which
	// is worth knowing when a threshold moves. Empty when the server does not
	// say.
	ServedBy string

	// entries are the answers in the order they arrived, which is the order
	// the questions were sent in.
	entries []NamedAnswer
}

// NewAnswers builds an answer set from entries in the order they arrived, which
// is how a [Decider] in another package hands back what its server said. The
// slice is copied, since an Answers does not change once built.
func NewAnswers(entries []NamedAnswer) *Answers {
	return &Answers{entries: slices.Clone(entries)}
}

// NamedAnswer is one answer and the id it came back under.
type NamedAnswer struct {
	// ID is the id the matching question was sent under.
	ID string
	// Answer is the answer itself.
	Answer Answer
}

// Answer is one typed answer. The interface is sealed: [PredicateAnswer],
// [ChoiceAnswer], [ScoreAnswer], [RefusalAnswer] and [UnknownAnswer] are the only
// implementations, so a type switch over an Answer is exhaustive once it handles
// those five.
type Answer interface {
	// AnswerType names the answer's type, which matches the type of the
	// question that asked for it unless the model declined or the server
	// answered with a type this version does not model.
	AnswerType() QuestionType
	// MarshalJSONTo writes the answer in this package's own JSON form, the one
	// a conversational model reads in a tool result.
	MarshalJSONTo(enc *jsontext.Encoder) error
	// isAnswer seals the interface.
	isAnswer()
}

// AnswerError is an answer that could not be read as the type asked for: the id
// is not in the set at all, the model declined that question, or it carries
// another type. It is always used as a pointer, so errors.As reaches it.
type AnswerError struct {
	// ID is the question id that was read.
	ID string
	// Want is the type the accessor asked for.
	Want QuestionType
	// Got is the type the set carried: empty when there was no answer under
	// this id, and [TypeRefusal] when the model declined the question.
	Got QuestionType
}

// Error names the id and both types, because a caller holding twenty answers
// has nothing else to go on.
func (e *AnswerError) Error() string {
	switch e.Got {
	case "":
		return fmt.Sprintf("decision: no answer for question %q, so there is no %s to read",
			e.ID, e.Want)
	case TypeRefusal:
		return fmt.Sprintf("decision: the model declined question %q, so there is no %s to read",
			e.ID, e.Want)
	}
	return fmt.Sprintf("decision: question %q was answered as %s, not as %s",
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

// Predicate reads the answer to a [Predicate] question. A missing id, a declined
// question or an answer of another type is an *[AnswerError] naming the id.
func (a *Answers) Predicate(id string) (PredicateAnswer, error) {
	return answerAs[PredicateAnswer](a, id, TypePredicate)
}

// Choice reads the answer to a [Choice] question. A missing id, a declined
// question or an answer of another type is an *[AnswerError] naming the id.
func (a *Answers) Choice(id string) (ChoiceAnswer, error) {
	return answerAs[ChoiceAnswer](a, id, TypeChoice)
}

// Score reads the answer to a [Score] question. A missing id, a declined
// question or an answer of another type is an *[AnswerError] naming the id.
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
