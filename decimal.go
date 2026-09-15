package goodall

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// maxDecimalDigits bounds both the significant digits and the scale a decimal
// may be parsed with. Prices run to ten decimal places, so the limit is far
// above any real figure; it is here because a provider body is untrusted input
// and "1e2000000000" would otherwise ask String for two gigabytes of zeros.
const maxDecimalDigits = 4096

// errDecimalSyntax is the shape complaint shared by every bad literal.
var errDecimalSyntax = errors.New("not a decimal number")

// errDecimalRange reports a literal too long or too far from zero to hold.
var errDecimalRange = errors.New("too many digits")

// Decimal is an exact decimal number: money and per-token prices, held so that
// a sum of them is exact. Binary floating point cannot represent a price like
// 0.0000000375, and an agent loop adds one up thousands of times.
//
// The zero value is 0 and is ready to use. A Decimal is immutable, so it is
// safe to copy and to share between goroutines, and two Decimals hold the same
// number if and only if they are ==; Cmp orders them.
type Decimal struct {
	// text is the canonical expansion — no exponent, no superfluous zeros,
	// "" for zero — which is what lets == mean "the same number".
	text string
}

// ParseDecimal reads a decimal number: an optional sign, digits with an
// optional fractional part, and an optional power-of-ten exponent, as
// OpenRouter writes its prices ("0.0000000375") and JSON writes its numbers
// ("1e-7"). The value is kept exactly; the exponent is folded into the
// expansion, so String never returns one.
func ParseDecimal(s string) (Decimal, error) {
	unscaled, scale, err := parseDecimalParts(s)
	if err != nil {
		return Decimal{}, fmt.Errorf("goodall: %q: %w", s, err)
	}
	return newDecimal(unscaled, scale), nil
}

