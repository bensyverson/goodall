package systemone

import (
	"errors"
	"strings"
	"testing"
)

// TestErrorMessageReadsEveryShapeServersSend covers the bodies servers on this
// wire are documented to send. The shapes are the servers' own (read 2026-11-02);
// where a server's docs give a shape with no values, the sentence is authored.
func TestErrorMessageReadsEveryShapeServersSend(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"Ollama's string": {
			`{"error": "text and schema must not exceed 64 KiB"}`,
			"text and schema must not exceed 64 KiB",
		},
		"LocalAI's nested object": {
			`{"error": {"message": "questions must not be empty", "type": "invalid_request"}}`,
			"questions must not be empty",
		},
		"a nested detail": {
			`{"error": {"detail": "the model is still loading"}}`,
			"the model is still loading",
		},
		"ollajev's detail list": {
			`{"detail": [{"loc": ["body", "questions", "a", "type"], "msg": "unknown question type", "type": "value_error"}]}`,
			"body.questions.a.type: unknown question type",
		},
		"a detail list naming an array index": {
			`{"detail": [{"loc": ["body", 2], "msg": "bad item", "type": "value_error"}]}`,
			"body.2: bad item",
		},
		"a detail list naming several": {
			`{"detail": [{"loc": ["a"], "msg": "one"}, {"msg": "two"}]}`,
			"a: one; two",
		},
		"a detail string": {
			`{"detail": "Not Found"}`,
			"Not Found",
		},
		"a plain message": {
			`{"message": "no model is loaded"}`,
			"no model is loaded",
		},
		"nothing recognized": {`{"model": "m"}`, ""},
		"an empty object":    {`{}`, ""},
		"an empty string":    {`{"error": ""}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ErrorMessage([]byte(tc.body)); got != tc.want {
				t.Errorf("ErrorMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestErrorMessageKeepsABodyItCannotParseAsTheMessage: a gateway's text is the
// best sentence there is, and it stays bounded so it prints in a log line.
func TestErrorMessageKeepsABodyItCannotParseAsTheMessage(t *testing.T) {
	if got := ErrorMessage([]byte("  upstream timed out\n")); got != "upstream timed out" {
		t.Errorf("ErrorMessage = %q", got)
	}
	long := ErrorMessage([]byte(strings.Repeat("x", 5000)))
	if !strings.HasSuffix(long, "…") || len(long) > 1100 {
		t.Errorf("a long body came back at %d bytes, unbounded", len(long))
	}
}

// TestDecodeAnswersRefusesABodyWithNoAnswers: every request asks at least one
// question, so a success body with nothing in it is a server that did not answer,
// and an empty set would read as a model with nothing to say.
func TestDecodeAnswersRefusesABodyWithNoAnswers(t *testing.T) {
	for name, body := range map[string]string{
		"an empty object":   `{}`,
		"no answers member": `{"model":"m","usage":{"input_tokens":1}}`,
		"an empty set":      `{"model":"m","answers":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeAnswers([]byte(body))
			if !errors.Is(err, ErrNoAnswers) {
				t.Errorf("error = %v, want ErrNoAnswers", err)
			}
		})
	}
}

// TestAScoreWithNoLegendDecodes is ollajev's documented example, quoted: the
// levels are numbered and named nowhere in the answer.
func TestAScoreWithNoLegendDecodes(t *testing.T) {
	answers, err := DecodeAnswers([]byte(`{"model":"m","answers":{"urgency": {"type": "score", "score": 0.9105, "confidence": 0.3798,
		"probabilities": {"0": 0.2515, "1": 0.5865, "2": 0.162}}}}`))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	score, err := answers.Score("urgency")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score != 0.9105 || score.Confidence != 0.3798 || score.Legend != nil {
		t.Errorf("Score = %+v", score)
	}
	if score.Probabilities["1"] != 0.5865 {
		t.Errorf("Probabilities = %v", score.Probabilities)
	}
}

// TestANoulWithAnExtraConfidenceDecodes: the member has no meaning that holds
// across servers, so it is ignored rather than refused.
func TestANoulWithAnExtraConfidenceDecodes(t *testing.T) {
	answers, err := DecodeAnswers([]byte(`{"model":"m","answers":{"is_urgent":{"type":"` + WirePredicate + `","` + WirePredicate + `":0.25,"confidence":0.5}}}`))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.25 {
		t.Errorf("Probability = %v", predicate.Probability)
	}
}
