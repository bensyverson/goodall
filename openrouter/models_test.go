package openrouter

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestModelMapsTheCatalogueEntry(t *testing.T) {
	var path string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write(fixture(t, "models.json"))
	})
	info, err := c.Model(t.Context(), "anthropic/claude-haiku-4.5")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if !strings.HasSuffix(path, "/models") {
		t.Errorf("path = %q, want it to end in /models", path)
	}
	if info.Provider != ProviderName {
		t.Errorf("Provider = %q", info.Provider)
	}
	caps := info.Capabilities
	for _, tc := range []struct {
		name string
		got  goodall.Support
		want goodall.Support
	}{
		{"ImageInput", caps.ImageInput, goodall.Supported},
		{"PDFInput", caps.PDFInput, goodall.Supported},
		{"AudioInput", caps.AudioInput, goodall.Unsupported},
		{"Tools", caps.Tools, goodall.Supported},
		{"Thinking", caps.Thinking, goodall.Supported},
		{"StructuredOutput", caps.StructuredOutput, goodall.Supported},
		{"CacheControl", caps.CacheControl, goodall.SupportUnknown},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if caps.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d", caps.ContextWindow)
	}
	if caps.MaxOutput != 64000 {
		t.Errorf("MaxOutput = %d", caps.MaxOutput)
	}
	wantEfforts := []goodall.Effort{goodall.EffortLow, goodall.EffortMedium, goodall.EffortHigh}
	if len(caps.ThinkingEfforts) != len(wantEfforts) {
		t.Fatalf("ThinkingEfforts = %v, want %v", caps.ThinkingEfforts, wantEfforts)
	}
	for i := range wantEfforts {
		if caps.ThinkingEfforts[i] != wantEfforts[i] {
			t.Fatalf("ThinkingEfforts = %v, want %v", caps.ThinkingEfforts, wantEfforts)
		}
	}
}

func TestModelPricesAreExactDecimals(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "models.json"))
	})
	info, err := c.Model(t.Context(), "anthropic/claude-haiku-4.5")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if info.Pricing == nil {
		t.Fatal("Pricing is nil, want the published prices")
	}
	for _, tc := range []struct {
		name string
		got  goodall.Decimal
		want string
	}{
		{"Input", info.Pricing.Input, "0.000003"},
		{"Output", info.Pricing.Output, "0.000015"},
		{"CacheRead", info.Pricing.CacheRead, "0.00000015"},
		{"CacheWrite", info.Pricing.CacheWrite, "0.00000375"},
	} {
		if got := tc.got.String(); got != tc.want {
			t.Errorf("%s = %q, want %q exactly — a float64 would not round-trip", tc.name, got, tc.want)
		}
	}
	if info.Pricing.Currency != "USD" {
		t.Errorf("Currency = %q", info.Pricing.Currency)
	}
}

func TestModelTextOnlyEntry(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "models.json"))
	})
	info, err := c.Model(t.Context(), "ibm-granite/granite-4.2-8b")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	caps := info.Capabilities
	if caps.ImageInput != goodall.Unsupported {
		t.Errorf("ImageInput = %v, want unsupported", caps.ImageInput)
	}
	if caps.Tools != goodall.Unsupported {
		t.Errorf("Tools = %v, want unsupported", caps.Tools)
	}
	if caps.Thinking != goodall.Unsupported {
		t.Errorf("Thinking = %v, want unsupported", caps.Thinking)
	}
	if caps.MaxOutput != 0 {
		t.Errorf("MaxOutput = %d, want 0 when max_completion_tokens is null", caps.MaxOutput)
	}
	if len(caps.ThinkingEfforts) != 0 {
		t.Errorf("ThinkingEfforts = %v, want none", caps.ThinkingEfforts)
	}
	if info.Pricing == nil || info.Pricing.CacheRead.String() != "0" {
		t.Errorf("Pricing = %+v, want a zero cache-read price", info.Pricing)
	}
}

func TestModelWithoutListsIsUnknownNotUnsupported(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"local/model","context_length":8192}]}`))
	})
	info, err := c.Model(t.Context(), "local/model")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	caps := info.Capabilities
	if caps.ImageInput != goodall.SupportUnknown || caps.Tools != goodall.SupportUnknown {
		t.Errorf("a catalogue entry with no lists must read unknown, got %+v", caps)
	}
	if caps.ContextWindow != 8192 {
		t.Errorf("ContextWindow = %d", caps.ContextWindow)
	}
	if info.Pricing != nil {
		t.Errorf("Pricing = %+v, want nil when the provider publishes none", info.Pricing)
	}
}

func TestModelNotInCatalogueIsNotFound(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "models.json"))
	})
	_, err := c.Model(t.Context(), "nobody/nothing")
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("Model error = %v (%T), want *goodall.APIError", err, err)
	}
	if apiErr.Kind != goodall.KindNotFound {
		t.Errorf("Kind = %v, want not_found", apiErr.Kind)
	}
	if !strings.Contains(apiErr.Message, "nobody/nothing") {
		t.Errorf("Message = %q, want it to name the model", apiErr.Message)
	}
}
