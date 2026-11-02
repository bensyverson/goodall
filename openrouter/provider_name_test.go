package openrouter

import (
	"errors"
	"net/http"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// A developer with only an OpenAI key should not be told an error came from
// OpenRouter, whichever path built it. Extensions keep the package's name, since
// that is how a request's extensions are matched to this client.
func TestAnErrorNamesTheDialectsServer(t *testing.T) {
	refusal := `{"error":{"message":"no","code":"bad"}}`
	question := decision.Questions{{ID: "q", Question: decision.Predicate{Instructions: "?"}}}
	calls := map[string]struct {
		status int
		body   string
		call   func(c *Client, t *testing.T) error
	}{
		"Complete refused": {http.StatusBadRequest, refusal, func(c *Client, t *testing.T) error {
			_, err := c.Complete(t.Context(), simpleRequest())
			return err
		}},
		"Complete with an error in a 200": {http.StatusOK, refusal, func(c *Client, t *testing.T) error {
			_, err := c.Complete(t.Context(), simpleRequest())
			return err
		}},
		"Stream refused": {http.StatusBadRequest, refusal, func(c *Client, t *testing.T) error {
			_, err := c.Stream(t.Context(), simpleRequest()).Collect()
			return err
		}},
		"Stream with an error frame": {http.StatusOK, "data: " + refusal + "\n\n" + "data: {\"type\":\"error\",\"message\":\"no\"}\n\n", func(c *Client, t *testing.T) error {
			_, err := c.Stream(t.Context(), simpleRequest()).Collect()
			return err
		}},
		"Decide refused": {http.StatusBadRequest, refusal, func(c *Client, t *testing.T) error {
			_, err := c.Decide(t.Context(), &decision.Request{Model: "m", State: "s", Questions: question})
			return err
		}},
		"Model refused": {http.StatusBadRequest, refusal, func(c *Client, t *testing.T) error {
			_, err := c.Model(t.Context(), "m")
			return err
		}},
		"Model not listed": {http.StatusOK, `{"data":[{"id":"other"}]}`, func(c *Client, t *testing.T) error {
			_, err := c.Model(t.Context(), "m")
			return err
		}},
	}
	for _, dialect := range []Dialect{OpenRouter, OpenAI, LMStudio, Generic} {
		for name, tc := range calls {
			t.Run(dialect.String()+"/"+name, func(t *testing.T) {
				c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
				}, WithDialect(dialect), WithMaxRetries(-1))
				apiErr, ok := errors.AsType[*goodall.APIError](tc.call(c, t))
				if !ok {
					t.Fatalf("want a *goodall.APIError, from a %d %s", tc.status, tc.body)
				}
				if apiErr.Provider != dialect.String() {
					t.Errorf("Provider = %q, want %q", apiErr.Provider, dialect)
				}
			})
		}
	}
}

// A capability refusal is built before any call, so no response path passes it.
func TestACapabilityRefusalNamesTheDialectsServer(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) {}, WithDialect(OpenAI))
	req := simpleRequest()
	req.StopSequences = []string{"END"}
	_, err := c.Complete(t.Context(), req)
	capErr, ok := errors.AsType[*goodall.CapabilityError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.CapabilityError: %v", err, err)
	}
	if capErr.Provider != "openai" {
		t.Errorf("Provider = %q", capErr.Provider)
	}
	if _, err := c.Stream(t.Context(), req).Collect(); err == nil {
		t.Fatal("the stream was not refused")
	} else if capErr, ok := errors.AsType[*goodall.CapabilityError](err); !ok || capErr.Provider != "openai" {
		t.Errorf("Stream: %v", err)
	}
}

func TestAModelDescriptionNamesTheDialectsServer(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}, WithDialect(LMStudio))
	info, err := c.Model(t.Context(), "m")
	if err != nil {
		t.Fatal(err)
	}
	if info.Provider != "lmstudio" {
		t.Errorf("Provider = %q", info.Provider)
	}
}
