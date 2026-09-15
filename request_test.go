package goodall

import (
	"errors"
	"testing"
)

// TestToolChoiceConstructors checks the four modes a request can ask for, and
// that the modifier composes with them rather than doubling the constructors.
func TestToolChoiceConstructors(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  ToolChoice
		want ToolChoice
	}{
		{"auto", ChooseAuto(), ToolChoice{}},
		{"any", ChooseAny(), ToolChoice{Mode: ToolChoiceAny}},
		{"none", ChooseNone(), ToolChoice{Mode: ToolChoiceNone}},
		{"named", ChooseTool("get_weather"), ToolChoice{Mode: ToolChoiceNamed, Name: "get_weather"}},
		{"serial", ChooseAny().Serial(), ToolChoice{Mode: ToolChoiceAny, NoParallel: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("= %+v, want %+v", tc.got, tc.want)
			}
		})
	}
	if ChooseAuto() != (ToolChoice{}) {
		t.Error("the zero ToolChoice is not the automatic choice")
	}
}

// TestToolChoiceSerialDoesNotMutate keeps the modifier a value operation, so a
// shared choice cannot be changed under another request.
func TestToolChoiceSerialDoesNotMutate(t *testing.T) {
	base := ChooseAny()
	_ = base.Serial()
	if base.NoParallel {
		t.Error("Serial() changed the receiver")
	}
}

// TestToolChoiceValidate: a provider must not have to guess what a
// half-specified choice meant.
func TestToolChoiceValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		choice  ToolChoice
		wantErr bool
	}{
		{"auto", ChooseAuto(), false},
		{"any", ChooseAny(), false},
		{"none", ChooseNone(), false},
		{"named", ChooseTool("get_weather"), false},
		{"named with no name", ToolChoice{Mode: ToolChoiceNamed}, true},
		{"a name without the named mode", ToolChoice{Mode: ToolChoiceAny, Name: "get_weather"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.choice.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, want an error: %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, KindInvalidRequest) {
				t.Errorf("Validate() error %v is not KindInvalidRequest", err)
			}
		})
	}
}

// TestToolChoiceModeStringAndKnown matches the vocabulary types: the zero
// value has a name, and a mode from a newer provider survives.
func TestToolChoiceModeStringAndKnown(t *testing.T) {
	if got := ToolChoiceAuto.String(); got != "auto" {
		t.Errorf("ToolChoiceAuto.String() = %q, want auto", got)
	}
	if got := ToolChoiceNamed.String(); got != "tool" {
		t.Errorf("ToolChoiceNamed.String() = %q, want tool", got)
	}
	if !ToolChoiceAny.Known() {
		t.Error("ToolChoiceAny is not Known")
	}
	if ToolChoiceMode("required").Known() {
		t.Error("an unrecognised mode reports itself Known")
	}
}

// fakeExtension stands in for a provider's options struct.
type fakeExtension struct{ Beta string }

func (fakeExtension) Provider() string { return "anthropic" }

// TestExtensionNamesItsProvider: a request built for one provider must fail
// loudly on another rather than losing its options, which is what the
// interface's one method is for.
func TestExtensionNamesItsProvider(t *testing.T) {
	req := &Request{Model: "claude-opus-5", Extensions: fakeExtension{Beta: "structured-outputs-2025-11-13"}}
	if req.Extensions.Provider() != "anthropic" {
		t.Errorf("Provider() = %q, want anthropic", req.Extensions.Provider())
	}
	if _, ok := req.Extensions.(fakeExtension); !ok {
		t.Error("a provider cannot recover its own extension type")
	}
}
