package metering

import (
	"errors"
	"math"
	"math/big"
	"sync"
	"testing"

	"ant/server/internal/domain"
)

var (
	inputA = Dimension{MetricID: "input", Unit: "token"}
	inputB = Dimension{MetricID: "output", Unit: "token"}
	cost   = Dimension{MetricID: "charge", Unit: "usd_micro"}
)

func testDefinition() Definition {
	return Definition{ID: "model-tariff", Revision: "mapping-v1", Formulas: []Formula{{
		Output: cost, Rounding: Ceiling, Conservative: true,
		Terms: []Term{{Input: inputA, Numerator: 1, Denominator: 3}, {Input: inputB, Numerator: 1, Denominator: 6}},
	}}}
}

func TestRoundOnlyAfterAllTermsAndPreserveBasis(t *testing.T) {
	c, err := New(testDefinition())
	if err != nil {
		t.Fatal(err)
	}
	values, err := c.Evaluate([]Dimension{cost}, []Sample{{Dimension: inputA, Amount: 1}, {Dimension: inputB, Amount: 1}})
	if err != nil || len(values) != 1 || values[0].Decimal != "1" || !values[0].Conservative {
		t.Fatalf("values=%+v err=%v", values, err)
	}
	def := testDefinition()
	def.Formulas[0].Conservative = false
	def.Formulas[0].Rounding = Exact
	c, err = New(def)
	if err != nil {
		t.Fatal(err)
	}
	if values, err := c.Evaluate([]Dimension{cost}, []Sample{{Dimension: inputA, Amount: 1}, {Dimension: inputB, Amount: 1}}); values != nil || !errors.Is(err, ErrFractional) {
		t.Fatalf("fraction silently rounded: %v %v", values, err)
	}
	values, err = c.Evaluate([]Dimension{cost}, []Sample{{Dimension: inputA, Amount: 3, Conservative: true}, {Dimension: inputB, Amount: 0}})
	if err != nil || !values[0].Conservative || values[0].Decimal != "1" {
		t.Fatalf("input basis lost: %+v %v", values, err)
	}
}

func TestIntermediateOverflowAndFinalOverflow(t *testing.T) {
	for _, tc := range []struct {
		numerator, denominator domain.Units
		want                   string
		raw                    bool
	}{
		{math.MaxInt64, math.MaxInt64, "9223372036854775807", false},
		{2, 1, "18446744073709551614", true},
	} {
		def := Definition{ID: "overflow", Revision: "mapping-v1", Formulas: []Formula{{Output: cost, Rounding: Exact, Terms: []Term{{Input: inputA, Numerator: tc.numerator, Denominator: tc.denominator}}}}}
		c, err := New(def)
		if err != nil {
			t.Fatal(err)
		}
		plan := domain.ExecutionPlan{ReportMappingRevision: "mapping-v1", Requirements: []domain.ResourceRequirement{{MetricID: cost.MetricID, Unit: cost.Unit}}}
		reports, err := c.Project(plan, []Sample{{Dimension: inputA, Amount: math.MaxInt64}}, 5, []domain.ID{"provider/request"})
		if err != nil || len(reports) != 1 {
			t.Fatalf("reports=%+v err=%v", reports, err)
		}
		if tc.raw {
			if reports[0].Actual != nil || reports[0].ActualOutOfRangeDecimal != tc.want {
				t.Fatalf("overflow lost: %+v", reports[0])
			}
		} else if reports[0].Actual == nil || domain.FormatUnits(*reports[0].Actual) != tc.want || reports[0].ActualOutOfRangeDecimal != "" {
			t.Fatalf("representable quotient lost: %+v", reports[0])
		}
	}
}

func TestSamplesFailClosedIncludingFreeRates(t *testing.T) {
	def := testDefinition()
	def.Formulas[0].Terms[0].Numerator = 0
	c, err := New(def)
	if err != nil {
		t.Fatal(err)
	}
	for name, samples := range map[string][]Sample{
		"missing free input": {{Dimension: inputB, Amount: 0}},
		"wrong unit":         {{Dimension: Dimension{MetricID: inputA.MetricID, Unit: "second"}, Amount: 0}, {Dimension: inputB, Amount: 0}},
		"negative":           {{Dimension: inputA, Amount: -1}, {Dimension: inputB}},
		"duplicate":          {{Dimension: inputA}, {Dimension: inputA}, {Dimension: inputB}},
	} {
		t.Run(name, func(t *testing.T) {
			if values, err := c.Evaluate([]Dimension{cost}, samples); values != nil || !errors.Is(err, ErrSamples) {
				t.Fatalf("values=%v err=%v", values, err)
			}
		})
	}
	values, err := c.Evaluate([]Dimension{cost}, []Sample{{Dimension: inputA}, {Dimension: inputB}})
	if err != nil || values[0].Decimal != "0" {
		t.Fatalf("explicit zero rejected: %v %v", values, err)
	}
}