// parseDecimalParts splits a literal into an unscaled integer and a scale,
// where the value is unscaled × 10⁻ˢᶜᵃˡᵉ.
func parseDecimalParts(s string) (*big.Int, int, error) {
	rest := s
	neg := false
	if rest != "" && (rest[0] == '+' || rest[0] == '-') {
		neg = rest[0] == '-'
		rest = rest[1:]
	}
	mantissa, exponent := rest, ""
	if i := strings.IndexAny(rest, "eE"); i >= 0 {
		mantissa, exponent = rest[:i], rest[i+1:]
		if exponent == "" {
			return nil, 0, errDecimalSyntax
		}
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	if whole == "" && fraction == "" {
		return nil, 0, errDecimalSyntax
	}
	if !allDigits(whole) || !allDigits(fraction) {
		return nil, 0, errDecimalSyntax
	}
	if len(whole)+len(fraction) > maxDecimalDigits {
		return nil, 0, errDecimalRange
	}
	exp := 0
	if exponent != "" {
		v, err := strconv.Atoi(exponent)
		if err != nil {
			if errors.Is(err, strconv.ErrRange) {
				return nil, 0, errDecimalRange
			}
			return nil, 0, errDecimalSyntax
		}
		exp = v
	}
	unscaled, ok := new(big.Int).SetString(whole+fraction, 10)
	if !ok {
		return nil, 0, errDecimalSyntax
	}
	if neg {
		unscaled.Neg(unscaled)
	}
	scale := len(fraction) - exp
	if scale > maxDecimalDigits || scale < -maxDecimalDigits {
		return nil, 0, errDecimalRange
	}
	return unscaled, scale, nil
}

// allDigits reports whether s is made only of ASCII digits. The empty string
// qualifies: "5." and ".5" are both meant to parse.
func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// newDecimal canonicalizes unscaled × 10⁻ˢᶜᵃˡᵉ: trailing fractional zeros go,
// a negative scale is multiplied out, and zero is the zero value however it
// was spelled. Canonical form is the invariant behind ==.
func newDecimal(unscaled *big.Int, scale int) Decimal {
	if unscaled.Sign() == 0 {
		return Decimal{}
	}
	u := new(big.Int).Set(unscaled)
	ten := big.NewInt(10)
	quo, rem := new(big.Int), new(big.Int)
	for scale > 0 {
		quo.QuoRem(u, ten, rem)
		if rem.Sign() != 0 {
			break
		}
		u.Set(quo)
		scale--
	}
	if scale < 0 {
		u.Mul(u, powerOfTen(-scale))
		scale = 0
	}
	digits := u.String()
	var b strings.Builder
	if digits[0] == '-' {
		b.WriteByte('-')
		digits = digits[1:]
	}
	switch {
	case scale == 0:
		b.WriteString(digits)
	case len(digits) <= scale:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", scale-len(digits)))
		b.WriteString(digits)
	default:
		b.WriteString(digits[:len(digits)-scale])
		b.WriteByte('.')
		b.WriteString(digits[len(digits)-scale:])
	}
	return Decimal{text: b.String()}
}

// powerOfTen is 10ⁿ for n ≥ 0.
func powerOfTen(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// parts recovers the unscaled integer and the scale from the canonical text.
// A canonical Decimal always parses, so there is no error to return.
func (d Decimal) parts() (*big.Int, int) {
	if d.text == "" {
		return new(big.Int), 0
	}
	whole, fraction, _ := strings.Cut(d.text, ".")
	unscaled, ok := new(big.Int).SetString(whole+fraction, 10)
	if !ok {
		return new(big.Int), 0
	}
	return unscaled, len(fraction)
}

// align brings two unscaled integers to a common scale so they can be added or
// compared.
func align(au *big.Int, as int, bu *big.Int, bs int) (*big.Int, *big.Int, int) {
	switch {
	case as < bs:
		return new(big.Int).Mul(au, powerOfTen(bs-as)), bu, bs
	case bs < as:
		return au, new(big.Int).Mul(bu, powerOfTen(as-bs)), as
	}
	return au, bu, as
}

// Add returns d + e, exactly.
func (d Decimal) Add(e Decimal) Decimal {
	du, ds := d.parts()
	eu, es := e.parts()
	du, eu, scale := align(du, ds, eu, es)
	return newDecimal(new(big.Int).Add(du, eu), scale)
}

// Mul returns d × e, exactly.
func (d Decimal) Mul(e Decimal) Decimal {
	du, ds := d.parts()
	eu, es := e.parts()
	return newDecimal(new(big.Int).Mul(du, eu), ds+es)
}

// MulInt returns d × n, exactly: a per-token price times a token count.
func (d Decimal) MulInt(n int64) Decimal {
	du, ds := d.parts()
	return newDecimal(new(big.Int).Mul(du, big.NewInt(n)), ds)
}

// Cmp returns -1 if d < e, 0 if d == e, and +1 if d > e.
func (d Decimal) Cmp(e Decimal) int {
	du, ds := d.parts()
	eu, es := e.parts()
	du, eu, _ = align(du, ds, eu, es)
	return du.Cmp(eu)
}

// IsZero reports whether d is zero. It is also what makes a zero Decimal
// disappear under the json "omitzero" option.
func (d Decimal) IsZero() bool { return d.text == "" }

// String is the exact decimal expansion, without an exponent and without
// trailing zeros: "0" for zero, "0.0000000375" for a per-token price.
func (d Decimal) String() string {
	if d.text == "" {
		return "0"
	}
	return d.text
}

// MarshalJSONTo writes the exact expansion as a JSON number. It is written as
// a token rather than through a float64, which would round a per-token price
// away, and rather than as a string, which no other JSON reader would treat as
// a number.
func (d Decimal) MarshalJSONTo(enc *jsontext.Encoder) error {
	return enc.WriteValue(jsontext.Value(d.String()))
}

// UnmarshalJSONFrom reads a JSON number, or a JSON string holding one, because
// OpenRouter quotes its prices and leaves its costs bare. A null decodes as
// zero, which is how a provider says it charged nothing it can name.
func (d *Decimal) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	v, err := dec.ReadValue()
	if err != nil {
		return err
	}
	switch v.Kind() {
	case 'n':
		*d = Decimal{}
		return nil
	case '0':
		parsed, err := ParseDecimal(string(v))
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case '"':
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		parsed, err := ParseDecimal(s)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	default:
		return fmt.Errorf("goodall: cannot decode JSON %v into a Decimal", v.Kind())
	}
}
