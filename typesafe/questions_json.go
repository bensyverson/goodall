package typesafe

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
)

// The decode side of a question list. The encode side lives in questions.go and
// is written by hand, because member order is part of the request contract;
// reading a question list back is what the authored-questions tool needs, so
// the two halves are symmetrical.

// UnmarshalJSONFrom reads a questions object back into the slice, keeping the
// order the members were written in and dispatching each one on its type
// member. A type this package does not know is an error rather than a skipped
// question: a request built from questions that were quietly dropped would ask
// something other than what it says it asks.
func (q *Questions) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("typesafe: questions must be a JSON object, got %v", tok.Kind())
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
			return fmt.Errorf("typesafe: question %q: %w", id, err)
		}
		out = append(out, NamedQuestion{ID: id, Question: question})
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	*q = out
	return nil
}

// wireNoul, wireChoice and wireScore are the decode-side shapes. They exist
// only so a question read back from JSON can be rebuilt as the ordered Go
// value; the encode side is written by hand, since member order is part of the
// contract.
type wireNoul struct {
	Instructions string `json:"instructions"`
	Criteria     *struct {
		True  string `json:"true,omitzero"`
		False string `json:"false,omitzero"`
	} `json:"criteria,omitzero"`
}

// wireChoice is a choice question as read back. Its criteria decode in
// document order, which a map-typed field could not do.
type wireChoice struct {
	Instructions string         `json:"instructions"`
	Criteria     orderedStrings `json:"criteria,omitzero"`
}

// wireScore is a score question as read back.
type wireScore struct {
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria,omitzero"`
}

// wireQuestionType reads just the type member, which is what decides how the
// rest of the object is read.
type wireQuestionType struct {
	Type QuestionType `json:"type"`
}

// unmarshalQuestion rebuilds one question from its JSON object.
func unmarshalQuestion(raw jsontext.Value) (Question, error) {
	var probe wireQuestionType
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case TypeNoul:
		var w wireNoul
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		n := Noul{Instructions: w.Instructions}
		if w.Criteria != nil {
			n.True, n.False = w.Criteria.True, w.Criteria.False
		}
		return n, nil
	case TypeChoice:
		var w wireChoice
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		options := make([]Option, 0, len(w.Criteria))
		for _, entry := range w.Criteria {
			options = append(options, Option{Key: entry.Key, Description: entry.Value})
		}
		return Choice{Instructions: w.Instructions, Options: options}, nil
	case TypeScore:
		var w wireScore
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, err
		}
		return Score{Instructions: w.Instructions, Levels: w.Criteria}, nil
	case "":
		return nil, errors.New("has no type member")
	}
	return nil, fmt.Errorf("has the unknown type %q; this version knows %s, %s and %s",
		probe.Type, TypeNoul, TypeChoice, TypeScore)
}

// orderedString is one member of a JSON object of strings, with the member
// name kept.
type orderedString struct {
	// Key is the member name.
	Key string
	// Value is the member's string value; a null member reads as empty.
	Value string
}

// orderedStrings decodes a JSON object of strings — or nulls — preserving the
// order the members arrived in.
type orderedStrings []orderedString

// UnmarshalJSONFrom reads the object member by member.
func (o *orderedStrings) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("typesafe: expected a JSON object, got %v", tok.Kind())
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
