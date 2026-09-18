package main

import (
	"slices"
	"testing"
)

// The probe's conclusion is a comparison of two numbers, so the arithmetic
// that produces them is worth a table test: an off-by-one in the pairing would
// report a noise floor of zero and turn run-to-run jitter into a finding.

func TestCrossSpread(t *testing.T) {
	cases := []struct {
		name    string
		a, b    []distribution
		want    float64
		wantKey string
	}{
		{
			name: "identical distributions spread by nothing",
			a:    []distribution{{"billing": 0.7, "technical": 0.3}},
			b:    []distribution{{"billing": 0.7, "technical": 0.3}},
		},
		{
			name:    "the widest key wins",
			a:       []distribution{{"billing": 0.70, "technical": 0.20, "sales": 0.10}},
			b:       []distribution{{"billing": 0.50, "technical": 0.35, "sales": 0.15}},
			want:    0.20,
			wantKey: "billing",
		},
		{
			name:    "every pair is compared, not just the first",
			a:       []distribution{{"billing": 0.70}, {"billing": 0.50}},
			b:       []distribution{{"billing": 0.65}, {"billing": 0.90}},
			want:    0.40,
			wantKey: "billing",
		},
		{
			name:    "a key missing on one side counts as zero probability",
			a:       []distribution{{"billing": 0.5, "technical": 0.5}},
			b:       []distribution{{"billing": 0.4, "sales": 0.6}},
			want:    0.6,
			wantKey: "sales",
		},
		{
			name: "no samples spread by nothing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, key := crossSpread(tc.a, tc.b)
			if !nearly(got, tc.want) {
				t.Errorf("crossSpread = %v, want %v", got, tc.want)
			}
			if tc.wantKey != "" && key != tc.wantKey {
				t.Errorf("crossSpread key = %q, want %q", key, tc.wantKey)
			}
		})
	}
}

func TestSpread(t *testing.T) {
	cases := []struct {
		name    string
		dists   []distribution
		want    float64
		wantKey string
	}{
		{
			name:  "one sample has no spread",
			dists: []distribution{{"billing": 0.7, "technical": 0.3}},
		},
		{
			name: "the same sample twice has no spread",
			dists: []distribution{
				{"billing": 0.7, "technical": 0.3},
				{"billing": 0.7, "technical": 0.3},
			},
		},
		{
			name: "the widest pair over three samples",
			dists: []distribution{
				{"billing": 0.70, "technical": 0.20, "sales": 0.10},
				{"billing": 0.66, "technical": 0.22, "sales": 0.12},
				{"billing": 0.55, "technical": 0.30, "sales": 0.15},
			},
			want:    0.15,
			wantKey: "billing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, key := spread(tc.dists)
			if !nearly(got, tc.want) {
				t.Errorf("spread = %v, want %v", got, tc.want)
			}
			if tc.wantKey != "" && key != tc.wantKey {
				t.Errorf("spread key = %q, want %q", key, tc.wantKey)
			}
		})
	}
}

// nearly compares two probabilities at a tolerance well below anything the
// probe would call an effect, so float noise in the table does not fail it.
func nearly(got, want float64) bool {
	d := got - want
	return d < 1e-9 && d > -1e-9
}

// TestSentOrder covers the wire check itself: the probe's whole claim rests on
// reading the option order back out of the bytes that were sent, and a decoder
// that silently returned author order regardless would make the run meaningless.
func TestSentOrder(t *testing.T) {
	body := []byte(`{"state":{"subject":"s","body":"b","plan":"growth"},"model":"jev-1.13.0",` +
		`"questions":{"department":{"type":"choice","instructions":"Which team?",` +
		`"criteria":{"sales":"c","technical":"b","billing":"a"}}}}`)
	got, err := sentOrder(body)
	if err != nil {
		t.Fatalf("sentOrder: %v", err)
	}
	want := []string{"sales", "technical", "billing"}
	if !slices.Equal(got, want) {
		t.Errorf("sentOrder = %v, want %v", got, want)
	}
}

// TestSentOrderWithoutTheQuestion keeps a body the probe cannot interpret from
// passing as a verified one.
func TestSentOrderWithoutTheQuestion(t *testing.T) {
	body := []byte(`{"model":"jev-1.13.0","questions":{"other":{"type":"noul","instructions":"Urgent?"}}}`)
	if _, err := sentOrder(body); err == nil {
		t.Error("sentOrder accepted a body carrying no choice question")
	}
}
