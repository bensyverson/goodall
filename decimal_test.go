package goodall

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

// mustDecimal parses a decimal a test states as a literal.
func mustDecimal(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, err)
	}
	return d
}

func TestParseDecimalExactRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0.0000000375", "0.0000000375"}, // an OpenRouter per-token price
		{"3", "3"},
		{"0", "0"},
		{"-0", "0"},
		{"0.0", "0"},
		{"00.5000", "0.5"},
		{"+1.5", "1.5"},
		{"-0.5", "-0.5"},
		{"1e-7", "0.0000001"},
		{"3E2", "300"},
		{"1.5e3", "1500"},
		{"0.1e1", "1"},
		{"12345678901234567890.12345", "12345678901234567890.12345"},
	}
	for _, c := range cases {
		d, err := ParseDecimal(c.in)
		if err != nil {
			t.Errorf("ParseDecimal(%q): %v", c.in, err)
			continue
		}
		if got := d.String(); got != c.want {
			t.Errorf("ParseDecimal(%q).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseDecimalRejectsGarbage(t *testing.T) {
	bad := []string{"", " ", "abc", "1.2.3", "0x10", " 1", "1 ", "1,5", "1e", "1e+", "+-1", ".", "-", "1e999999999999999999", strings.Repeat("9", 100000)}
	for _, s := range bad {
		if _, err := ParseDecimal(s); err == nil {
			t.Errorf("ParseDecimal(%q) succeeded, want an error", s)
		}
	}
}

// TestDecimalZeroValueIsUsable: the zero Decimal is 0, prints as 0, and is the
// identity for Add, so a struct holding one needs no constructor.
func TestDecimalZeroValueIsUsable(t *testing.T) {
	var d Decimal
	if !d.IsZero() {
		t.Error("zero Decimal reports IsZero() = false")
	}
	if got := d.String(); got != "0" {
		t.Errorf("zero Decimal.String() = %q, want %q", got, "0")
	}
	if d != mustDecimal(t, "0") {
		t.Error(`zero Decimal != ParseDecimal("0"); the canonical form is not unique`)
	}
	if d != mustDecimal(t, "-0.000") {
		t.Error(`zero Decimal != ParseDecimal("-0.000"); the canonical form is not unique`)
	}
	x := mustDecimal(t, "0.25")
	if got := d.Add(x); got != x {
		t.Errorf("zero.Add(%v) = %v, want %v", x, got, x)
	}
	if got := x.Add(d); got != x {
		t.Errorf("%v.Add(zero) = %v, want %v", x, got, x)
	}
	if d.Cmp(x) != -1 {
		t.Errorf("zero.Cmp(%v) = %d, want -1", x, d.Cmp(x))
	}
}

// TestDecimalAddIsExact is the reason Decimal exists: binary floating point
// gets 0.1 + 0.2 wrong and money may not be wrong.
func TestDecimalAddIsExact(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"0.1", "0.2", "0.3"},
		{"0.0000000375", "0.0000000375", "0.000000075"},
		{"1", "-1", "0"},
		{"-0.5", "0.25", "-0.25"},
		{"12345678901234567890", "1", "12345678901234567891"},
		{"0.007", "3", "3.007"},
	}
	for _, c := range cases {
		got := mustDecimal(t, c.a).Add(mustDecimal(t, c.b)).String()
		if got != c.want {
			t.Errorf("%s + %s = %s, want %s", c.a, c.b, got, c.want)
		}
		if want := mustDecimal(t, c.want); mustDecimal(t, c.a).Add(mustDecimal(t, c.b)) != want {
			t.Errorf("%s + %s is not == to the canonical %s", c.a, c.b, c.want)
		}
	}
}

func TestDecimalMul(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"0.0000000375", "2", "0.000000075"},
		{"0.1", "0.2", "0.02"},
		{"-1.5", "4", "-6"},
		{"0", "12.5", "0"},
	}
	for _, c := range cases {
		got := mustDecimal(t, c.a).Mul(mustDecimal(t, c.b)).String()
		if got != c.want {
			t.Errorf("%s * %s = %s, want %s", c.a, c.b, got, c.want)
		}
	}
}

