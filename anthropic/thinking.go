package anthropic

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bensyverson/goodall"
)

// minThinkingBudget is the smallest budget_tokens Anthropic accepts on a
// budget-only model. A derived budget is raised to it, and a max_tokens with
// no room for it is refused rather than sent.
const minThinkingBudget = 1024

// thinkingStyle is how a model takes its thinking settings. It is a typed
// fact about the model rather than a pair of booleans, because the three
// cases are mutually exclusive and each one shapes the whole thinking object.
type thinkingStyle string

const (
	// styleAdaptive takes thinking {type: "adaptive", display} with the
	// effort rung in output_config, and accepts {type: "disabled"}.
	styleAdaptive thinkingStyle = "adaptive"
	// styleAlwaysOn is adaptive but refuses to be turned off, so
	// EffortOff omits the thinking settings entirely.
	styleAlwaysOn thinkingStyle = "always_on"
	// styleBudget is the older form: thinking {type: "enabled",
	// budget_tokens} with no display and no effort rung.
	styleBudget thinkingStyle = "budget"
)

// thinkingStyleFor classifies a model by its name, which is all goodall has
// before a call. The heuristic, in order:
//
//   - a name containing "fable" is styleAlwaysOn: Fable 5.x always thinks and
//     rejects an explicit disabled;
//   - otherwise the first major.minor version in the name decides — 4.6 and
//     above is styleAdaptive, anything below (Haiku 4.5, Opus 4.5 and every
//     3.x) is styleBudget;
//   - a name with no version in it is styleAdaptive, because an unrecognized
//     name is far more likely to be newer than older.
//
// The version is read from hyphen-separated components, so both spellings
// work — claude-opus-4-6 and claude-3-7-sonnet-20250219 — and a trailing date
// is not mistaken for a minor version. A vendor prefix
// (us.anthropic.claude-…, anthropic/claude-…) does not disturb it.
//
// It is a guess, and only the guess: where the request carries the
// catalog's answer, [styleFor] prefers it.
func thinkingStyleFor(model string) thinkingStyle {
	name := strings.ToLower(model)
	if strings.Contains(name, "fable") {
		return styleAlwaysOn
	}
	major, minor, ok := modelVersion(name)
	if !ok {
		return styleAdaptive
	}
	if major > 4 || (major == 4 && minor >= 6) {
		return styleAdaptive
	}
	return styleBudget
}

// styleFor decides how a model takes its thinking settings, preferring the
// catalog's published fact to the name heuristic. The loop puts the
// catalog entry on the request; a caller who builds one by hand may too,
// and nil simply leaves the heuristic in charge.
//
// The catalog reports which thinking *form* a model accepts and nothing
// about whether it may be turned off, so a name that reads as always-on keeps
// saying so on top of a published "adaptive": Fable rejects an explicit
// disabled, and the models endpoint has no leaf that says it.
func styleFor(model string, info *goodall.ModelInfo) thinkingStyle {
	heuristic := thinkingStyleFor(model)
	if info == nil {
		return heuristic
	}
	switch info.Capabilities.ThinkingStyle {
	case goodall.ThinkingAdaptive:
		if heuristic == styleAlwaysOn {
			return styleAlwaysOn
		}
		return styleAdaptive
	case goodall.ThinkingBudget:
		return styleBudget
	}
	return heuristic
}

// modelVersion reads the first major.minor version out of a hyphenated model
// name. The minor part is only taken from a one- or two-digit component, so
// the release date in claude-opus-5-20260214 is not read as a minor version.
func modelVersion(name string) (major, minor int, ok bool) {
	parts := strings.Split(name, "-")
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || part == "" {
			continue
		}
		major = n
		if i+1 < len(parts) && len(parts[i+1]) <= 2 {
			if m, err := strconv.Atoi(parts[i+1]); err == nil {
				minor = m
			}
		}
		return major, minor, true
	}
	return 0, 0, false
}

