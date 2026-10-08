// Package metering evaluates immutable, explicitly registered usage formulas.
// It has no clock, storage, credentials or network. The ledger alone decides
// admission and settlement; a calculator can only propose typed reports.
package metering

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"sort"

	"ant/server/internal/domain"
)

var (
	ErrDefinition = errors.New("metering: invalid definition")
	ErrSamples    = errors.New("metering: invalid or missing usage samples")
	ErrProjection = errors.New("metering: unsupported report projection")
	ErrFractional = errors.New("metering: fractional result requires explicit rounding")
)

const (
	Exact       = "exact"
	Ceiling     = "ceiling"
	MaxFormulas = 64
	MaxTerms    = 256
	MaxSamples  = 256
)

type Dimension struct {
	MetricID domain.ID `json:"metricId"`
	Unit     string    `json:"unit"`
}

// A rate is expressed in output minimal units per input minimal unit.
// Zero rates are valid, but their input still has to be reported explicitly.
type Term struct {
	Input       Dimension    `json:"input"`
	Numerator   domain.Units `json:"numerator"`
	Denominator domain.Units `json:"denominator"`
}

type Formula struct {
	Output       Dimension    `json:"output"`
	Fixed        domain.Units `json:"fixed"`
	Terms        []Term       `json:"terms"`
	Rounding     string       `json:"rounding"`
	Conservative bool         `json:"conservative"`
}

type Definition struct {
	ID       domain.ID `json:"id"`
	Revision domain.ID `json:"revision"`
	Formulas []Formula `json:"formulas"`
}

type Sample struct {
	Dimension    Dimension
	Amount       domain.Units
	Conservative bool
}

// Value preserves calculated integers even when the ledger cannot represent
// them as Units. It never uses float64 or saturating arithmetic.
type Value struct {
	Dimension    Dimension
	Decimal      string
	Conservative bool
}

type Calculator struct {
	definition Definition
	formulas   map[Dimension]Formula
	digest     string
}

func New(def Definition) (*Calculator, error) {
	if !validName(string(def.ID)) || !validName(string(def.Revision)) || len(def.Formulas) == 0 || len(def.Formulas) > MaxFormulas {
		return nil, ErrDefinition
	}
	def = cloneDefinition(def)
	outputs := make(map[domain.ID]bool, len(def.Formulas))
	inputs := make(map[domain.ID]string)
	formulas := make(map[Dimension]Formula, len(def.Formulas))
	termCount := 0
	for i := range def.Formulas {
		f := &def.Formulas[i]
		if !validDimension(f.Output) || outputs[f.Output.MetricID] || f.Fixed < 0 || (f.Rounding != Exact && f.Rounding != Ceiling) {
			return nil, ErrDefinition
		}
		outputs[f.Output.MetricID] = true
		seen := make(map[Dimension]bool, len(f.Terms))
		termCount += len(f.Terms)
		if termCount > MaxTerms {
			return nil, ErrDefinition
		}
		for _, t := range f.Terms {
			if !validDimension(t.Input) || seen[t.Input] || t.Numerator < 0 || t.Denominator <= 0 {
				return nil, ErrDefinition
			}
			if unit, ok := inputs[t.Input.MetricID]; ok && unit != t.Input.Unit {
				return nil, ErrDefinition
			}
			inputs[t.Input.MetricID] = t.Input.Unit
			seen[t.Input] = true
		}
		sort.Slice(f.Terms, func(i, j int) bool { return dimensionLess(f.Terms[i].Input, f.Terms[j].Input) })
		formulas[f.Output] = *f
	}
	sort.Slice(def.Formulas, func(i, j int) bool { return dimensionLess(def.Formulas[i].Output, def.Formulas[j].Output) })
	raw, err := json.Marshal(def)
	if err != nil {
		return nil, ErrDefinition
	}
	digest := sha256.Sum256(raw)
	return &Calculator{definition: def, formulas: formulas, digest: hex.EncodeToString(digest[:])}, nil
}

func (c *Calculator) Definition() Definition {
	if c == nil {
		return Definition{}
	}
	return cloneDefinition(c.definition)
}

// Digest covers identities, units, rates, rounding and conservative basis;
// registration order cannot change it. Persisted plan mapping revisions still
// have to be changed explicitly when an integration changes its calculator.
func (c *Calculator) Digest() string {
	if c == nil {
		return ""
	}
	return c.digest
}

