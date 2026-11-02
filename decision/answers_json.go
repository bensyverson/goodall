package decision

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"

	"github.com/bensyverson/goodall"
)

// The JSON form of an answer set, which is what a conversational model reads in a
// tool result and what a consumer watching the run decodes back. It belongs to
// this package and no server: a codec translates a wire into these types, and
// nothing about a wire's spelling reaches here.

// Member names of the JSON form.
const (
	memberType          = "type"
	memberProbability   = "probability"
	memberChoice        = "choice"
	memberScore         = "score"
	memberConfidence    = "confidence"
	memberLegend        = "legend"
	memberProbabilities = "probabilities"
	memberNote          = "note"
	memberModel         = "model"
	memberAnswers       = "answers"
	memberUsage         = "usage"
	memberCost          = "cost"
)

// refusalNote is what a model reads where an answer would be. A bare type member
// would leave it to work out whether the whole judgment failed, so the note says
// the part it has to act on: this one has no answer and the rest are good.
const refusalNote = "The judge declined this question, so there is no answer to act on here. The other answers in this result stand."

// MarshalJSONTo writes the answer as an object naming its type and its
// probability.
func (p PredicateAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openAnswer(enc, TypePredicate); err != nil {
		return err
	}
	if err := writeFloatMember(enc, memberProbability, p.Probability); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// MarshalJSONTo writes the answer as an object naming its type, the pick, the
// confidence and the distribution with its most likely option first.
func (c ChoiceAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openAnswer(enc, TypeChoice); err != nil {
		return err
	}
	if err := writeStringMember(enc, memberChoice, c.Choice); err != nil {
		return err
	}
	if err := writeFloatMember(enc, memberConfidence, c.Confidence); err != nil {
		return err
	}
	if err := writeProbabilities(enc, c.Probabilities); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// MarshalJSONTo writes the answer as an object naming its type, the level, the
// confidence, the legend and the distribution with its most likely level first.
func (s ScoreAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openAnswer(enc, TypeScore); err != nil {
		return err
	}
	if err := writeFloatMember(enc, memberScore, s.Score); err != nil {
		return err
	}
	if err := writeFloatMember(enc, memberConfidence, s.Confidence); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.String(memberLegend)); err != nil {
		return err
	}
	if err := json.MarshalEncode(enc, s.Legend, json.Deterministic(true)); err != nil {
		return err
	}
	if err := writeProbabilities(enc, s.Probabilities); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// MarshalJSONTo writes the refusal as an object naming its type and telling the
// reader what it means for the rest of the set.
func (RefusalAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openAnswer(enc, TypeRefusal); err != nil {
		return err
	}
	if err := writeStringMember(enc, memberNote, refusalNote); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// MarshalJSONTo writes the answer's own bytes back out, unchanged.
func (u UnknownAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	return enc.WriteValue(u.Raw)
}

// MarshalJSONTo writes the set as one object: the model, the answers keyed by id
// in the order they arrived, the usage, and the cost when there is one to state.
// The form is this package's own, which [Answers.UnmarshalJSONFrom] reads back:
//
//	{"model":"…","answers":{
//	  "is_urgent":{"type":"predicate","probability":0.96},
//	  "department":{"type":"choice","choice":"billing","confidence":0.96,"probabilities":{"billing":0.97,"other":0.03}},
//	  "anger":{"type":"score","score":1.2,"confidence":0.8,"legend":{"0":"Calm","1":"Furious"},"probabilities":{"1":0.8,"0":0.2}},
//	  "declined":{"type":"refusal","note":"…"}
//	},"usage":{"input_tokens":12,"output_tokens":3}}
func (a *Answers) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeStringMember(enc, memberModel, a.Model); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.String(memberAnswers)); err != nil {
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
	if err := enc.WriteToken(jsontext.String(memberUsage)); err != nil {
		return err
	}
	if err := json.MarshalEncode(enc, a.Usage); err != nil {
		return err
	}
	// The zero cost means "not known" and is left out, because a member with
	// a zero amount in front of a model would read as free.
	if a.Cost != (goodall.Cost{}) {
		if err := enc.WriteToken(jsontext.String(memberCost)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, a.Cost); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// UnmarshalJSONFrom reads the form [Answers.MarshalJSONTo] writes. The answers
// object is read member by member so their order, the order the questions were
// asked in, survives, which a map-typed field could not do.
func (a *Answers) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var w struct {
		Model   string        `json:"model"`
		Answers namedAnswers  `json:"answers,omitzero"`
		Usage   goodall.Usage `json:"usage,omitzero"`
		Cost    goodall.Cost  `json:"cost,omitzero"`
	}
	if err := json.UnmarshalDecode(dec, &w); err != nil {
		return err
	}
	a.Model, a.Usage, a.Cost, a.entries = w.Model, w.Usage, w.Cost, w.Answers
	return nil
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
		return fmt.Errorf("decision: answers must be a JSON object, got %v", tok.Kind())
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
			return fmt.Errorf("decision: answer %q: %w", id, err)
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
// does not model becomes an [UnknownAnswer] rather than an error, since dropping
// it would lose a judgment that was paid for.
func unmarshalAnswer(raw jsontext.Value) (Answer, error) {
	var probe struct {
		Type QuestionType `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case TypePredicate:
		return decodeAs[PredicateAnswer](raw)
	case TypeChoice:
		return decodeAs[ChoiceAnswer](raw)
	case TypeScore:
		return decodeAs[ScoreAnswer](raw)
	case TypeRefusal:
		return RefusalAnswer{}, nil
	}
	return UnknownAnswer{Type: probe.Type, Raw: raw}, nil
}

// decodeAs is the one body the modeled answer types share.
func decodeAs[T Answer](raw jsontext.Value) (Answer, error) {
	var answer T
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// openAnswer writes the object opening and the type member every answer shares.
func openAnswer(enc *jsontext.Encoder, t QuestionType) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	return writeStringMember(enc, memberType, string(t))
}

// writeStringMember writes one member whose value is a JSON string.
func writeStringMember(enc *jsontext.Encoder, name, value string) error {
	if err := enc.WriteToken(jsontext.String(name)); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.String(value))
}

// writeFloatMember writes one member whose value is a JSON number.
func writeFloatMember(enc *jsontext.Encoder, name string, value float64) error {
	if err := enc.WriteToken(jsontext.String(name)); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.Float(value))
}

// writeProbabilities writes the probabilities member in rank order, so that two
// encodings of one answer are the same bytes and a reader meets the most likely
// outcome first.
func writeProbabilities(enc *jsontext.Encoder, probabilities map[string]float64) error {
	if err := enc.WriteToken(jsontext.String(memberProbabilities)); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, outcome := range rank(probabilities) {
		if err := writeFloatMember(enc, outcome.Key, outcome.Probability); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}