// budgetPercent is the share of max_tokens an effort rung spends on thinking
// for a budget-only model, as whole percent so the arithmetic is exact.
//
// Low, medium and high are the ratios OpenRouter uses when it translates an
// effort for a Claude model (0.2, 0.5, 0.8), so a thread that moves between
// the two providers thinks about as hard on both. The two rungs above high
// are goodall's own, spaced to stay ordered and to leave room for the answer:
// 0.95 is the most OpenRouter ever asks for.
func budgetPercent(e goodall.Effort) (int, bool) {
	switch e {
	case goodall.EffortLow:
		return 20, true
	case goodall.EffortMedium:
		return 50, true
	case goodall.EffortHigh:
		return 80, true
	case goodall.EffortXHigh:
		return 90, true
	case goodall.EffortMax:
		return 95, true
	}
	return 0, false
}

// budgetTokens derives budget_tokens from an effort rung, clamped to at least
// Anthropic's minimum and strictly below max_tokens, which the API requires
// so the model has room to answer after it has finished thinking.
func budgetTokens(percent, maxTokens int) (int, error) {
	if maxTokens <= minThinkingBudget {
		return 0, fmt.Errorf("anthropic: %w: max_tokens is %d, and a thinking budget on this model must be at least %d and below max_tokens",
			goodall.KindInvalidRequest, maxTokens, minThinkingBudget)
	}
	budget := maxTokens * percent / 100
	return min(max(budget, minThinkingBudget), maxTokens-1), nil
}

// thinkingFor translates a request's thinking configuration. It returns the
// thinking object and the output_config that carries the effort rung; either
// may be nil, and both are nil when the request asks for nothing. maxTokens
// is the value the request will carry, which a derived budget must stay
// below.
//
// It takes the whole request because the translation needs three things from
// it — the model's name, the thinking configuration and the catalog entry
// the loop attached — and stays a pure function of it: nothing here reads a
// catalog of its own.
//
// The zero configuration sends no thinking fields at all, so a consumer who
// never thinks about thinking adds nothing to the cached prefix. On a
// budget-only model the display setting has no wire form and is dropped: that
// generation returns thinking text whenever thinking is on.
func thinkingFor(req *goodall.Request, maxTokens int) (*wireThinkingConfig, *wireOutputConfig, error) {
	model, cfg := req.Model, req.Thinking
	if cfg.Effort == goodall.EffortDefault && cfg.Display == goodall.DisplayDefault {
		return nil, nil, nil
	}
	style := styleFor(model, req.ModelInfo)

	if cfg.Effort == goodall.EffortOff {
		if style == styleAlwaysOn {
			// The model thinks whatever it is told, and sending
			// disabled is a 400, so the setting is left out.
			return nil, nil, nil
		}
		return &wireThinkingConfig{Type: thinkingDisabled}, nil, nil
	}

	if style == styleBudget {
		if cfg.Effort == goodall.EffortDefault {
			return nil, nil, nil
		}
		percent, ok := budgetPercent(cfg.Effort)
		if !ok {
			return nil, nil, fmt.Errorf("anthropic: %w: model %q takes a thinking budget rather than an effort, and %q has no budget to derive",
				goodall.KindInvalidRequest, model, cfg.Effort)
		}
		budget, err := budgetTokens(percent, maxTokens)
		if err != nil {
			return nil, nil, err
		}
		return &wireThinkingConfig{Type: thinkingEnabled, BudgetTokens: budget}, nil, nil
	}

	thinking := &wireThinkingConfig{Type: thinkingAdaptive, Display: cfg.Display}
	if cfg.Effort == goodall.EffortDefault {
		return thinking, nil, nil
	}
	// An effort goodall does not define passes through: the rungs are a
	// provider's to extend, and Anthropic refuses what it cannot read.
	return thinking, &wireOutputConfig{Effort: cfg.Effort}, nil
}