func (c *Calculator) Evaluate(outputs []Dimension, samples []Sample) ([]Value, error) {
	if c == nil || len(outputs) == 0 || len(outputs) > MaxFormulas || len(samples) > MaxSamples {
		return nil, ErrProjection
	}
	byDimension := make(map[Dimension]Sample, len(samples))
	metrics := make(map[domain.ID]bool, len(samples))
	for _, s := range samples {
		if !validDimension(s.Dimension) || s.Amount < 0 || metrics[s.Dimension.MetricID] {
			return nil, ErrSamples
		}
		metrics[s.Dimension.MetricID] = true
		byDimension[s.Dimension] = s
	}
	seen := make(map[Dimension]bool, len(outputs))
	values := make([]Value, 0, len(outputs))
	for _, output := range outputs {
		formula, ok := c.formulas[output]
		if !ok || seen[output] {
			return nil, ErrProjection
		}
		seen[output] = true
		sum := new(big.Rat).SetInt64(int64(formula.Fixed))
		conservative := formula.Conservative
		for _, term := range formula.Terms {
			sample, ok := byDimension[term.Input]
			if !ok {
				return nil, ErrSamples
			}
			product := new(big.Int).Mul(big.NewInt(int64(sample.Amount)), big.NewInt(int64(term.Numerator)))
			sum.Add(sum, new(big.Rat).SetFrac(product, big.NewInt(int64(term.Denominator))))
			conservative = conservative || sample.Conservative
		}
		integer, remainder := new(big.Int), new(big.Int)
		integer.QuoRem(sum.Num(), sum.Denom(), remainder)
		if remainder.Sign() != 0 {
			if formula.Rounding == Exact {
				return nil, ErrFractional
			}
			integer.Add(integer, big.NewInt(1))
		}
		values = append(values, Value{Dimension: output, Decimal: integer.String(), Conservative: conservative})
	}
	return values, nil
}

// Project produces only the dimensions requested by the persisted plan, in
// plan order. A stale mapping, missing dimension or any calculation failure
// returns no partial reports. Timestamp and evidence are injected by the codec.
func (c *Calculator) Project(plan domain.ExecutionPlan, samples []Sample, at domain.Millis, evidence []domain.ID) ([]domain.MetricReport, error) {
	if c == nil || plan.ReportMappingRevision != c.definition.Revision || at < 0 || len(evidence) == 0 || len(evidence) > 64 {
		return nil, ErrProjection
	}
	seenEvidence := make(map[domain.ID]bool, len(evidence))
	for _, id := range evidence {
		if !validName(string(id)) || seenEvidence[id] {
			return nil, ErrProjection
		}
		seenEvidence[id] = true
	}
	outputs := make([]Dimension, 0, len(plan.Requirements))
	metricUnits := make(map[domain.ID]string, len(plan.Requirements))
	for _, req := range plan.Requirements {
		if unit, exists := metricUnits[req.MetricID]; exists {
			if unit != req.Unit {
				return nil, ErrProjection
			}
			continue
		}
		metricUnits[req.MetricID] = req.Unit
		outputs = append(outputs, Dimension{MetricID: req.MetricID, Unit: req.Unit})
	}
	values, err := c.Evaluate(outputs, samples)
	if err != nil {
		return nil, err
	}
	reports := make([]domain.MetricReport, 0, len(values))
	for _, value := range values {
		timestamp := at
		report := domain.MetricReport{MetricID: value.Dimension.MetricID, Unit: value.Dimension.Unit, Conservative: value.Conservative, ChargedAt: &timestamp, EvidenceRefs: slices.Clone(evidence)}
		amount, err := domain.ParseUnits(value.Decimal)
		if errors.Is(err, domain.ErrQuantityRange) {
			if domain.ValidateOutOfRangeUnitsDecimal(value.Decimal) != nil {
				return nil, ErrProjection
			}
			report.ActualOutOfRangeDecimal = value.Decimal
		} else if err != nil {
			return nil, ErrProjection
		} else {
			report.Actual = &amount
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func validName(s string) bool         { return len(s) > 0 && len(s) <= 256 }
func validDimension(d Dimension) bool { return validName(string(d.MetricID)) && validName(d.Unit) }
func dimensionLess(a, b Dimension) bool {
	if a.MetricID != b.MetricID {
		return a.MetricID < b.MetricID
	}
	return a.Unit < b.Unit
}
func cloneDefinition(d Definition) Definition {
	d.Formulas = slices.Clone(d.Formulas)
	for i := range d.Formulas {
		d.Formulas[i].Terms = slices.Clone(d.Formulas[i].Terms)
	}
	return d
}
