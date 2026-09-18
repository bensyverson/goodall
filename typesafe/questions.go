package typesafe

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
)

// QuestionType names one of the three shapes a Jev question takes. It is the
// "type" member on the wire, and the answer to a question carries the same
// value, which is what lets a mismatched accessor say what it found.
type QuestionType string

const (
	// TypeNoul is a yes-or-no question, answered with the probability that
	// the answer is yes.
	TypeNoul QuestionType = "noul"
	// TypeChoice is a pick-one question over a named set of options,
	// answered with the chosen option and the whole distribution.
	TypeChoice QuestionType = "choice"
	// TypeScore is a rating against an ordered rubric, answered with a
	// probability-weighted level that may fall between two of them.
	TypeScore QuestionType = "score"
)

// String names the type as it appears on the wire.
func (t QuestionType) String() string { return string(t) }

// Question is one typed question in a request. The interface is sealed —
// [Noul], [Choice] and [Score] are the only implementations, because they are
// the only ones the API has — so a type switch over a Question is exhaustive.
//
// Instructions and option descriptions are strings. The API also accepts JSON
// structure in either position; goodall sends strings only, which is what
// every documented example uses, and a structured form would be a new field
// rather than a change to these.
type Question interface {
	// QuestionType names this question's wire type.
	QuestionType() QuestionType
	// MarshalJSONTo writes the question as a JSON object whose members are
	// in the order the API reference lists them, so two identical requests
	// marshal to identical bytes.
	MarshalJSONTo(enc *jsontext.Encoder) error
	// validate reports, in words that complete "question %q …", what would
	// make the API refuse this question. Being unexported it also seals the
	// interface.
	validate() error
}

// Noul is a yes-or-no question. The answer is the probability that the answer
// is yes, so 0.5 means "yes and no are equally likely" rather than "medium",
// and a noul answer carries no confidence figure at all.
type Noul struct {
	// Instructions is the yes-or-no question to evaluate, such as "Does
	// this convey urgency?".
	Instructions string
	// True describes what a yes — a value near 1 — means. Optional.
	True string
	// False describes what a no — a value near 0 — means. Optional.
	False string
}

// QuestionType names this question's wire type.
func (Noul) QuestionType() QuestionType { return TypeNoul }

// MarshalJSONTo writes the noul question, with its criteria object only when
// one of the two descriptions was given.
func (n Noul) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openQuestion(enc, TypeNoul, n.Instructions); err != nil {
		return err
	}
	if n.True != "" || n.False != "" {
		if err := enc.WriteToken(jsontext.String(criteriaMember)); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if n.True != "" {
			if err := writeStringMember(enc, "true", n.True); err != nil {
				return err
			}
		}
		if n.False != "" {
			if err := writeStringMember(enc, "false", n.False); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// validate reports a noul question the API would refuse.
func (n Noul) validate() error {
	if n.Instructions == "" {
		return errors.New("has no instructions")
	}
	return nil
}

// Option is one named choice in a [Choice] question. Options hold their key
// because they live in a slice: the wire shape is an object, a map would sort
// them, and the order is the author's — goodall sends it exactly as written,
// because two identical requests must marshal to identical bytes.
//
// That order is part of the question where the call is close. Measured against
// jev-1.13.0 on 2026-09-17 by scripts/probe-option-order, three samples per
// order: on a ticket whose department was genuinely ambiguous, reversing three
// options moved the leading option from 0.85–0.86 to 0.72–0.78 — a cross-order
// spread of 0.14 against a same-order spread of 0.06, each time favoring
// whichever option was listed first — while on a ticket with an obvious answer
// both orders returned the same distribution. So on a close call read
// [ChoiceAnswer.Probabilities] rather than the pick, and keep the order fixed
// across runs that are meant to be comparable.
type Option struct {
	// Key is the option's name, which is what a [ChoiceAnswer] reports and
	// what its probabilities are keyed by.
	Key string
	// Description is the rubric for this option. An empty description is
	// sent as null, which is the API's way of saying "no extra detail".
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

// QuestionType names this question's wire type.
func (Choice) QuestionType() QuestionType { return TypeChoice }

// MarshalJSONTo writes the choice question, its options as the criteria
// object in slice order, an option with no description as null.
func (c Choice) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openQuestion(enc, TypeChoice, c.Instructions); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.String(criteriaMember)); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, opt := range c.Options {
		if err := enc.WriteToken(jsontext.String(opt.Key)); err != nil {
			return err
		}
		if opt.Description == "" {
			if err := enc.WriteToken(jsontext.Null); err != nil {
				return err
			}
			continue
		}
		if err := enc.WriteToken(jsontext.String(opt.Description)); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// validate reports a choice question the API would refuse. A repeated key is
// caught here rather than by the encoder, whose duplicate-member error names
// neither the question nor the option.
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

// QuestionType names this question's wire type.
func (Score) QuestionType() QuestionType { return TypeScore }

// MarshalJSONTo writes the score question, its levels as the criteria array in
// slice order.
func (s Score) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openQuestion(enc, TypeScore, s.Instructions); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.String(criteriaMember)); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for _, level := range s.Levels {
		if err := enc.WriteToken(jsontext.String(level)); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// validate reports a score question the API would refuse.
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

// Questions is the ordered question list of one request. It marshals as the
// request's questions object in slice order, which is what makes two identical
// requests identical bytes; a map would sort them and would make the order the
// author chose unrecoverable.
type Questions []NamedQuestion

// MarshalJSONTo writes the questions as a JSON object, in slice order.
func (q Questions) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, nq := range q {
		if err := enc.WriteToken(jsontext.String(nq.ID)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, nq.Question); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// validate reports the first thing about this list the API would refuse,
// before any call is made. Every message names the question it is about,
// because a request carrying twenty questions gives a caller nothing else to
// go on.
func (q Questions) validate() error {
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

// The member names shared by every question and answer shape.
const (
	typeMember         = "type"
	instructionsMember = "instructions"
	criteriaMember     = "criteria"
)

// openQuestion writes the object opening and the two members every question
// shares, leaving the encoder inside the object for the criteria.
func openQuestion(enc *jsontext.Encoder, t QuestionType, instructions string) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeStringMember(enc, typeMember, string(t)); err != nil {
		return err
	}
	return writeStringMember(enc, instructionsMember, instructions)
}

// writeStringMember writes one member whose value is a JSON string.
func writeStringMember(enc *jsontext.Encoder, name, value string) error {
	if err := enc.WriteToken(jsontext.String(name)); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.String(value))
}
