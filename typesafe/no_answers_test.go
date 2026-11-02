package typesafe

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// TestA200WithNoAnswersIsAProtocolError: the shared codec refuses a body that
// answers nothing, and on this client too that is a broken response rather than a
// model with nothing to say.
func TestA200WithNoAnswersIsAProtocolError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"model":"jev-latest"}`)
	}))
	defer srv.Close()
	client := New("key", WithBaseURL(srv.URL))
	_, err := client.Decide(t.Context(), &decision.Request{State: "s", Questions: simpleQuestions()})
	if _, ok := errors.AsType[*goodall.ProtocolError](err); !ok {
		t.Errorf("error is %T, want *goodall.ProtocolError: %v", err, err)
	}
}
