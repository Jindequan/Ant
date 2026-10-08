package domain

import (
	"errors"
	"math"
	"testing"
)

// Boundary tests for checked arithmetic (review P2-1): negating MinInt64 has
// no in-range counterpart and must error instead of wrapping silently.
//
// R01 rework note (2026-10-02): the former assertion demanded that
// SubUnits(MinInt64, MinInt64) overflow — that expectation encoded the very
// defect R01 proved (MinInt64-MinInt64 == 0 is exactly representable, and the
// pre-fix implementation wrongly rejected it). Updated to the corrected
// contract; authorized by the integrator. The full-coverage big.Int oracle
// lives in quantity_oracle_test.go.
func TestSubUnitsMinIntBoundary(t *testing.T) {
	if _, err := SubUnits(0, Units(math.MinInt64)); !errors.Is(err, ErrQuantityRange) {
		t.Fatalf("SubUnits(0, MinInt64) must be out of range, got %v", err)
	}
	if u, err := SubUnits(Units(math.MinInt64), Units(math.MinInt64)); err != nil || u != 0 {
		t.Fatalf("SubUnits(MinInt64, MinInt64) is exactly representable: want 0,nil, got %d,%v", u, err)
	}
	if u, err := SubUnits(5, 3); err != nil || u != 2 {
		t.Fatalf("SubUnits(5,3) = %d, %v", u, err)
	}
	if u, err := SubUnits(0, -7); err != nil || u != 7 {
		t.Fatalf("SubUnits(0,-7) = %d, %v", u, err)
	}
}

func TestAddUnitsOverflow(t *testing.T) {
	if _, err := AddUnits(Units(math.MaxInt64), 1); !errors.Is(err, ErrQuantityRange) {
		t.Fatalf("AddUnits(MaxInt64, 1) must overflow, got %v", err)
	}
	if _, err := AddUnits(Units(math.MinInt64), -1); !errors.Is(err, ErrQuantityRange) {
		t.Fatalf("AddUnits(MinInt64, -1) must overflow, got %v", err)
	}
	if u, err := AddUnits(3, 4); err != nil || u != 7 {
		t.Fatalf("AddUnits(3,4) = %d, %v", u, err)
	}
}

// ParseUnits canonical-form rules (codec strictness at the rule boundary).
func TestParseUnitsStrict(t *testing.T) {
	ok := map[string]Units{"0": 0, "42": 42, "-5": -5, "9223372036854775807": 9223372036854775807, "-9223372036854775808": -9223372036854775808}
	for in, want := range ok {
		u, err := ParseUnits(in)
		if err != nil || u != want {
			t.Fatalf("ParseUnits(%q) = %d, %v; want %d", in, u, err, want)
		}
	}
	bad := []string{"", "-0", "01", "+1", "1.0", "1e3", " 1", "1 ", "１", "99999999999999999999", "-9223372036854775809"}
	for _, in := range bad {
		if _, err := ParseUnits(in); err == nil {
			t.Fatalf("ParseUnits(%q) must fail", in)
		}
	}
	if FormatUnits(-9223372036854775808) != "-9223372036854775808" {
		t.Fatal("FormatUnits min boundary")
	}
}

func TestValidateOutOfRangeUnitsDecimal(t *testing.T) {
	for _, value := range []string{"9223372036854775808", "100000000000000000000000000000000000000"} {
		if err := ValidateOutOfRangeUnitsDecimal(value); err != nil {
			t.Fatalf("ValidateOutOfRangeUnitsDecimal(%q): %v", value, err)
		}
	}
	for _, tc := range []struct {
		value string
		want  error
	}{
		{"", ErrQuantityFormat}, {"0", ErrQuantityFormat}, {"01", ErrQuantityFormat}, {"+9223372036854775808", ErrQuantityFormat},
		{"-9223372036854775809", ErrQuantityFormat}, {"9.223372036854775808e18", ErrQuantityFormat},
		{"9223372036854775807", ErrQuantityNotOutOfRange}, {"42", ErrQuantityNotOutOfRange},
	} {
		if err := ValidateOutOfRangeUnitsDecimal(tc.value); !errors.Is(err, tc.want) {
			t.Fatalf("ValidateOutOfRangeUnitsDecimal(%q) = %v, want %v", tc.value, err, tc.want)
		}
	}
}
