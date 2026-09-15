package anthropic

import (
	"fmt"

	"github.com/bensyverson/goodall"
)

// DefaultMaxTokens is the output cap sent when a goodall.Request leaves
// MaxTokens at zero. Anthropic requires max_tokens and publishes no default,
// so one has to be chosen: this is large enough for a tool-using turn with
// thinking and within the output limit of every current Claude model.
//
// The catalog's max_output deliberately does not override it. Anthropic
// refuses a non-streaming request whose max_tokens implies more than ten
// minutes of generation, so sending a model's 64k or 128k ceiling would break
// Complete on the very models that publish the largest one. A caller who
// wants a different cap sets Request.MaxTokens.
const DefaultMaxTokens = 8192

// Beta is a value for the anthropic-beta header. Betas are opt-in features
// whose wire shape is not yet stable, so goodall never sends one on its own;
// a caller names the ones their request needs.
type Beta string

const (
	// BetaThinkingBindingControls reports what the server did to a request
	// whose thinking blocks no longer match the prefix that produced them,
	// in the response's input_transformations, instead of dropping them
	// silently.
	BetaThinkingBindingControls Beta = "thinking-binding-controls-2026-08-01"
	// BetaStructuredOutputs turns on strict tool schemas and the structured
	// output format.
	BetaStructuredOutputs Beta = "structured-outputs-2025-11-13"
)

// String is the header value, verbatim.
func (b Beta) String() string { return string(b) }

// ServiceTier selects how a request is served and billed. The zero value
// sends nothing and takes the account's default.
type ServiceTier string

const (
	// ServiceTierDefault is the zero value: send no service_tier.
	ServiceTierDefault ServiceTier = ""
	// ServiceTierAuto uses provisioned throughput where it is available
	// and falls back to standard capacity.
	ServiceTierAuto ServiceTier = "auto"
	// ServiceTierStandardOnly refuses the priority tier, so a request is
	// never billed at the priority price.
	ServiceTierStandardOnly ServiceTier = "standard_only"
)

// Extensions carries the Anthropic-only options of one request. It is
// goodall's Extension for this provider: set it on Request.Extensions, and a
// request carrying another provider's extension is refused before the call
// rather than losing its options silently.
//
// It is deliberately small. Anything that belongs to every provider lives on
// goodall.Request, and anything experimental belongs behind a Beta.
type Extensions struct {
	// Betas are the beta features this request needs, sent as the
	// anthropic-beta header in the order given.
	Betas []Beta
	// ServiceTier selects how the request is served and billed.
	ServiceTier ServiceTier
}

// Provider names the provider that understands these options.
func (Extensions) Provider() string { return ProviderName }

// extensionsFor reads the request's extension. A nil extension is the common
// case and yields the zero options; another provider's is an error naming
// them, because a request built for OpenRouter and sent here would otherwise
// quietly lose its routing and its reasoning settings.
func extensionsFor(ext goodall.Extension) (Extensions, error) {
	switch v := ext.(type) {
	case nil:
		return Extensions{}, nil
	case Extensions:
		return v, nil
	case *Extensions:
		if v == nil {
			return Extensions{}, nil
		}
		return *v, nil
	default:
		return Extensions{}, fmt.Errorf("anthropic: %w: the request carries %s extensions (%T), which this provider cannot send",
			goodall.KindInvalidRequest, v.Provider(), v)
	}
}
