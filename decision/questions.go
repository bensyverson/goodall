package decision

import (
	"errors"
	"fmt"

	"github.com/bensyverson/goodall"
)

// QuestionType names one of the shapes a question takes. The answer to a
// question carries the same value, which is what lets a mismatched accessor say
// what it found.
type QuestionType string

const (
	// TypePredicate is a yes-or-no question, answered with the probability
	// that the answer is yes.
	TypePredicate QuestionType = "predicate"
	// TypeChoice is a pick-one question over a named set of options,
	// answered with the chosen option and the whole distribution.
	TypeChoice QuestionType = "choice"
	// TypeScore is a rating against an ordered rubric, answered with a
	// probability-weighted level that may fall between two of them.
	TypeScore QuestionType = "score"
	// TypeRefusal is the type of a [RefusalAnswer]. It names no question
	// anyone can ask: any question may come back as one instead of its own
	// type, when the model declines to answer it.
	TypeRefusal QuestionType = "refusal"
)

// String names the type.
func (t QuestionType) String() string { return string(t) }

// Question is one typed question in a request. The interface is sealed:
// [Predicate], [Choice] and [Score] are the only implementations, so a type
// switch over a Question is exhaustive, and a codec turning one into a wire
// format has no fourth case to miss.
//
// Instructions and option descriptions are strings. A structured form would be
// a new field rather than a change to these.
type Question interface {
	// QuestionType names this question's type.
	QuestionType() QuestionType
	// validate reports, in words that complete "question %q …", what would
	// make this question unanswerable. Being unexported it also seals the
	// interface.
	validate() error
}

// Predicate is a yes-or-no question. The answer is the probability that the
// answer is yes, so 0.5 means "yes and no are equally likely" rather than
// "medium", and a predicate answer carries no confidence figure at all.
type Predicate struct {
	// Instructions is the yes-or-no question to evaluate, such as "Does
	// this convey urgency?".
	Instructions string
	// True describes what a yes, a value near 1, means. Optional.
	True string
	// False describes what a no, a value near 0, means. Optional.
	False string
}

// QuestionType names this question's type.
func (Predicate) QuestionType() QuestionType { return TypePredicate }

// validate reports a predicate that could not be answered.
func (p Predicate) validate() error {
	if p.Instructions == "" {
		return errors.New("has no instructions")
	}
	return nil
}

// Option is one named choice in a [Choice] question. Options hold their key
// because they live in a slice: a map would sort them, and the order is the
// author's. goodall sends it exactly as written, because two identical requests
// must marshal to identical bytes.
//
// That order is part of the question where the call is close. Measured on
// 2026-09-17 by scripts/probe-option-order on one model, three samples per order,
// so another model may differ: on a ticket
// whose department was genuinely ambiguous, reversing three options moved the
// leading option from 0.85–0.86 to 0.72–0.78, a cross-order spread of 0.14
// against a same-order spread of 0.06, each time favoring whichever option was
// listed first, while on a ticket with an obvious answer both orders returned
// the same distribution. So on a close call read [ChoiceAnswer.Probabilities]
// rather than the pick, and keep the order fixed across runs that are meant to
// be comparable.
type Option struct {
	// Key is the option's name, which is what a [ChoiceAnswer] reports and
	// what its probabilities are keyed by.
	Key string
	// Description is the rubric for this option. Empty means "no extra
	// detail".
	Description string
}

// Choice is a pick-one question over a named set of options. The answer is the
// most probable option together with the whole distribution, so a caller can
// threshold on the margin rather than trusting the pick.
type Choice struct {
	// Instructions is what the model should decide, such as "Which team
	// should handle this?".
	Instructions string
	// Options are the options to choose between, in the order they are
	// sent. At least one is required and the keys must be distinct.
	Options []Option
}

// QuestionType names this question's type.
func (Choice) QuestionType() QuestionType { return TypeChoice }

// validate reports a choice that could not be answered. A repeated key is
// caught here rather than by a codec's encoder, whose duplicate-member error
// would name neither the question nor the option.
func (c Choice) validate() error {
	if c.Instructions == "" {
		return errors.New("has no instructions")
	}
	if len(c.Options) == 0 {
		return errors.New("needs at least one option")
	}
	seen := make(map[string]bool, len(c.Options))
	for i, opt := range c.Options {
		if opt.Key == "" {
			return fmt.Errorf("has an option with no key at index %d", i)
		}
		if seen[opt.Key] {
			return fmt.Errorf("repeats the option %q", opt.Key)
		}
		seen[opt.Key] = true
	}
	return nil
}

// Score is a rating against an ordered rubric. The answer is a
// probability-weighted position across the levels, so it may land between two
// of them: with levels "Calm", "Frustrated" and "Very angry", 1.6 is between
// the second and the third.
type Score struct {
	// Instructions is what the model should rate, such as "How frustrated
	// is the customer?".
	Instructions string
	// Levels describes each level of the rubric, in order from lowest to
	// highest. At least two are required; their indices are the keys of the
	// answer's legend and probabilities.
	Levels []string
}

// QuestionType names this question's type.
func (Score) QuestionType() QuestionType { return TypeScore }

// validate reports a score that could not be answered.
func (s Score) validate() error {
	if s.Instructions == "" {
		return errors.New("has no instructions")
	}
	if len(s.Levels) < 2 {
		return errors.New("needs at least two levels")
	}
	for i, level := range s.Levels {
		if level == "" {
			return fmt.Errorf("has an empty level description at index %d", i)
		}
	}
	return nil
}

// NamedQuestion is one question and the id its answer comes back under. The id
// is the caller's: it is not shown to the model and takes no part in the
// judgment.
type NamedQuestion struct {
	// ID is the key this question is sent under and its answer returned
	// under. It must be non-empty and distinct within a request.
	ID string
	// Question is the question itself.
	Question Question
}

// Questions is the ordered question list of one request. It is a slice because
// the order is the author's and a codec must be able to send it as written,
// which is what makes two identical requests identical bytes; a map would sort
// them and would make the order the author chose unrecoverable.
type Questions []NamedQuestion

// Validate reports the first thing about this list that could never be
// answered, so a caller learns it before any call is made. The error carries
// [goodall.KindInvalidRequest], and every message names the question it is
// about, because a request carrying twenty questions gives a caller nothing else
// to go on.
func (q Questions) Validate() error {
	if len(q) == 0 {
		return invalid("a request needs at least one question")
	}
	seen := make(map[string]bool, len(q))
	for i, nq := range q {
		if nq.ID == "" {
			return invalid("the question at index %d has no id", i)
		}
		if seen[nq.ID] {
			return invalid("two questions share the id %q", nq.ID)
		}
		seen[nq.ID] = true
		if nq.Question == nil {
			return invalid("question %q carries no question", nq.ID)
		}
		if err := nq.Question.validate(); err != nil {
			return invalid("question %q %s", nq.ID, err)
		}
	}
	return nil
}

// invalid builds the error a locally refused request comes back as. It wraps
// [goodall.KindInvalidRequest] so that errors.Is places it beside the refusal a
// server would have returned for the same mistake.
func invalid(format string, args ...any) error {
	return fmt.Errorf("decision: %w: "+format,
		append([]any{goodall.KindInvalidRequest}, args...)...)
}
