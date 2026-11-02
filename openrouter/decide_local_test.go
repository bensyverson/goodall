package openrouter

import (
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// unroutedBody is what LM Studio 0.4.19 answered POST /v1/systemone with, as HTTP
// 200, observed 2026-11-02. It has the shape of an answer to nothing.
const unroutedBody = `{"error":"Unexpected endpoint or method. (POST /v1/systemone)"}`

// localSeen is what a local test server received.
type localSeen struct {
	authHeaders []string
	body        map[string]any
}

// localServer is a keyless client on a local dialect, built the way a consumer
// builds one, with no retries so a test sees the first answer.
func localServer(t *testing.T, dialect Dialect, status int, body string) (*Client, *localSeen) {
	t.Helper()
	seen := &localSeen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		seen.authHeaders = r.Header.Values("Authorization")
		seen.body = nil
		if err := json.Unmarshal(data, &seen.body); err != nil {
			t.Errorf("the request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(WithBaseURL(srv.URL), WithDialect(dialect), WithMaxRetries(-1)), seen
}

// localRequest asks with no model, which is how a local server is asked to use
// whichever one it has loaded.
func localRequest() *decision.Request {
	return &decision.Request{State: "Help!", Questions: decideQuestions()}
}

// TestDecideSurfacesEveryLocalServersSentence is the error bodies the local
// servers document or were seen sending, each on the status it comes with. The
// shapes are theirs, and the source of each sentence is named beside it.
func TestDecideSurfacesEveryLocalServersSentence(t *testing.T) {
	for name, tc := range map[string]struct {
		status   int
		body     string
		message  string
		wantKind goodall.ErrorKind
		wantType string
	}{
		"Ollama's string, quoted from its docs": {
			status:   http.StatusRequestEntityTooLarge,
			body:     `{"error": "text and schema must not exceed 64 KiB"}`,
			message:  "text and schema must not exceed 64 KiB",
			wantKind: goodall.KindInvalidRequest,
		},
		"LocalAI's nested form, the shape from its docs with an authored sentence": {
			status:   http.StatusBadRequest,
			body:     `{"error": {"message": "questions must not be empty", "type": "invalid_request"}}`,
			message:  "questions must not be empty",
			wantKind: goodall.KindInvalidRequest,
		},
		"ollajev's detail list, the shape from its docs with authored values": {
			status:   http.StatusUnprocessableEntity,
			body:     `{"detail": [{"loc": ["body", "questions", "a", "type"], "msg": "unknown question type", "type": "value_error"}]}`,
			message:  "body.questions.a.type: unknown question type",
			wantKind: goodall.KindInvalidRequest,
		},
		"LM Studio 0.4.28's validator, observed": {
			status:   http.StatusBadRequest,
			body:     `{"error":{"message":"- request.questions.a.type: Invalid discriminator value. Expected 'choice' | 'score' | 'noul'","type":"invalid_request_error","param":"questions.a.type","code":"invalid_value"}}`,
			message:  "- request.questions.a.type: Invalid discriminator value. Expected 'choice' | 'score' | 'noul'",
			wantKind: goodall.KindInvalidRequest,
			wantType: "invalid_value",
		},
		"a framework's string detail": {
			status:   http.StatusNotFound,
			body:     `{"detail":"Not Found"}`,
			message:  "Not Found",
			wantKind: goodall.KindNotFound,
		},
		"a plain message": {
			status:   http.StatusServiceUnavailable,
			body:     `{"message":"no model is loaded"}`,
			message:  "no model is loaded",
			wantKind: goodall.KindOverloaded,
		},
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := localServer(t, LMStudio, tc.status, tc.body)
			_, err := client.Decide(t.Context(), localRequest())
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
			}
			if apiErr.Message != tc.message {
				t.Errorf("Message = %q, want %q", apiErr.Message, tc.message)
			}
			if apiErr.Kind != tc.wantKind {
				t.Errorf("Kind = %s, want %s", apiErr.Kind, tc.wantKind)
			}
			if apiErr.Status != tc.status {
				t.Errorf("Status = %d, want %d", apiErr.Status, tc.status)
			}
			if apiErr.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", apiErr.Type, tc.wantType)
			}
			if apiErr.Provider != ProviderName {
				t.Errorf("Provider = %q", apiErr.Provider)
			}
			if string(apiErr.Raw) != tc.body {
				t.Errorf("Raw = %s, want the body verbatim", apiErr.Raw)
			}
		})
	}
}