// TestDecimalMulInt prices a token count at a per-token price, which is the
// only arithmetic a provider actually performs.
func TestDecimalMulInt(t *testing.T) {
	cases := []struct {
		price string
		n     int64
		want  string
	}{
		{"0.0000000375", 1_000_000, "0.0375"},
		{"0.0000000375", 0, "0"},
		{"0.000003", 12_345, "0.037035"},
		{"0.0000000375", -8, "-0.0000003"},
	}
	for _, c := range cases {
		got := mustDecimal(t, c.price).MulInt(c.n).String()
		if got != c.want {
			t.Errorf("%s * %d = %s, want %s", c.price, c.n, got, c.want)
		}
	}
}

func TestDecimalCmp(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1", "0.2", -1},
		{"0.2", "0.1", 1},
		{"0.30", "0.3", 0},
		{"-1", "0", -1},
		{"0", "0", 0},
		{"10", "9.999999", 1},
	}
	for _, c := range cases {
		if got := mustDecimal(t, c.a).Cmp(mustDecimal(t, c.b)); got != c.want {
			t.Errorf("%s.Cmp(%s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestDecimalIsZero(t *testing.T) {
	for _, s := range []string{"0", "-0", "0.000", "0e10"} {
		if !mustDecimal(t, s).IsZero() {
			t.Errorf("ParseDecimal(%q).IsZero() = false, want true", s)
		}
	}
	for _, s := range []string{"0.0000000001", "-0.1", "3"} {
		if mustDecimal(t, s).IsZero() {
			t.Errorf("ParseDecimal(%q).IsZero() = true, want false", s)
		}
	}
}

// TestDecimalMarshalsAsNumberToken: the wire carries the exact decimal text as
// a JSON number, never a float64 that would round it.
func TestDecimalMarshalsAsNumberToken(t *testing.T) {
	type envelope struct {
		D Decimal `json:"d"`
	}
	cases := []struct {
		in   string
		want string
	}{
		{"0.0000000375", `{"d":0.0000000375}`},
		{"3", `{"d":3}`},
		{"-0.5", `{"d":-0.5}`},
		{"0", `{"d":0}`},
		{"12345678901234567890.12345", `{"d":12345678901234567890.12345}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(envelope{mustDecimal(t, c.in)})
		if err != nil {
			t.Fatalf("marshal %s: %v", c.in, err)
		}
		if string(got) != c.want {
			t.Errorf("marshal %s = %s, want %s", c.in, got, c.want)
		}
		var back envelope
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", got, err)
		}
		if back.D != mustDecimal(t, c.in) {
			t.Errorf("round trip of %s = %s", c.in, back.D)
		}
	}
	var zero envelope
	got, err := json.Marshal(zero)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"d":0}` {
		t.Errorf("zero Decimal marshals as %s, want {\"d\":0}", got)
	}
}

// TestDecimalUnmarshalsFromNumberOrString: OpenRouter prices arrive as strings
// and its usage cost as a number, so both decode.
func TestDecimalUnmarshalsFromNumberOrString(t *testing.T) {
	type envelope struct {
		D Decimal `json:"d"`
	}
	cases := []struct {
		wire string
		want string
	}{
		{`{"d":"0.0000000375"}`, "0.0000000375"},
		{`{"d":0.0000000375}`, "0.0000000375"},
		{`{"d":"3"}`, "3"},
		{`{"d":1e-7}`, "0.0000001"},
		{`{"d":null}`, "0"},
		{`{}`, "0"},
	}
	for _, c := range cases {
		var got envelope
		if err := json.Unmarshal([]byte(c.wire), &got); err != nil {
			t.Errorf("unmarshal %s: %v", c.wire, err)
			continue
		}
		if got.D.String() != c.want {
			t.Errorf("unmarshal %s = %s, want %s", c.wire, got.D, c.want)
		}
	}
	for _, wire := range []string{`{"d":true}`, `{"d":{}}`, `{"d":"abc"}`, `{"d":[]}`} {
		var got envelope
		if err := json.Unmarshal([]byte(wire), &got); err == nil {
			t.Errorf("unmarshal %s succeeded (got %s), want an error", wire, got.D)
		}
	}
}
