package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Quantity codec: JSON quantities are exact decimal strings over int64
// minimal units. The codec is deliberately strict — no floats, no exponents,
// no leading zeros, no "-0", no bare JSON numbers, no null, no overflow.
// See the Units doc comment in types.go (R04): Units implements
// Marshaler/Unmarshaler itself so the canonical spelling is enforced at the
// raw-token boundary, before any spelling information can be lost.

var (
	ErrQuantityFormat        = errors.New("quantity: invalid decimal string")
	ErrQuantityRange         = errors.New("quantity: value out of int64 range")
	ErrQuantityNotOutOfRange = errors.New("quantity: decimal is representable as int64")
)

const maxUnitsDecimal = "9223372036854775807"

// ParseUnits parses a strict canonical decimal string into Units.
func ParseUnits(s string) (Units, error) {
	if s == "" {
		return 0, ErrQuantityFormat
	}
	body := s
	neg := false
	if body[0] == '-' {
		neg = true
		body = body[1:]
		if body == "" {
			return 0, ErrQuantityFormat
		}
	}
	if body == "0" {
		if neg {
			return 0, ErrQuantityFormat // canonical zero is "0"; "-0" is rejected
		}
		return 0, nil
	}
	if body[0] == '0' {
		return 0, ErrQuantityFormat // no leading zeros ("010", "00")
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, ErrQuantityFormat // rejects '.', 'e', '+', spaces, unicode digits
		}
	}
	u, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrQuantityRange
	}
	return Units(u), nil
}

// ValidateOutOfRangeUnitsDecimal validates the optional exact report form for
// non-negative usage that cannot be represented by Units. The syntax is a
// canonical unsigned decimal integer, and the value must be strictly greater
// than MaxInt64. It deliberately does not convert through float64 or int64.
func ValidateOutOfRangeUnitsDecimal(s string) error {
	if s == "" || s[0] == '0' {
		return ErrQuantityFormat
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return ErrQuantityFormat
		}
	}
	if len(s) < len(maxUnitsDecimal) || (len(s) == len(maxUnitsDecimal) && s <= maxUnitsDecimal) {
		return ErrQuantityNotOutOfRange
	}
	return nil
}

// FormatUnits renders Units as a canonical decimal string ("0" for zero).
func FormatUnits(u Units) string {
	return strconv.FormatInt(int64(u), 10)
}

// MarshalJSON emits the canonical quoted decimal string.
func (u Units) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(FormatUnits(u))), nil
}

// UnmarshalJSON enforces the canonical quoted wire form. Accepted input is
// exactly one JSON string whose content is canonical decimal within int64;
// bare numbers, null, floats, exponents, "-0", leading zeros and overflow
// are rejected. (The former `,string` tag reached this method with the raw
// quoted token; untagged string fields reach it identically, so both usages
// are strict.)
func (u *Units) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("%w: quantity must be a JSON string in canonical decimal form, got %s", ErrQuantityFormat, s)
	}
	unquoted, err := strconv.Unquote(s)
	if err != nil {
		return fmt.Errorf("%w: %q is not a plain JSON string", ErrQuantityFormat, s)
	}
	parsed, err := ParseUnits(unquoted)
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

// checked arithmetic: each operation validates against the int64 bounds
// directly (R01) so no sign class, including the negative-overflow-to-zero
// wraparound and the MinInt64 negation, is mishandled.
func AddUnits(a, b Units) (Units, error) {
	sum := int64(a) + int64(b)
	if (b > 0 && sum < int64(a)) || (b < 0 && sum > int64(a)) {
		return 0, ErrQuantityRange
	}
	return Units(sum), nil
}

func SubUnits(a, b Units) (Units, error) {
	// a-b overflows iff it leaves [MinInt64, MaxInt64]; check via the bound
	// the subtrahend would have to cross instead of negating b (MinInt64 has
	// no in-range negation, and -MinInt64==MinInt64 would wrap silently).
	if b > 0 && int64(a) < math.MinInt64+int64(b) {
		return 0, ErrQuantityRange
	}
	if b < 0 && int64(a) > math.MaxInt64+int64(b) {
		return 0, ErrQuantityRange
	}
	return Units(int64(a) - int64(b)), nil
}

// MustParseUnits is for tests and examples only; production code must
// propagate errors from ParseUnits.
func MustParseUnits(s string) Units {
	u, err := ParseUnits(s)
	if err != nil {
		panic("MustParseUnits(" + s + "): " + err.Error())
	}
	return u
}

var _ json.Marshaler = Units(0)
var _ json.Unmarshaler = (*Units)(nil)