// TestAnUnroutedPathIsAnErrorOnAnyStatus is LM Studio 0.4.19, which answers a path
// it does not route with a 200. Decoding that as an empty answer set would read as
// a model with nothing to say.
func TestAnUnroutedPathIsAnErrorOnAnyStatus(t *testing.T) {
	const sentence = "Unexpected endpoint or method. (POST /v1/systemone)"
	for name, tc := range map[string]struct {
		status, wantStatus int
		wantKind           goodall.ErrorKind
	}{
		"a 200, as observed": {http.StatusOK, 0, goodall.KindUnknown},
		"a 404":              {http.StatusNotFound, http.StatusNotFound, goodall.KindNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := localServer(t, LMStudio, tc.status, unroutedBody)
			answers, err := client.Decide(t.Context(), localRequest())
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v (answers %+v)", err, err, answers)
			}
			if apiErr.Message != sentence {
				t.Errorf("Message = %q, want %q", apiErr.Message, sentence)
			}
			if apiErr.Kind != tc.wantKind || apiErr.Status != tc.wantStatus {
				t.Errorf("Kind, Status = %s, %d, want %s, %d", apiErr.Kind, apiErr.Status, tc.wantKind, tc.wantStatus)
			}
		})
	}
}

// TestA200WithNoAnswersIsLoud: a success body with nothing in it is a server that
// did not answer. Where it says why, the sentence comes back as the server's
// error; where it says nothing, the error is one this client's caller can report.
func TestA200WithNoAnswersIsLoud(t *testing.T) {
	t.Run("saying nothing", func(t *testing.T) {
		for name, body := range map[string]string{
			"an empty object":   `{}`,
			"no answers member": `{"model":"m","usage":{"input_tokens":1}}`,
			"an empty set":      `{"model":"m","answers":{}}`,
			"a null set":        `{"model":"m","answers":null}`,
			"an empty error":    `{"error":""}`,
		} {
			t.Run(name, func(t *testing.T) {
				client, _ := localServer(t, Generic, http.StatusOK, body)
				answers, err := client.Decide(t.Context(), localRequest())
				protocolErr, ok := errors.AsType[*goodall.ProtocolError](err)
				if !ok {
					t.Fatalf("error is %T, want *goodall.ProtocolError: %v (answers %+v)", err, err, answers)
				}
				if !strings.Contains(protocolErr.Reason, "answers") {
					t.Errorf("Reason = %q, want one saying what was missing", protocolErr.Reason)
				}
			})
		}
	})
	t.Run("saying why", func(t *testing.T) {
		for name, body := range map[string]string{
			"a message": `{"message":"no model is loaded"}`,
			"a detail":  `{"detail":"no model is loaded"}`,
		} {
			t.Run(name, func(t *testing.T) {
				client, _ := localServer(t, Generic, http.StatusOK, body)
				_, err := client.Decide(t.Context(), localRequest())
				apiErr, ok := errors.AsType[*goodall.APIError](err)
				if !ok {
					t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
				}
				if apiErr.Message != "no model is loaded" || apiErr.Kind != goodall.KindUnknown {
					t.Errorf("APIError = %+v", apiErr)
				}
			})
		}
	})
}

// TestAnAnswerSetIsNotMistakenForAnError keeps the tolerance from reaching too
// far: a member that merely could carry a sentence does not undo real answers.
func TestAnAnswerSetIsNotMistakenForAnError(t *testing.T) {
	const answers = `"answers":{"is_urgent":{"type":"noul","noul":0.25}}`
	for name, body := range map[string]string{
		"a null error":   `{"error":null,` + answers + `}`,
		"an empty error": `{"error":"",` + answers + `}`,
		"a message":      `{"message":"ok",` + answers + `}`,
		"a detail":       `{"detail":"ok",` + answers + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := localServer(t, Generic, http.StatusOK, body)
			got, err := client.Decide(t.Context(), localRequest())
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if predicate, err := got.Predicate("is_urgent"); err != nil || predicate.Probability != 0.25 {
				t.Errorf("Predicate = %+v, %v", predicate, err)
			}
		})
	}
}

