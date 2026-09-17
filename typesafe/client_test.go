package typesafe

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// testKey is the key every test client sends. It is not a real credential.
const testKey = "ts-test-key"

// capture is what the test server saw: the counts and the fields the client is
// responsible for setting. The body is kept verbatim, because the byte-for-byte
// assertions are the point of most of this file.
type capture struct {
	Requests    int
	Method      string
	Path        string
	Auth        string
	ContentType string
	Body        []byte
}

// serve starts a test server running h and returns a client pointed at it.
func serve(t *testing.T, h http.HandlerFunc, opts ...ClientOption) (*Client, *capture) {
	t.Helper()
	seen := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.Requests++
		seen.Method = r.Method
		seen.Path = r.URL.Path
		seen.Auth = r.Header.Get("Authorization")
		seen.ContentType = r.Header.Get("Content-Type")
		seen.Body = body
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	opts = append([]ClientOption{WithBaseURL(srv.URL)}, opts...)
	return New(testKey, opts...), seen
}

// serveJSON starts a server that answers every call with one status and body.
func serveJSON(t *testing.T, status int, body string, opts ...ClientOption) (*Client, *capture) {
	t.Helper()
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}, opts...)
}

// okAnswers is the smallest successful response body, used where the test is
// about the request rather than the answer.
const okAnswers = `{"model":"jev-1.13.0","answers":{"is_urgent":{"type":"noul","noul":0.5}},` +
	`"usage":{"input_tokens":12,"output_tokens":3}}`

// documentedState and documentedQuestions are TypeSafe's own example from
// docs.typesafe.ai/api, reordered so that the questions are *not* in
// alphabetical order: a marshaler that sorted them would be caught.
const documentedState = "Help! My payouts have been failing for 3 days."

func documentedQuestions() Questions {
	return Questions{
		{ID: "department", Question: Choice{
			Instructions: "Which team should handle this?",
			Options: []Option{
				{Key: "billing", Description: "Payments, invoicing, refunds"},
				{Key: "technical", Description: "Bugs, outages, integrations"},
				{Key: "sales"},
			},
		}},
		{ID: "is_urgent", Question: Noul{
			Instructions: "Does this convey urgency?",
			True:         "Explicitly time-sensitive",
			False:        "No urgency expressed",
		}},
		{ID: "frustration", Question: Score{
			Instructions: "How frustrated is the customer?",
			Levels:       []string{"Calm", "Frustrated", "Very angry"},
		}},
	}
}

// documentedBody is the exact body the questions above must produce: the three
// top-level members in this order, the questions in author order, each
// question's members in the order the API reference lists them, and the
// option with no description as null.
const documentedBody = `{"state":"Help! My payouts have been failing for 3 days.",` +
	`"model":"jev-latest",` +
	`"questions":{` +
	`"department":{"type":"choice","instructions":"Which team should handle this?",` +
	`"criteria":{"billing":"Payments, invoicing, refunds","technical":"Bugs, outages, integrations","sales":null}},` +
	`"is_urgent":{"type":"noul","instructions":"Does this convey urgency?",` +
	`"criteria":{"true":"Explicitly time-sensitive","false":"No urgency expressed"}},` +
	`"frustration":{"type":"score","instructions":"How frustrated is the customer?",` +
	`"criteria":["Calm","Frustrated","Very angry"]}}}`

func TestAskSendsTheDocumentedRequest(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, okAnswers)
	if _, err := client.Ask(t.Context(), documentedState, documentedQuestions()); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if seen.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", seen.Method)
	}
	if seen.Path != "/v1/systemone" {
		t.Errorf("path = %s, want /v1/systemone", seen.Path)
	}
	if seen.Auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want %q", seen.Auth, "Bearer "+testKey)
	}
	if seen.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", seen.ContentType)
	}
	if got := string(seen.Body); got != documentedBody {
		t.Errorf("body mismatch\n got: %s\nwant: %s", got, documentedBody)
	}
}

func TestTwoIdenticalAsksSendIdenticalBytes(t *testing.T) {
	// Invariant 8: the same questions marshal to the same bytes, so a
	// prompt cache and a diff of two requests both mean something. A map
	// anywhere in the request path would break this at random.
	client, seen := serveJSON(t, http.StatusOK, okAnswers)
	state := map[string]any{"zeta": 1, "alpha": 2, "mu": 3}
	var bodies []string
	for range 8 {
		if _, err := client.Ask(t.Context(), state, documentedQuestions()); err != nil {
			t.Fatalf("Ask: %v", err)
		}
		bodies = append(bodies, string(seen.Body))
	}
	for i, body := range bodies {
		if body != bodies[0] {
			t.Fatalf("request %d differs from the first\n got: %s\nwant: %s", i, body, bodies[0])
		}
	}
	if !strings.Contains(bodies[0], `"state":{"alpha":2,"mu":3,"zeta":1}`) {
		t.Errorf("a map state is not written in a stable order: %s", bodies[0])
	}
}

