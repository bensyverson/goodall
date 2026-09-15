package goodall

import (
	"encoding/json/v2"
	"slices"
	"testing"
)

// sameCapabilities compares two capability sets field by field; Capabilities
// carries a slice, so == does not apply.
func sameCapabilities(a, b Capabilities) bool {
	if !slices.Equal(a.ThinkingEfforts, b.ThinkingEfforts) {
		return false
	}
	return a.ImageInput == b.ImageInput &&
		a.PDFInput == b.PDFInput &&
		a.AudioInput == b.AudioInput &&
		a.Tools == b.Tools &&
		a.Thinking == b.Thinking &&
		a.CacheControl == b.CacheControl &&
		a.StructuredOutput == b.StructuredOutput &&
		a.ContextWindow == b.ContextWindow &&
		a.MaxOutput == b.MaxOutput
}

func TestSupportString(t *testing.T) {
	cases := []struct {
		in   Support
		want string
	}{
		{SupportUnknown, "unknown"},
		{Support(0), "unknown"},
		{Supported, "supported"},
		{Unsupported, "unsupported"},
		{Support(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("Support(%d).String() = %q, want %q", int(c.in), got, c.want)
		}
	}
}

// TestSupportTextRoundTrip: unknown is the zero value and any text goodall does
// not recognise decodes to it, because invariant 11 says unknown means try.
func TestSupportTextRoundTrip(t *testing.T) {
	for _, s := range []Support{SupportUnknown, Supported, Unsupported} {
		text, err := s.MarshalText()
		if err != nil {
			t.Fatalf("Support(%d).MarshalText(): %v", int(s), err)
		}
		if string(text) != s.String() {
			t.Errorf("MarshalText = %q, want %q", text, s.String())
		}
		var back Support
		if err := back.UnmarshalText(text); err != nil {
			t.Fatalf("UnmarshalText(%q): %v", text, err)
		}
		if back != s {
			t.Errorf("round trip of %v = %v", s, back)
		}
	}
	for _, text := range []string{"", "maybe", "Supported", "true"} {
		var got Support
		if err := got.UnmarshalText([]byte(text)); err != nil {
			t.Errorf("UnmarshalText(%q): %v, want no error", text, err)
		}
		if got != SupportUnknown {
			t.Errorf("UnmarshalText(%q) = %v, want SupportUnknown", text, got)
		}
	}
}

func TestCapabilityString(t *testing.T) {
	cases := []struct {
		in   Capability
		want string
	}{
		{CapImageInput, "image_input"},
		{CapPDFInput, "pdf_input"},
		{CapAudioInput, "audio_input"},
		{CapTools, "tools"},
		{CapThinking, "thinking"},
		{CapCacheControl, "cache_control"},
		{CapStructuredOutput, "structured_output"},
		{Capability("video_input"), "video_input"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("Capability(%q).String() = %q, want %q", string(c.in), got, c.want)
		}
	}
}

func TestCapabilityKnown(t *testing.T) {
	for _, c := range []Capability{CapImageInput, CapPDFInput, CapAudioInput, CapTools, CapThinking, CapCacheControl, CapStructuredOutput} {
		if !c.Known() {
			t.Errorf("Capability(%q).Known() = false, want true", string(c))
		}
	}
	for _, c := range []Capability{"", "video_input", "Tools"} {
		if c.Known() {
			t.Errorf("Capability(%q).Known() = true, want false", string(c))
		}
	}
}

func TestCapabilitiesGet(t *testing.T) {
	caps := Capabilities{
		ImageInput:       Supported,
		PDFInput:         Unsupported,
		AudioInput:       SupportUnknown,
		Tools:            Supported,
		Thinking:         Supported,
		CacheControl:     Unsupported,
		StructuredOutput: Supported,
	}
	cases := []struct {
		in   Capability
		want Support
	}{
		{CapImageInput, Supported},
		{CapPDFInput, Unsupported},
		{CapAudioInput, SupportUnknown},
		{CapTools, Supported},
		{CapThinking, Supported},
		{CapCacheControl, Unsupported},
		{CapStructuredOutput, Supported},
		{Capability("video_input"), SupportUnknown},
		{Capability(""), SupportUnknown},
	}
	for _, c := range cases {
		if got := caps.Get(c.in); got != c.want {
			t.Errorf("Get(%q) = %v, want %v", string(c.in), got, c.want)
		}
	}
	var zero Capabilities
	for _, c := range []Capability{CapImageInput, CapTools, CapThinking} {
		if got := zero.Get(c); got != SupportUnknown {
			t.Errorf("zero Capabilities.Get(%q) = %v, want SupportUnknown", string(c), got)
		}
	}
}

// TestCapabilitiesJSON: a fact nobody established is absent from the wire
// rather than decoded as "unsupported".
func TestCapabilitiesJSON(t *testing.T) {
	var zero Capabilities
	got, err := json.Marshal(zero)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{}` {
		t.Fatalf("zero Capabilities marshals as %s, want {}", got)
	}

	caps := Capabilities{
		ImageInput:       Supported,
		PDFInput:         Unsupported,
		Tools:            Supported,
		Thinking:         Supported,
		ThinkingEfforts:  []Effort{EffortLow, EffortHigh, EffortMax},
		ContextWindow:    200000,
		MaxOutput:        64000,
		StructuredOutput: Supported,
	}
	const want = `{"image_input":"supported","pdf_input":"unsupported","tools":"supported","thinking":"supported","structured_output":"supported","thinking_efforts":["low","high","max"],"context_window":200000,"max_output":64000}`
	got, err = json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("marshal = %s, want %s", got, want)
	}
	var back Capabilities
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if !sameCapabilities(back, caps) {
		t.Fatalf("round trip = %+v, want %+v", back, caps)
	}
}

func TestModelInfoJSON(t *testing.T) {
	info := ModelInfo{
		ID:       "anthropic/claude-opus-5",
		Provider: "openrouter",
		Capabilities: Capabilities{
			ImageInput:    Supported,
			Tools:         Supported,
			ContextWindow: 200000,
		},
		Pricing: &Pricing{
			Input:      mustDecimal(t, "0.000005"),
			Output:     mustDecimal(t, "0.000025"),
			CacheRead:  mustDecimal(t, "0.0000005"),
			CacheWrite: mustDecimal(t, "0.00000625"),
			Currency:   "USD",
		},
	}
	const want = `{"id":"anthropic/claude-opus-5","provider":"openrouter","capabilities":{"image_input":"supported","tools":"supported","context_window":200000},"pricing":{"input":0.000005,"output":0.000025,"cache_read":0.0000005,"cache_write":0.00000625,"currency":"USD"}}`
	got, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("marshal = %s, want %s", got, want)
	}
	var back ModelInfo
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if back.Pricing == nil || *back.Pricing != *info.Pricing {
		t.Fatalf("pricing round trip = %+v, want %+v", back.Pricing, info.Pricing)
	}
	if back.ID != info.ID || back.Provider != info.Provider || !sameCapabilities(back.Capabilities, info.Capabilities) {
		t.Fatalf("round trip = %+v, want %+v", back, info)
	}
}

// TestModelInfoUnknownPricingIsNil: a catalogue entry without prices must stay
// distinguishable from one that prices everything at zero.
func TestModelInfoUnknownPricingIsNil(t *testing.T) {
	var info ModelInfo
	got, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"capabilities":{}}` {
		t.Fatalf("zero ModelInfo marshals as %s, want {\"capabilities\":{}}", got)
	}
	var back ModelInfo
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if back.Pricing != nil {
		t.Fatalf("pricing = %+v, want nil", back.Pricing)
	}
}