// TestAStringErrorBesideAnswersStillWins: a server that names a failure is
// believed over a body that also has answers, as with the object form.
func TestAStringErrorBesideAnswersStillWins(t *testing.T) {
	client, _ := localServer(t, Generic, http.StatusOK, `{"error":"it fell over","answers":{"a":{"type":"noul","noul":0.25}}}`)
	_, err := client.Decide(t.Context(), localRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok || apiErr.Message != "it fell over" {
		t.Errorf("error = %v, want the sentence on an APIError", err)
	}
}

// TestAKeylessLocalRequestNamesNoModelAndSendsNoCredential is what lets a local
// server answer with whichever model is loaded, for no key and no bill. The body
// is LM Studio 0.4.28's shape as observed: no cost and no generation id.
func TestAKeylessLocalRequestNamesNoModelAndSendsNoCredential(t *testing.T) {
	const body = `{"model":"julia-1","answers":{` +
		`"department":{"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.95,"technical":0.05}},` +
		`"is_urgent":{"type":"noul","noul":0.25,"confidence":0.5}},` +
		`"usage":{"input_tokens":192,"output_tokens":0}}`
	for _, dialect := range []Dialect{LMStudio, Generic} {
		t.Run(dialect.String(), func(t *testing.T) {
			client, seen := localServer(t, dialect, http.StatusOK, body)
			answers, err := client.Decide(t.Context(), localRequest())
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if model, present := seen.body["model"]; present {
				t.Errorf("model = %v, want the member absent", model)
			}
			if len(seen.authHeaders) != 0 {
				t.Errorf("Authorization = %q, want no header at all", seen.authHeaders)
			}
			if answers.Model != "julia-1" {
				t.Errorf("Model = %q, want the one the server named", answers.Model)
			}
			if answers.Cost.Reported {
				t.Errorf("Cost = %+v, want it unreported", answers.Cost)
			}
			if choice, err := answers.Choice("department"); err != nil || choice.Choice != "billing" {
				t.Errorf("Choice = %+v, %v", choice, err)
			}
			if predicate, err := answers.Predicate("is_urgent"); err != nil || predicate.Probability != 0.25 {
				t.Errorf("Predicate = %+v, %v", predicate, err)
			}
		})
	}
}

// TestALocalModelNameStillTravels: omission is for an empty model, and a caller
// who names one on a server with several loaded has to reach it.
func TestALocalModelNameStillTravels(t *testing.T) {
	client, seen := localServer(t, LMStudio, http.StatusOK, decideBody())
	req := localRequest()
	req.Model = "julia-1"
	if _, err := client.Decide(t.Context(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if seen.body["model"] != "julia-1" {
		t.Errorf("model = %v", seen.body["model"])
	}
}

// TestAScoreWithNoLegendReadsThroughDecide is ollajev's documented answer, quoted.
func TestAScoreWithNoLegendReadsThroughDecide(t *testing.T) {
	client, _ := localServer(t, Generic, http.StatusOK, `{"model":"m","answers":{"urgency": {"type": "score", "score": 0.9105, "confidence": 0.3798,
		"probabilities": {"0": 0.2515, "1": 0.5865, "2": 0.162}}}}`)
	answers, err := client.Decide(t.Context(), localRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	score, err := answers.Score("urgency")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score != 0.9105 || score.Legend != nil {
		t.Errorf("Score = %+v", score)
	}
}

// TestAStringErrorReachesAChatCaller: the conversational path reads the same error
// member, so a local server's sentence is not lost on it either.
func TestAStringErrorReachesAChatCaller(t *testing.T) {
	for name, status := range map[string]int{"inside a 200": http.StatusOK, "on a 400": http.StatusBadRequest} {
		t.Run(name, func(t *testing.T) {
			client, _ := localServer(t, LMStudio, status, `{"error":"the model is not loaded"}`)
			_, err := client.Complete(t.Context(), simpleRequest())
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
			}
			if apiErr.Message != "the model is not loaded" {
				t.Errorf("Message = %q", apiErr.Message)
			}
		})
	}
}

// TestAStringErrorFrameEndsAStream: a frame the decoder could not read becomes an
// unknown event and the stream goes on, so without the string form a failure read
// as nothing at all.
func TestAStringErrorFrameEndsAStream(t *testing.T) {
	client, _ := localServer(t, LMStudio, http.StatusOK, "data: {\"error\":\"the model crashed\"}\n\n")
	_, err := collectEvents(client.Stream(t.Context(), simpleRequest()))
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.Message != "the model crashed" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

// TestASuccessfulBodyIsNotReadAsAnErrorByItsOtherMembers: the catalog path hands
// a body it could not read to the decoder with no status, and only an error
// member says a 200 failed.
func TestASuccessfulBodyIsNotReadAsAnErrorByItsOtherMembers(t *testing.T) {
	if apiErr := decodeError(0, http.Header{}, []byte(`{"message":"ok","detail":"ok"}`)); apiErr != nil {
		t.Errorf("decodeError read a frame with no error member as %+v", apiErr)
	}
	if apiErr := decodeError(0, http.Header{}, []byte(`{"error":"down"}`)); apiErr == nil || apiErr.Message != "down" {
		t.Errorf("decodeError(0) = %+v, want the sentence", apiErr)
	}
}
