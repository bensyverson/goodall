package anthropic

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestModelReadsTheCapabilityTree(t *testing.T) {
	c, seen := serveFixture(t, "model.json", "application/json")
	got, err := c.Model(t.Context(), "claude-opus-4-5-20251101")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	req, _ := seen.last(t)
	if want := modelsPath + "/claude-opus-4-5-20251101"; req.URL.Path != want {
		t.Errorf("path = %q, want %q", req.URL.Path, want)
	}
	if req.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	want := &goodall.ModelInfo{
		ID:          "claude-opus-4-5-20251101",
		Provider:    ProviderName,
		DisplayName: "Claude Opus 4.5",
		Capabilities: goodall.Capabilities{
			ImageInput:       goodall.Supported,
			PDFInput:         goodall.Supported,
			Tools:            goodall.Supported,
			Thinking:         goodall.Supported,
			ThinkingStyle:    goodall.ThinkingAdaptive,
			CacheControl:     goodall.Supported,
			StructuredOutput: goodall.Supported,
			ThinkingEfforts:  []goodall.Effort{goodall.EffortLow, goodall.EffortMedium, goodall.EffortHigh, goodall.EffortXHigh},
			ContextWindow:    200000,
			MaxOutput:        64000,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ModelInfo =\n%#v\nwant\n%#v", got, want)
	}
	if got.Pricing != nil {
		t.Error("Pricing is set although Anthropic publishes no per-token price here")
	}
}

func TestModelReportsImageAndPDFAsSeparateFacts(t *testing.T) {
	c, _ := serveFixture(t, "model_image_only.json", "application/json")
	got, err := c.Model(t.Context(), "claude-3-haiku-20240307")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if got.Capabilities.ImageInput != goodall.Supported {
		t.Errorf("ImageInput = %v, want supported", got.Capabilities.ImageInput)
	}
	if got.Capabilities.PDFInput != goodall.Unsupported {
		t.Errorf("PDFInput = %v, want unsupported: a model can read images and refuse PDFs", got.Capabilities.PDFInput)
	}
}

func TestAnAbsentCapabilityLeafIsUnknownNotUnsupported(t *testing.T) {
	c, _ := serveFixture(t, "model_image_only.json", "application/json")
	got, err := c.Model(t.Context(), "claude-3-haiku-20240307")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if got.Capabilities.StructuredOutput != goodall.SupportUnknown {
		t.Errorf("StructuredOutput = %v, want unknown: the catalogue said nothing", got.Capabilities.StructuredOutput)
	}
	if got.Capabilities.AudioInput != goodall.SupportUnknown {
		t.Errorf("AudioInput = %v, want unknown: Anthropic publishes no such leaf", got.Capabilities.AudioInput)
	}
	if len(got.Capabilities.ThinkingEfforts) != 0 {
		t.Errorf("ThinkingEfforts = %v, want none", got.Capabilities.ThinkingEfforts)
	}
}

func TestThinkingIsUnsupportedOnlyWhenBothTypesSaySo(t *testing.T) {
	c, _ := serveFixture(t, "model_image_only.json", "application/json")
	got, err := c.Model(t.Context(), "claude-3-haiku-20240307")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if got.Capabilities.Thinking != goodall.Unsupported {
		t.Errorf("Thinking = %v, want unsupported: neither thinking type is offered", got.Capabilities.Thinking)
	}
}

// TestThinkingFollowsItsOwnLeafBeforeItsTypes covers the shape a live
// GET /v1/models/{id} returns: "thinking" carries a "supported" member beside
// its "types" map. A model offering a thinking type this version of goodall
// does not model would read as unsupported from the types alone, which would
// have the agent refuse a request the model accepts.
func TestThinkingFollowsItsOwnLeafBeforeItsTypes(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"type":"model","id":"claude-future-1","max_input_tokens":500000,"max_tokens":32000,
			"capabilities":{"thinking":{"supported":true,"types":{"enabled":{"supported":false},"adaptive":{"supported":false}}}}}`)
	})
	got, err := c.Model(t.Context(), "claude-future-1")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if got.Capabilities.Thinking != goodall.Supported {
		t.Errorf("Thinking = %v, want supported: the tree says so even though no type goodall knows is offered", got.Capabilities.Thinking)
	}
}

func TestThinkingFallsBackToTheTypesWhenTheLeafIsSilent(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"type":"model","id":"claude-quiet-1",
			"capabilities":{"thinking":{"types":{"adaptive":{"supported":true}}}}}`)
	})
	got, err := c.Model(t.Context(), "claude-quiet-1")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if got.Capabilities.Thinking != goodall.Supported {
		t.Errorf("Thinking = %v, want supported from the adaptive type alone", got.Capabilities.Thinking)
	}
}

func TestAnUnknownModelIsANotFoundAPIError(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-nope"},"request_id":"req_404"}`)
	})
	_, err := c.Model(t.Context(), "claude-nope")
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %#v, want a *goodall.APIError", err)
	}
	if !errors.Is(err, goodall.KindNotFound) {
		t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindNotFound)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", apiErr.Status)
	}
}

func TestModelEscapesTheIdentifierInThePath(t *testing.T) {
	c, seen := serveFixture(t, "model.json", "application/json")
	if _, err := c.Model(t.Context(), "weird/model id"); err != nil {
		t.Fatalf("Model: %v", err)
	}
	req, _ := seen.last(t)
	if req.URL.EscapedPath() != modelsPath+"/weird%2Fmodel%20id" {
		t.Errorf("escaped path = %q, want the identifier escaped into one segment", req.URL.EscapedPath())
	}
}

func TestModelIsFetchedOncePerIdentifier(t *testing.T) {
	// The agent asks for the model at the start of every run, so a client
	// that re-fetched would pay for the same answer on every run.
	c, seen := serveFixture(t, "model.json", "application/json")
	first, err := c.Model(t.Context(), "claude-opus-4-5-20251101")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	second, err := c.Model(t.Context(), "claude-opus-4-5-20251101")
	if err != nil {
		t.Fatalf("Model again: %v", err)
	}
	if seen.count() != 1 {
		t.Errorf("the client made %d requests for one model, want 1", seen.count())
	}
	if first != second {
		t.Errorf("the two lookups returned different values, %p and %p", first, second)
	}
	if _, err := c.Model(t.Context(), "claude-sonnet-5"); err != nil {
		t.Fatalf("a second identifier: %v", err)
	}
	if seen.count() != 2 {
		t.Errorf("the client made %d requests for two models, want 2", seen.count())
	}
}

func TestAFailedLookupIsNotRemembered(t *testing.T) {
	// A catalogue can lag the API, so "no such model" is an answer that may
	// stop being true; only successes are worth keeping.
	var calls int
	body := fixture(t, "model.json")
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write(fixture(t, "live_error.json"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	if _, err := c.Model(t.Context(), "claude-opus-4-5-20251101"); err == nil {
		t.Fatal("the first lookup succeeded, though the server refused it")
	}
	if _, err := c.Model(t.Context(), "claude-opus-4-5-20251101"); err != nil {
		t.Fatalf("the second lookup failed, so the refusal was cached: %v", err)
	}
	if calls != 2 {
		t.Errorf("the server saw %d requests, want 2", calls)
	}
}
