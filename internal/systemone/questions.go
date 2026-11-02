package systemone

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/bensyverson/goodall/decision"
)

// The member names every question shape shares, and the type values that are
// spelled the same on this wire as in the neutral package but are stated here so
// a later rename there cannot move what is sent or how it is read. The published
// schema has no refusal, and reading one costs nothing if a server sends it.
const (
	typeMember         = "type"
	instructionsMember = "instructions"
	criteriaMember     = "criteria"

	wireChoice = "choice"
	wireScore  = "score"

	wireRefusal = "refusal"
)

// Questions is a [decision.Questions] as this wire spells it: an object of
// questions keyed by id, in slice order. The encode side is written by hand,
// because member order is part of the request contract.
type Questions decision.Questions

// MarshalJSONTo writes the questions as a JSON object, in slice order.
func (q Questions) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, nq := range q {
		if err := enc.WriteToken(jsontext.String(nq.ID)); err != nil {
			return err
		}
		if err := writeQuestion(enc, nq); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// writeQuestion writes one question. The switch is exhaustive because
// [decision.Question] is sealed; what reaches the default is a nil question,
// which a validated request does not carry.
func writeQuestion(enc *jsontext.Encoder, nq decision.NamedQuestion) error {
	switch question := nq.Question.(type) {
	case decision.Predicate:
		return writePredicate(enc, question)
	case decision.Choice:
		return writeChoice(enc, question)
	case decision.Score:
		return writeScore(enc, question)
	}
	return fmt.Errorf("systemone: question %q carries %T, which this wire has no spelling for", nq.ID, nq.Question)
}

// writePredicate writes a predicate, with its criteria object only when one of
// the two descriptions was given.
func writePredicate(enc *jsontext.Encoder, p decision.Predicate) error {
	if err := openQuestion(enc, WirePredicate, p.Instructions); err != nil {
		return err
	}
	if p.True != "" || p.False != "" {
		if err := enc.WriteToken(jsontext.String(criteriaMember)); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if p.True != "" {
			if err := writeStringMember(enc, "true", p.True); err != nil {
				return err
			}
		}
		if p.False != "" {
			if err := writeStringMember(enc, "false", p.False); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// writeChoice writes a choice, its options as the criteria object in slice
// order, an option with no description as null, which is the wire's way of
// saying "no extra detail".
func writeChoice(enc *jsontext.Encoder, c decision.Choice) error {
	if err := openQuestion(enc, wireChoice, c.Instructions); err != nil {
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

// writeScore writes a score, its levels as the criteria array in slice order.
func writeScore(enc *jsontext.Encoder, s decision.Score) error {
	if err := openQuestion(enc, wireScore, s.Instructions); err != nil {
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

// openQuestion writes the object opening and the two members every question
// shares, leaving the encoder inside the object for the criteria.
func openQuestion(enc *jsontext.Encoder, spelling, instructions string) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeStringMember(enc, typeMember, spelling); err != nil {
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

// UnmarshalJSONFrom reads a questions object back into the slice, keeping the
// order the members were written in and dispatching each one on its type
// member. A type this wire does not have is an error rather than a skipped
// question: a request built from questions that were quietly dropped would ask
// something other than what it says it asks.
func (q *Questions) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("systemone: questions must be a JSON object, got %v", tok.Kind())
	}
	out := make(Questions, 0, len(*q))
	for dec.PeekKind() != '}' {
		nameTok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		// The token is voided by the next read, so the id is copied out
		// before the question's own value is decoded.
		id := nameTok.String()
		raw, err := dec.ReadValue()
		if err != nil {
			return err
		}
		question, err := unmarshalQuestion(raw)
		if err != nil {
			return fmt.Errorf("systemone: question %q: %w", id, err)
		}
		out = append(out, decision.NamedQuestion{ID: id, Question: question})
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	*q = out
	return nil
}

// wirePredicateQuestion, wireChoiceQuestion and wireScoreQuestion are the
// decode-side shapes. They exist only so a question read back from JSON can be
// rebuilt as the ordered Go value.
type wirePredicateQuestion struct {
	Instructions string `json:"instructions"`
	Criteria     *struct {
		True  string `json:"true,omitzero"`
		False string `json:"false,omitzero"`
	} `json:"criteria,omitzero"`
}

// wireChoiceQuestion is a choice as read back. Its criteria decode in document
// order, which a map-typed field could not do.
type wireChoiceQuestion struct {
	Instructions string         `json:"instructions"`
	Criteria     orderedStrings `json:"criteria,omitzero"`
}

// wireScoreQuestion is a score as read back.
type wireScoreQuestion struct {
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria,omitzero"`
}

// wireType reads just the type member, which is what decides how the rest of an
// object is read.
type wireType struct {
	Type string `json:"type"`
}

// unmarshalQuestion rebuilds one question from its JSON object.
func unmarshalQuestion(raw jsontext.Value) (decision.Question, error) {
	var probe wireType
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case WirePredicate:
		var w wirePredicateQuestion
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		p := decision.Predicate{Instructions: w.Instructions}
		if w.Criteria != nil {
			p.True, p.False = w.Criteria.True, w.Criteria.False
		}
		return p, nil
	case wireChoice:
		var w wireChoiceQuestion
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		options := make([]decision.Option, 0, len(w.Criteria))
		for _, entry := range w.Criteria {
			options = append(options, decision.Option{Key: entry.Key, Description: entry.Value})
		}
		return decision.Choice{Instructions: w.Instructions, Options: options}, nil
	case wireScore:
		var w wireScoreQuestion
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		return decision.Score{Instructions: w.Instructions, Levels: w.Criteria}, nil
	case "":
		return nil, errors.New("has no type member")
	}
	return nil, fmt.Errorf("has the unknown type %q; this wire knows %s, %s and %s",
		probe.Type, WirePredicate, wireChoice, wireScore)
}

// orderedString is one member of a JSON object of strings, with the member name
// kept.
type orderedString struct {
	// Key is the member name.
	Key string
	// Value is the member's string value; a null member reads as empty.
	Value string
}

// orderedStrings decodes a JSON object of strings or nulls, preserving the order
// the members arrived in.
type orderedStrings []orderedString

// UnmarshalJSONFrom reads the object member by member.
func (o *orderedStrings) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("systemone: expected a JSON object, got %v", tok.Kind())
	}
	out := make(orderedStrings, 0, len(*o))
	for dec.PeekKind() != '}' {
		nameTok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		key := nameTok.String()
		var value string
		if dec.PeekKind() == 'n' {
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
		} else if err := json.UnmarshalDecode(dec, &value); err != nil {
			return err
		}
		out = append(out, orderedString{Key: key, Value: value})
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	*o = out
	return nil
}
