package systemone

import (
	json "encoding/json/v2"
	"testing"

	"github.com/bensyverson/goodall/decision"
)

// TestEncodeRequestOmitsAnEmptyModel: an empty string names no model, and leaving
// the member out is how a server with one model loaded is asked to use it.
func TestEncodeRequestOmitsAnEmptyModel(t *testing.T) {
	body, err := EncodeRequest(&decision.Request{State: "s", Questions: documentedQuestions()})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var members map[string]any
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	if model, present := members["model"]; present {
		t.Errorf("model = %v, want the member absent", model)
	}
}