func TestAskMarshalsTheStateAsGiven(t *testing.T) {
	type record struct {
		Author string `json:"author"`
		Body   string `json:"body"`
	}
	cases := map[string]struct {
		state any
		want  string
	}{
		"string": {"plain text", `"state":"plain text"`},
		"struct": {record{Author: "ada", Body: "hi"}, `"state":{"author":"ada","body":"hi"}`},
		"slice":  {[]record{{Author: "ada"}}, `"state":[{"author":"ada","body":""}]`},
		"number": {42, `"state":42`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, seen := serveJSON(t, http.StatusOK, okAnswers)
			if _, err := client.Ask(t.Context(), tc.state, documentedQuestions()); err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if !strings.Contains(string(seen.Body), tc.want) {
				t.Errorf("body does not carry %s: %s", tc.want, seen.Body)
			}
		})
	}
}

func TestWithModelOverridesTheDefault(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, okAnswers)
	if _, err := client.Ask(t.Context(), "hi", documentedQuestions(), WithModel("jev-1.13.0")); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !strings.Contains(string(seen.Body), `"model":"jev-1.13.0"`) {
		t.Errorf("the model option was not sent: %s", seen.Body)
	}
	if DefaultModel != "jev-latest" {
		t.Errorf("DefaultModel = %q, want jev-latest", DefaultModel)
	}
}

// TestAskRefusesABadRequestBeforeTheNetwork is the "fail loudly" half: a
// request the API would refuse with a 422 costs no call and no tokens, and the
// error names what is wrong.
func TestAskRefusesABadRequestBeforeTheNetwork(t *testing.T) {
	cases := map[string]struct {
		state     any
		questions Questions
		phrase    string
	}{
		"no questions": {
			state: "hi", questions: nil, phrase: "at least one question",
		},
		"empty questions": {
			state: "hi", questions: Questions{}, phrase: "at least one question",
		},
		"nil state": {
			state: nil, questions: documentedQuestions(), phrase: "state",
		},
		"empty id": {
			state:     "hi",
			questions: Questions{{Question: Noul{Instructions: "urgent?"}}},
			phrase:    "id",
		},
		"duplicate id": {
			state: "hi",
			questions: Questions{
				{ID: "dup", Question: Noul{Instructions: "urgent?"}},
				{ID: "dup", Question: Noul{Instructions: "angry?"}},
			},
			phrase: `"dup"`,
		},
		"nil question": {
			state:     "hi",
			questions: Questions{{ID: "empty"}},
			phrase:    `"empty"`,
		},
		"no instructions": {
			state:     "hi",
			questions: Questions{{ID: "urgent", Question: Noul{}}},
			phrase:    "instructions",
		},
		"score with one level": {
			state: "hi",
			questions: Questions{{ID: "frustration", Question: Score{
				Instructions: "How frustrated?",
				Levels:       []string{"Calm"},
			}}},
			phrase: "two levels",
		},
		"score with no levels": {
			state: "hi",
			questions: Questions{{ID: "frustration", Question: Score{
				Instructions: "How frustrated?",
			}}},
			phrase: "two levels",
		},
		"choice with no options": {
			state: "hi",
			questions: Questions{{ID: "department", Question: Choice{
				Instructions: "Which team?",
			}}},
			phrase: "option",
		},
		"choice with an unnamed option": {
			state: "hi",
			questions: Questions{{ID: "department", Question: Choice{
				Instructions: "Which team?",
				Options:      []Option{{Key: "billing"}, {Description: "no key"}},
			}}},
			phrase: "option",
		},
		"choice with a repeated option": {
			state: "hi",
			questions: Questions{{ID: "department", Question: Choice{
				Instructions: "Which team?",
				Options:      []Option{{Key: "billing"}, {Key: "billing", Description: "again"}},
			}}},
			phrase: `"billing"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, seen := serveJSON(t, http.StatusOK, okAnswers)
			_, err := client.Ask(t.Context(), tc.state, tc.questions)
			if err == nil {
				t.Fatal("Ask accepted a request the API would refuse")
			}
			if seen.Requests != 0 {
				t.Errorf("the client made %d calls; a request this bad must cost nothing", seen.Requests)
			}
			if !errors.Is(err, goodall.KindInvalidRequest) {
				t.Errorf("error is not KindInvalidRequest: %v", err)
			}
			if !strings.Contains(err.Error(), tc.phrase) {
				t.Errorf("error %q does not name %s", err, tc.phrase)
			}
		})
	}
}

func TestNewTakesTheDocumentedDefaults(t *testing.T) {
	if ProviderName != "typesafe" {
		t.Errorf("ProviderName = %q, want typesafe", ProviderName)
	}
	if DefaultBaseURL != "https://api.typesafe.ai" {
		t.Errorf("DefaultBaseURL = %q, want https://api.typesafe.ai", DefaultBaseURL)
	}
	client := New("k")
	if client.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want the default", client.baseURL)
	}
	trimmed := New("k", WithBaseURL("https://example.test/"))
	if trimmed.baseURL != "https://example.test" {
		t.Errorf("WithBaseURL kept the trailing slash: %q", trimmed.baseURL)
	}
	retries := New("k", WithMaxRetries(5))
	if retries.http.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", retries.http.MaxRetries)
	}
	custom := &http.Client{}
	if New("k", WithHTTPClient(custom)).http.HTTP != custom {
		t.Error("WithHTTPClient did not install the client")
	}
}
