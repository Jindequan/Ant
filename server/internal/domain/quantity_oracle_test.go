package domain

// R01 oracle: AddUnits/SubUnits are checked against math/big as the reference
// implementation. The review requirement is a whole-family property, not a
// list of specials: for ANY int64 pair the operation must succeed with the
// exact big-integer result when that result is representable in int64, and
// must fail with ErrQuantityRange (leaving the inputs untouched) when it is
// not. The edge matrix {Min,-1,0,1,Max}², deterministic pseudo-random pairs
// (fixed seed) and boundary-biased pairs all run through the same oracle.

import (
	"errors"
	"math"
	"math/big"
	"math/rand"
	"testing"
)

var (
	bigMin = new(big.Int).SetInt64(math.MinInt64)
	bigMax = new(big.Int).SetInt64(math.MaxInt64)
)

func inInt64Range(z *big.Int) bool {
	return z.Cmp(bigMin) >= 0 && z.Cmp(bigMax) <= 0
}

// runOracle asserts the full R01 contract for one operand pair and one
// operation ("add" or "sub") against the math/big reference result.
func runOracle(t *testing.T, label string, a, b Units, subtract bool) {
	t.Helper()

	want := new(big.Int).SetInt64(int64(a))
	if subtract {
		want.Sub(want, new(big.Int).SetInt64(int64(b)))
	} else {
		want.Add(want, new(big.Int).SetInt64(int64(b)))
	}

	got, err := SubUnits(a, b)
	if !subtract {
		got, err = AddUnits(a, b)
	}

	op := "AddUnits"
	if subtract {
		op = "SubUnits"
	}
	if inInt64Range(want) {
		if err != nil {
			t.Fatalf("%s: %s(%d,%d)=%s is representable in int64 but got error %v", label, op, int64(a), int64(b), want, err)
		}
		if got != Units(want.Int64()) {
			t.Fatalf("%s: %s(%d,%d) = %d, want exact big result %d", label, op, int64(a), int64(b), int64(got), want)
		}
	} else if !errors.Is(err, ErrQuantityRange) {
		t.Fatalf("%s: %s(%d,%d)=%s overflows int64 but got err=%v, want ErrQuantityRange", label, op, int64(a), int64(b), want, err)
	}
	// Units are plain int64 values passed by value, so the failure path cannot
	// mutate caller state by construction; no defensive copy is possible here.
}

// TestQuantityOracleEdgeMatrix covers every combination of the critical
// boundary operands {Min, Max, -1, 0, 1} for both operations: 50 oracle cases.
func TestQuantityOracleEdgeMatrix(t *testing.T) {
	edges := []Units{
		Units(math.MinInt64),
		-1,
		0,
		1,
		Units(math.MaxInt64),
	}
	for _, a := range edges {
		for _, b := range edges {
			runOracle(t, "edge", a, b, false)
			runOracle(t, "edge", a, b, true)
		}
	}
}

// TestQuantityOracleRandomPairs runs 500 deterministic pseudo-random full-
// range int64 pairs (fixed seed) through the oracle for both operations.
func TestQuantityOracleRandomPairs(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261002)) // fixed seed: reproducible failures
	for i := 0; i < 500; i++ {
		a := Units(int64(rnd.Uint64()))
		b := Units(int64(rnd.Uint64()))
		runOracle(t, "random", a, b, false)
		runOracle(t, "random", a, b, true)
	}
}

// TestQuantityOracleBoundaryBiasedPairs biases operands near MinInt64 and
// MaxInt64 (±1000), where wraparound shortcuts are most likely to hide.
func TestQuantityOracleBoundaryBiasedPairs(t *testing.T) {
	rnd := rand.New(rand.NewSource(42)) // fixed seed
	biased := func() Units {
		var u Units
		switch rnd.Intn(4) {
		case 0:
			u = Units(math.MinInt64) + Units(rnd.Intn(1000))
		case 1:
			u = Units(math.MaxInt64) - Units(rnd.Intn(1000))
		case 2:
			u = -Units(rnd.Intn(1000))
		default:
			u = Units(rnd.Intn(1000))
		}
		return u
	}
	for i := 0; i < 200; i++ {
		a, b := biased(), biased()
		runOracle(t, "biased", a, b, false)
		runOracle(t, "biased", a, b, true)
	}
}

// TestQuantityOracleKnownCounterexamples pins the three concrete defects from
// R01 with their exact corrected expectations (the whole-family oracle above
// subsumes them; these named cases are the regression anchors).
func TestQuantityOracleKnownCounterexamples(t *testing.T) {
	t.Run("R01-add-negative-overflow-wraps-to-zero", func(t *testing.T) {
		// MinInt64 + MinInt64 = -2^64: not representable. The defective
		// implementation returned 0,nil because the `>0` sign check missed the
		// negative overflow that wraps onto zero.
		got, err := AddUnits(Units(math.MinInt64), Units(math.MinInt64))
		if !errors.Is(err, ErrQuantityRange) || got != 0 {
			t.Fatalf("AddUnits(Min,Min): want (0,ErrQuantityRange), got (%d,%v)", int64(got), err)
		}
	})
	t.Run("R01-sub-legal-maximum", func(t *testing.T) {
		// -1 - MinInt64 = MaxInt64 exactly: legal. The defective
		// implementation reported overflow ("cannot negate MinInt64" was
		// treated as "result must overflow").
		got, err := SubUnits(-1, Units(math.MinInt64))
		if err != nil || got != Units(math.MaxInt64) {
			t.Fatalf("SubUnits(-1,Min): want (MaxInt64,nil), got (%d,%v)", int64(got), err)
		}
	})
	t.Run("R01-sub-minimum-minus-itself", func(t *testing.T) {
		// MinInt64 - MinInt64 = 0 exactly: legal.
		got, err := SubUnits(Units(math.MinInt64), Units(math.MinInt64))
		if err != nil || got != 0 {
			t.Fatalf("SubUnits(Min,Min): want (0,nil), got (%d,%v)", int64(got), err)
		}
	})
}
