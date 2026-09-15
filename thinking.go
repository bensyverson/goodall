package goodall

// Effort is how hard a model should think before answering. Adaptive thinking
// made effort the knob both providers expose, so goodall has no token budget
// in its public API; a provider that still wants one derives it from the
// effort. Like every enum decoded from a provider, it is a string, so a rung
// added to the ladder after this release survives a round trip.
type Effort string

const (
	// EffortDefault is the zero value: goodall sends nothing and the model
	// thinks as much as the provider's default says.
	EffortDefault Effort = ""
	// EffortOff asks for no thinking. Some models refuse to turn it off,
	// in which case the provider omits the setting rather than failing.
	EffortOff Effort = "off"
	// EffortLow is the shallowest deliberate setting.
	EffortLow Effort = "low"
	// EffortMedium is a middling amount of thinking.
	EffortMedium Effort = "medium"
	// EffortHigh is a generous amount of thinking.
	EffortHigh Effort = "high"
	// EffortXHigh is more than high, where the model offers it.
	EffortXHigh Effort = "xhigh"
	// EffortMax is as much thinking as the model will do.
	EffortMax Effort = "max"
)

// String names the effort, calling the zero value "default".
func (e Effort) String() string {
	if e == EffortDefault {
		return "default"
	}
	return string(e)
}

// Known reports whether this is one of the efforts goodall defines. A model
// catalogue may list a rung that is not, and it is kept as it arrived.
func (e Effort) Known() bool {
	switch e {
	case EffortDefault, EffortOff, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	}
	return false
}

// ThinkingDisplay is how much of the model's thinking comes back with the
// answer. It does not change how much the model thinks, only what is shown:
// the blocks are preserved and replayed either way.
type ThinkingDisplay string

const (
	// DisplayDefault is the zero value: whatever the provider defaults to,
	// which on recent Claude models is omitted.
	DisplayDefault ThinkingDisplay = ""
	// DisplaySummarized returns readable summaries of the thinking, which
	// is what a UI that shows a thinking pane needs to ask for.
	DisplaySummarized ThinkingDisplay = "summarized"
	// DisplayOmitted returns thinking blocks with no readable text. They
	// still arrive, and are still replayed on the next turn.
	DisplayOmitted ThinkingDisplay = "omitted"
)

// String names the display setting, calling the zero value "default".
func (d ThinkingDisplay) String() string {
	if d == DisplayDefault {
		return "default"
	}
	return string(d)
}

// Known reports whether this is one of the display settings goodall defines.
func (d ThinkingDisplay) Known() bool {
	switch d {
	case DisplayDefault, DisplaySummarized, DisplayOmitted:
		return true
	}
	return false
}

// Thinking is the thinking configuration of a request. The zero value asks
// for nothing, so a consumer who never thinks about thinking sends no thinking
// fields at all.
type Thinking struct {
	// Effort is how hard the model should think.
	Effort Effort `json:"effort,omitzero"`
	// Display is how much of that thinking comes back as readable text.
	Display ThinkingDisplay `json:"display,omitzero"`
}