func TestDefinitionRejectsAmbiguityAndBudgets(t *testing.T) {
	for name, mutate := range map[string]func(*Definition){
		"revision":         func(d *Definition) { d.Revision = "" },
		"zero divisor":     func(d *Definition) { d.Formulas[0].Terms[0].Denominator = 0 },
		"negative rate":    func(d *Definition) { d.Formulas[0].Terms[0].Numerator = -1 },
		"negative fixed":   func(d *Definition) { d.Formulas[0].Fixed = -1 },
		"rounding":         func(d *Definition) { d.Formulas[0].Rounding = "nearest" },
		"duplicate input":  func(d *Definition) { d.Formulas[0].Terms[1] = d.Formulas[0].Terms[0] },
		"duplicate output": func(d *Definition) { d.Formulas = append(d.Formulas, d.Formulas[0]) },
		"ambiguous output unit": func(d *Definition) {
			f := d.Formulas[0]
			f.Output.Unit = "cny_micro"
			d.Formulas = append(d.Formulas, f)
		},
		"ambiguous input unit": func(d *Definition) {
			d.Formulas[0].Terms[1].Input = Dimension{MetricID: inputA.MetricID, Unit: "second"}
		},
		"too many rules": func(d *Definition) { d.Formulas = make([]Formula, MaxFormulas+1) },
		"too many terms": func(d *Definition) { d.Formulas[0].Terms = make([]Term, MaxTerms+1) },
	} {
		t.Run(name, func(t *testing.T) {
			def := testDefinition()
			mutate(&def)
			if c, err := New(def); c != nil || !errors.Is(err, ErrDefinition) {
				t.Fatalf("calculator=%v err=%v", c, err)
			}
		})
	}
}

func TestProjectionRevisionUnitsEvidenceAndNoPartialOutput(t *testing.T) {
	c, err := New(testDefinition())
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ExecutionPlan{ReportMappingRevision: "mapping-v1", Requirements: []domain.ResourceRequirement{{MetricID: cost.MetricID, Unit: cost.Unit}, {MetricID: cost.MetricID, Unit: cost.Unit}}}
	samples := []Sample{{Dimension: inputA, Amount: 3}, {Dimension: inputB}}
	evidence := []domain.ID{"request-1"}
	reports, err := c.Project(plan, samples, 7, evidence)
	if err != nil || len(reports) != 1 || *reports[0].Actual != 1 || *reports[0].ChargedAt != 7 {
		t.Fatalf("shared dimension projection=%+v err=%v", reports, err)
	}
	evidence[0] = "changed"
	if reports[0].EvidenceRefs[0] != "request-1" {
		t.Fatal("aliased evidence")
	}
	plan.Requirements[1].Unit = "cny_micro"
	if reports, err := c.Project(plan, samples, 7, evidence); reports != nil || err == nil {
		t.Fatal("mixed currencies accepted")
	}
	plan.Requirements = plan.Requirements[:1]
	plan.ReportMappingRevision = "stale"
	if reports, err := c.Project(plan, samples, 7, evidence); reports != nil || err == nil {
		t.Fatal("stale mapping accepted")
	}
	plan.ReportMappingRevision = "mapping-v1"
	for _, ids := range [][]domain.ID{nil, {""}, {"same", "same"}} {
		if reports, err := c.Project(plan, samples, 7, ids); reports != nil || err == nil {
			t.Fatal("invalid evidence accepted")
		}
	}
	if values, err := c.Evaluate([]Dimension{cost, {MetricID: "missing", Unit: "request"}}, samples); values != nil || err == nil {
		t.Fatal("partial output returned")
	}
}

func TestImmutableDefinitionStableDigestAndConcurrentEvaluation(t *testing.T) {
	def := testDefinition()
	c, err := New(def)
	if err != nil {
		t.Fatal(err)
	}
	reversed := testDefinition()
	reversed.Formulas[0].Terms[0], reversed.Formulas[0].Terms[1] = reversed.Formulas[0].Terms[1], reversed.Formulas[0].Terms[0]
	c2, err := New(reversed)
	if err != nil || c2.Digest() != c.Digest() {
		t.Fatal("order changed digest", err)
	}
	def.Formulas[0].Terms[0].Numerator = 99
	copy := c.Definition()
	copy.Formulas[0].Terms[0].Numerator = 77
	if c.Definition().Formulas[0].Terms[0].Numerator != 1 {
		t.Fatal("definition aliases input/output")
	}
	changed, err := New(def)
	if err != nil || changed.Digest() == c.Digest() {
		t.Fatal("tariff change not reflected", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				values, err := c.Evaluate([]Dimension{cost}, []Sample{{Dimension: inputA, Amount: 3}, {Dimension: inputB}})
				if err != nil || values[0].Decimal != "1" {
					t.Error("concurrent result", values, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// Independent integer oracle for rational products and once-only ceiling.
func FuzzRationalMetering(f *testing.F) {
	f.Add(int64(1), int64(3), int64(1), int64(6), int64(1), int64(1))
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64), int64(7), int64(11), int64(math.MaxInt64), int64(13))
	f.Fuzz(func(t *testing.T, a, b, c, d, x, y int64) {
		if a < 0 || c < 0 || x < 0 || y < 0 || b <= 0 || d <= 0 {
			return
		}
		def := testDefinition()
		def.Formulas[0].Terms = []Term{{Input: inputA, Numerator: domain.Units(a), Denominator: domain.Units(b)}, {Input: inputB, Numerator: domain.Units(c), Denominator: domain.Units(d)}}
		calc, err := New(def)
		if err != nil {
			t.Fatal(err)
		}
		values, err := calc.Evaluate([]Dimension{cost}, []Sample{{Dimension: inputA, Amount: domain.Units(x)}, {Dimension: inputB, Amount: domain.Units(y)}})
		if err != nil {
			t.Fatal(err)
		}
		n := new(big.Int).Mul(big.NewInt(x), big.NewInt(a))
		n.Mul(n, big.NewInt(d))
		other := new(big.Int).Mul(big.NewInt(y), big.NewInt(c))
		other.Mul(other, big.NewInt(b))
		n.Add(n, other)
		den := new(big.Int).Mul(big.NewInt(b), big.NewInt(d))
		n.Add(n, new(big.Int).Sub(den, big.NewInt(1)))
		n.Quo(n, den)
		if values[0].Decimal != n.String() {
			t.Fatalf("got %s want %s", values[0].Decimal, n)
		}
	})
}
