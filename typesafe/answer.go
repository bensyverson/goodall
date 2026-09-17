package typesafe

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"slices"
	"strconv"
)

// The four answer shapes, one per question type plus the unknown. The set they
// arrive in, and the accessors that read it, are in answers.go.

// NoulAnswer is the answer to a [Noul]: the probability that the answer is yes.
// There is no confidence figure, because there is nothing a distribution over
// two outcomes would say that the probability does not — 0.5 means "yes and no
// are equally likely", not "medium".
type NoulAnswer struct {
	// Noul is the probability of yes, from 0 (no) to 1 (yes).
	Noul float64 `json:"noul"`
}

// AnswerType names this answer's wire type.
func (NoulAnswer) AnswerType() QuestionType { return TypeNoul }

func (NoulAnswer) isAnswer() {}

// Yes reports whether the probability of yes is at least threshold. It is a
// method rather than a comparison at each call site because a threshold is the
// one number in a judgment pipeline worth keeping in one reviewable place.
func (n NoulAnswer) Yes(threshold float64) bool { return n.Noul >= threshold }

// MarshalJSONTo writes the answer as the API sends it.
func (n NoulAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openAnswer(enc, TypeNoul); err != nil {
		return err
	}
	if err := writeFloatMember(enc, "noul", n.Noul); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// ChoiceAnswer is the answer to a [Choice]: the most probable option, the whole
// distribution and a confidence derived from it.
//
// Confidence is a statistic of Probabilities and nothing more — concentrated
// means high, spread means low. It says nothing about whether this particular
// answer is right, and it is calibrated per model version, so a threshold tuned
// against one version belongs with a pinned [WithModel].
type ChoiceAnswer struct {
	// Choice is the option with the highest probability.
	Choice string `json:"choice"`
	// Probabilities is every option's probability; they sum to 1. It is a
	// map because it is read rather than sent, so no order is lost; for the
	// options in rank order call Ranked.
	Probabilities map[string]float64 `json:"probabilities,omitzero"`
	// Confidence is how concentrated Probabilities is, from 0 to 1.
	Confidence float64 `json:"confidence"`
}

// AnswerType names this answer's wire type.
func (ChoiceAnswer) AnswerType() QuestionType { return TypeChoice }

func (ChoiceAnswer) isAnswer() {}

// Ranked lists the options by descending probability, ties broken by option
// name so that two identical answers rank identically.
func (c ChoiceAnswer) Ranked() []Outcome { return rank(c.Probabilities) }

// MarshalJSONTo writes the answer as the API sends it.
func (c ChoiceAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openAnswer(enc, TypeChoice); err != nil {
		return err
	}
	if err := writeStringMember(enc, "choice", c.Choice); err != nil {
		return err
	}
	if err := writeFloatMember(enc, "confidence", c.Confidence); err != nil {
		return err
	}
	if err := writeProbabilities(enc, c.Probabilities); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// ScoreAnswer is the answer to a [Score]: a probability-weighted position
// across the levels, which may land between two of them.
//
// Score is a weighted average, so 2.12 over the levels "Calm", "Mildly
// annoyed", "Frustrated" and "Furious" means "Frustrated, edging toward
// Furious" rather than "level 2 with certainty". Confidence is a statistic of
// Probabilities, as on a [ChoiceAnswer].
type ScoreAnswer struct {
	// Score is the probability-weighted level, from 0 to one less than the
	// number of levels.
	Score float64 `json:"score"`
	// Legend maps each level index, as a decimal string, back to the
	// description the question gave it. For the descriptions in level order
	// call Levels.
	Legend map[string]string `json:"legend,omitzero"`
	// Probabilities maps each level index, as a decimal string, to its
	// probability; they sum to 1.
	Probabilities map[string]float64 `json:"probabilities,omitzero"`
	// Confidence is how concentrated Probabilities is, from 0 to 1.
	Confidence float64 `json:"confidence"`
}

// AnswerType names this answer's wire type.
func (ScoreAnswer) AnswerType() QuestionType { return TypeScore }

func (ScoreAnswer) isAnswer() {}

// Levels lists the level descriptions in level order, which is the order the
// question sent them in. A legend with a gap or a non-numeric key yields
// whatever contiguous run starts at zero, since a caller indexing by level
// cannot use the rest.
func (s ScoreAnswer) Levels() []string {
	out := make([]string, 0, len(s.Legend))
	for i := range len(s.Legend) {
		level, ok := s.Legend[strconv.Itoa(i)]
		if !ok {
			break
		}
		out = append(out, level)
	}
	return out
}

// Ranked lists the levels by descending probability, keyed by level index and
// ties broken by that index. Read the description of a key out of Legend.
func (s ScoreAnswer) Ranked() []Outcome { return rank(s.Probabilities) }

// MarshalJSONTo writes the answer as the API sends it.
func (s ScoreAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := openAnswer(enc, TypeScore); err != nil {
		return err
	}
	if err := writeFloatMember(enc, "score", s.Score); err != nil {
		return err
	}
	if err := writeFloatMember(enc, "confidence", s.Confidence); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.String("legend")); err != nil {
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

// UnknownAnswer is an answer whose type this version of goodall does not model,
// kept whole rather than dropped (invariant 6). The API already has a fourth
// question type that is not in its published reference — a 422 names
// bounding_box among the tags it expects — so this is a live case: a request
// that somehow asks for one gets its answer here, with the bytes intact.
type UnknownAnswer struct {
	// Type is the type the API reported.
	Type QuestionType
	// Raw is the answer object as received.
	Raw jsontext.Value
}

// AnswerType names the type the API reported.
func (u UnknownAnswer) AnswerType() QuestionType { return u.Type }

func (UnknownAnswer) isAnswer() {}

// MarshalJSONTo writes the answer's own bytes back out, unchanged.
func (u UnknownAnswer) MarshalJSONTo(enc *jsontext.Encoder) error {
	return enc.WriteValue(u.Raw)
}

// Outcome is one option or level with its probability, as [ChoiceAnswer.Ranked]
// and [ScoreAnswer.Ranked] return them.
type Outcome struct {
	// Key is the option name, or the level index as a decimal string.
	Key string
	// Probability is that outcome's probability.
	Probability float64
}

// rank sorts a probability distribution by descending probability, ties broken
// by key so the order is a function of the answer alone.
func rank(probabilities map[string]float64) []Outcome {
	out := make([]Outcome, 0, len(probabilities))
	for key, probability := range probabilities {
		out = append(out, Outcome{Key: key, Probability: probability})
	}
	slices.SortFunc(out, func(a, b Outcome) int {
		if a.Probability != b.Probability {
			if a.Probability > b.Probability {
				return -1
			}
			return 1
		}
		return cmpStrings(a.Key, b.Key)
	})
	return out
}

// cmpStrings orders two keys, numerically where both are level indices so that
// level 10 does not sort before level 2.
func cmpStrings(a, b string) int {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		}
		return 0
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// openAnswer writes the object opening and the type member every answer shares.
func openAnswer(enc *jsontext.Encoder, t QuestionType) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	return writeStringMember(enc, typeMember, string(t))
}

// writeFloatMember writes one member whose value is a JSON number.
func writeFloatMember(enc *jsontext.Encoder, name string, value float64) error {
	if err := enc.WriteToken(jsontext.String(name)); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.Float(value))
}

// writeProbabilities writes the probabilities member, in key order so that two
// encodings of one answer are the same bytes.
func writeProbabilities(enc *jsontext.Encoder, probabilities map[string]float64) error {
	if err := enc.WriteToken(jsontext.String("probabilities")); err != nil {
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
