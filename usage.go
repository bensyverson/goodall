package goodall

// Usage counts the tokens one call consumed. Both providers report the same
// five facts under different names; a zero field means the provider reported
// nothing there, which for cache and reasoning tokens is the usual case.
type Usage struct {
	// Input is the uncached prompt tokens.
	Input int `json:"input_tokens,omitzero"`
	// Output is the generated tokens, thinking included.
	Output int `json:"output_tokens,omitzero"`
	// CacheRead is the prompt tokens served from the prompt cache, billed
	// at a fraction of the input price.
	CacheRead int `json:"cache_read_tokens,omitzero"`
	// CacheWrite is the prompt tokens written to the prompt cache, billed
	// above the input price.
	CacheWrite int `json:"cache_write_tokens,omitzero"`
	// Reasoning is the part of Output the model spent thinking, where the
	// provider breaks it out.
	Reasoning int `json:"reasoning_tokens,omitzero"`
}

// Add sums two usage reports field by field, so a run can accumulate its turns
// into one total.
func (u Usage) Add(v Usage) Usage {
	return Usage{
		Input:      u.Input + v.Input,
		Output:     u.Output + v.Output,
		CacheRead:  u.CacheRead + v.CacheRead,
		CacheWrite: u.CacheWrite + v.CacheWrite,
		Reasoning:  u.Reasoning + v.Reasoning,
	}
}

// TotalInput is how large the prompt was: cached reads and cache writes are
// prompt tokens too, so the prompt size is the sum of the three input fields,
// not Input alone.
func (u Usage) TotalInput() int {
	return u.Input + u.CacheRead + u.CacheWrite
}

// Cost is what a call cost, when the provider says so. Anthropic reports
// tokens and no money, so its costs carry Reported false and an amount of
// zero; OpenRouter reports the amount it charged. goodall keeps no price list,
// so a cost is either a provider's figure or absent.
type Cost struct {
	// Amount is the money charged, exact.
	Amount Decimal `json:"amount"`
	// Currency is the ISO code the amount is in, empty when unknown.
	Currency string `json:"currency,omitzero"`
	// Reported is whether this figure came from the provider. A false
	// value with a zero amount means "not known", never "free".
	Reported bool `json:"reported"`
}

// Add sums two costs. The zero Cost is the identity, so a run can accumulate
// into a fresh total and so a provider that reports no money at all leaves the
// total alone. Otherwise the sum is Reported only when both operands were: a
// total that quietly omits a leg must not present itself as a provider's
// figure. Costs in different currencies are added but marked not reported,
// since the resulting number belongs to neither.
func (c Cost) Add(d Cost) Cost {
	if c == (Cost{}) {
		return d
	}
	if d == (Cost{}) {
		return c
	}
	sum := Cost{
		Amount:   c.Amount.Add(d.Amount),
		Currency: c.Currency,
		Reported: c.Reported && d.Reported,
	}
	switch {
	case sum.Currency == "":
		sum.Currency = d.Currency
	case d.Currency != "" && d.Currency != sum.Currency:
		sum.Reported = false
	}
	return sum
}
