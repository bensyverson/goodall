package goodall

import (
	"encoding/json/v2"
	"testing"
)

func TestUsageAdd(t *testing.T) {
	a := Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Reasoning: 50}
	b := Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Reasoning: 5}
	want := Usage{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, Reasoning: 55}
	if got := a.Add(b); got != want {
		t.Errorf("Add = %+v, want %+v", got, want)
	}
	if got := a.Add(Usage{}); got != a {
		t.Errorf("Add(zero) = %+v, want %+v", got, a)
	}
	if got := (Usage{}).Add(b); got != b {
		t.Errorf("zero.Add = %+v, want %+v", got, b)
	}
}

// TestUsageTotalInput: research §1 — the prompt size a model was billed for is
// the sum of the three input-side fields, not Input alone.
func TestUsageTotalInput(t *testing.T) {
	cases := []struct {
		in   Usage
		want int
	}{
		{Usage{}, 0},
		{Usage{Input: 7}, 7},
		{Usage{Input: 7, CacheRead: 100, CacheWrite: 1000}, 1107},
		{Usage{Output: 9, Reasoning: 4}, 0},
	}
	for _, c := range cases {
		if got := c.in.TotalInput(); got != c.want {
			t.Errorf("%+v.TotalInput() = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestUsageJSON(t *testing.T) {
	cases := []struct {
		name string
		in   Usage
		want string
	}{
		{"zero", Usage{}, `{}`},
		{"full", Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Reasoning: 5},
			`{"input_tokens":1,"output_tokens":2,"cache_read_tokens":3,"cache_write_tokens":4,"reasoning_tokens":5}`},
		{"input only", Usage{Input: 1}, `{"input_tokens":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("marshal = %s, want %s", got, c.want)
			}
			var back Usage
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if back != c.in {
				t.Fatalf("round trip = %+v, want %+v", back, c.in)
			}
		})
	}
}

// TestCostAddReported: a total may only call itself a reported figure when
// every leg that contributed an amount came from a provider.
func TestCostAddReported(t *testing.T) {
	usd := func(t *testing.T, amount string, reported bool) Cost {
		t.Helper()
		return Cost{Amount: mustDecimal(t, amount), Currency: "USD", Reported: reported}
	}
	cases := []struct {
		name         string
		a, b         Cost
		wantAmount   string
		wantCurrency string
		wantReported bool
	}{
		{"both reported", usd(t, "0.0001", true), usd(t, "0.00002", true), "0.00012", "USD", true},
		{"one unreported", usd(t, "0.0001", true), usd(t, "0.00002", false), "0.00012", "USD", false},
		{"both unreported", usd(t, "0.0001", false), usd(t, "0.00002", false), "0.00012", "USD", false},
		{"zero accumulator", Cost{}, usd(t, "0.0001", true), "0.0001", "USD", true},
		{"reported plus zero", usd(t, "0.0001", true), Cost{}, "0.0001", "USD", true},
		{"two zeros", Cost{}, Cost{}, "0", "", false},
		{"provider that reports nothing", usd(t, "0.0001", true), Cost{Reported: false}, "0.0001", "USD", true},
		{"currency from the reported leg", Cost{Amount: mustDecimal(t, "0.5"), Reported: true}, usd(t, "0.25", true), "0.75", "USD", true},
		{"currency mismatch", usd(t, "1", true), Cost{Amount: mustDecimal(t, "1"), Currency: "EUR", Reported: true}, "2", "USD", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.a.Add(c.b)
			if got.Amount.String() != c.wantAmount {
				t.Errorf("amount = %s, want %s", got.Amount, c.wantAmount)
			}
			if got.Currency != c.wantCurrency {
				t.Errorf("currency = %q, want %q", got.Currency, c.wantCurrency)
			}
			if got.Reported != c.wantReported {
				t.Errorf("reported = %v, want %v", got.Reported, c.wantReported)
			}
		})
	}
}

func TestCostJSON(t *testing.T) {
	cases := []struct {
		name string
		in   Cost
		want string
	}{
		{"not reported", Cost{}, `{"amount":0,"reported":false}`},
		{"reported", Cost{Amount: mustDecimal(t, "0.0001234"), Currency: "USD", Reported: true},
			`{"amount":0.0001234,"currency":"USD","reported":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("marshal = %s, want %s", got, c.want)
			}
			var back Cost
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if back != c.in {
				t.Fatalf("round trip = %+v, want %+v", back, c.in)
			}
		})
	}
}
