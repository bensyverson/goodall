package conformance_test

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/conformance"
)

// replay answers from a backend's recordings. It picks the recording whose request
// is the one that arrived and refuses any other, because a replay that answered
// whatever it was sent could not tell a wrong request from a right one.
type replay struct {
	backend conformance.Backend
}

// RoundTrip finds the recording and serves its body with its status.
func (r *replay) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if req.URL.Path != r.backend.Path {
		return nil, fmt.Errorf("posted to %s, and this backend's route is %s", req.URL.Path, r.backend.Path)
	}
	if got, want := req.Header.Get("Authorization") != "", r.backend.KeyEnv != ""; got != want {
		return nil, fmt.Errorf("Authorization header present = %v, want %v", got, want)
	}
	sent, err := canonical(body)
	if err != nil {
		return nil, fmt.Errorf("the client sent a body that is not JSON: %w", err)
	}
	var wanted []string
	for _, form := range conformance.Forms {
		name := r.backend.Fixture(form)
		recorded, err := os.ReadFile(filepath.Join("testdata", "live_"+name+".request.json"))
		if err != nil {
			return nil, err
		}
		want, err := canonical(recorded)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		wanted = append(wanted, string(want))
		if !bytes.Equal(sent, want) {
			continue
		}
		response, err := os.ReadFile(filepath.Join("testdata", "live_"+name+".json"))
		if err != nil {
			return nil, err
		}
		status := http.StatusOK
		if form == conformance.FormRefused {
			status = r.backend.RefusedStatus
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(response)),
			Request:    req,
		}, nil
	}
	return nil, fmt.Errorf("no recording of this request:\n%s\nrecorded:\n%s", sent, strings.Join(wanted, "\n"))
}

// canonical compacts a body without reordering it: a recording is indented for
// reading, and member order is part of what the client promises to keep stable.
func canonical(data []byte) ([]byte, error) {
	value := jsontext.Value(bytes.Clone(data))
	if err := value.Compact(); err != nil {
		return nil, err
	}
	return value, nil
}

// TestReplayRefusesARequestItHasNoRecordingOf proves the guard the suite's claim
// rests on: a question changed by one word finds no recording.
func TestReplayRefusesARequestItHasNoRecordingOf(t *testing.T) {
	backend := conformance.Backends[0]
	client := &http.Client{Transport: &replay{backend: backend}}
	_, err := backend.New("fixture-key", client).Decide(t.Context(), &decision.Request{
		Model: backend.Model,
		State: conformance.ObjectState,
		Questions: decision.Questions{{ID: "q", Question: decision.Predicate{
			Instructions: "A question nobody recorded?",
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "no recording of this request") {
		t.Fatalf("Decide gave %v, want the replay's refusal", err)
	}
}
