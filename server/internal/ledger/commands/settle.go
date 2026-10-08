package commands

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrSettlementInvalid = errors.New("ledger settle: invalid report")
var ErrSettlementConflict = errors.New("ledger settle: conflicting or late report retained; reconciliation required")

// SettlementPolicy is trusted runtime classification, not supplier retry flags.
// A nil classifier closes continuation. Provider adapters must be checked against
// the stored plan before invoking this fact path.
type SettlementPolicy struct {
	AllowContinue bool
	RetryAllowed  func(domain.ExecutionPlan, domain.ExecutionReport) bool
}
type SettlementService struct {
	st        store.Store
	runtimeID domain.ID
	clock     PreparationClock
	policy    SettlementPolicy
	prep      *PreparationService
}

func NewSettlementService(st store.Store, runtimeID domain.ID, clock PreparationClock, policy SettlementPolicy, prep *PreparationService) *SettlementService {
	return &SettlementService{st: st, runtimeID: runtimeID, clock: clock, policy: policy, prep: prep}
}

// Settle commits report, reservation phases, counter facts, occupancy conservation,
// result and ordered events together. It never admits or sends another request.
func (s *SettlementService) Settle(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, report domain.ExecutionReport) (ledger.SettleReply, error) {
	var reply ledger.SettleReply
	if s == nil || s.st == nil || s.clock == nil || s.runtimeID == "" || s.prep == nil || s.prep.runtimeID != s.runtimeID || owner.RuntimeID != s.runtimeID || !authorized(auth, owner.Key.Namespace) {
		return reply, ErrPreparationForbidden
	}
	if report.ReportID == "" || report.AttemptID == "" {
		return reply, ErrSettlementInvalid
	}
	hash, raw, err := commandHash(report)
	if err != nil {
		return reply, err
	}
	scope := domain.ID("settle/" + string(owner.Key.Namespace))
	conflict := false
	err = s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(report.AttemptID), Control: true, Run: func(tx store.WriterTx) error {
		sx, ok := tx.(store.SettlementWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrSettlementInvalid
		}
		op, ok, e := sx.GetClaimOperation(owner.Key.Namespace, owner.Key.OperationID)
		if e != nil {
			return e
		}
		if !ok {
			return ErrPreparationForbidden
		}
		a, ok, e := sx.GetPreparationAttempt(report.AttemptID)
		if e != nil {
			return e
		}
		if !ok || a.Key != owner.Key {
			return ErrPreparationForbidden
		}
		receipt, exists, e := tx.GetReceipt(scope, a.AttemptID)
		if e != nil {
			return e
		}
		if exists {
			if receipt.Kind == "settle" && receipt.InputHash == hash {
				return json.Unmarshal(receipt.ResultJSON, &reply)
			}
			conflict = true
			return sx.AppendSettlementEvidence(a.Key, report, hash, auth.PrincipalID, now)
		}
		if op.State != domain.Running || op.RuntimeID != owner.RuntimeID || op.OwnerID != owner.OwnerID || op.Generation != owner.Generation || a.Generation != owner.Generation || op.LeaseUntil <= now || a.Phase != "executing" {
			conflict = true
			return sx.AppendSettlementEvidence(a.Key, report, hash, auth.PrincipalID, now)
		}
		// Deadline and cancellation cannot erase already dispatched accounting facts.
		permit, ok, e := sx.GetPreparationPermit(a.AttemptID)
		if e != nil {
			return e
		}
		if !ok || permit.Status != "consumed" {
			return ErrSettlementInvalid
		}
		facts, e := sx.GetSettlementFacts(a, now)
		if e != nil {
			return e
		}
		if facts.ReportHash != "" {
			return ErrClaimIntegrity
		}
		switch report.Outcome {
		case "known_success", "known_failure", "unknown":
		default:
			return ErrSettlementInvalid
		}
		if report.RetrySafety != domain.ProvedSafe && report.RetrySafety != domain.Unsafe && report.RetrySafety != domain.Undetermined {
			return ErrSettlementInvalid
		}
		if report.ExternalEnded && len(report.EndEvidenceRefs) == 0 {
			return ErrSettlementInvalid
		}
		metrics := map[domain.ID]domain.MetricReport{}
		for _, m := range report.Metrics {
			if m.MetricID == "" || m.Unit == "" || (m.Actual != nil && *m.Actual < 0) || (m.ChargedAt != nil && (*m.ChargedAt < 0 || *m.ChargedAt > now)) {
				return ErrSettlementInvalid
			}
			if m.Conservative && (m.Actual == nil || m.ActualOutOfRangeDecimal != "") {
				return ErrSettlementInvalid
			}
			if m.ActualOutOfRangeDecimal != "" {
				if m.Actual != nil || domain.ValidateOutOfRangeUnitsDecimal(m.ActualOutOfRangeDecimal) != nil || len(m.EvidenceRefs) == 0 {
					return ErrSettlementInvalid
				}
			}
			if _, dup := metrics[m.MetricID]; dup {
				return ErrSettlementInvalid
			}
			found := false
			for _, req := range a.Plan.Requirements {
				if req.MetricID == m.MetricID && req.Unit == m.Unit {
					found = true
				}
			}
			if !found {
				return ErrSettlementInvalid
			}
			metrics[m.MetricID] = m
		}
		// Independent mapping check before any settlement fact is accepted:
		// every charged requirement's bound must be certified under the
		// plan's report mapping revision, and every allocated resource must
		// still carry its requirement's metric identity. Drift here is
		// persisted-identity inconsistency, not a report defect: fail closed
		// with no side effects; reconciliation owns recovery. (Equality of
		// the two revisions is the synthetic G1-DEV convention; real
		// integration must define the authoritative revision mapping.)
		for _, req := range a.Plan.Requirements {
			mapped := false
			for _, b := range a.Plan.Bounds {
				if b.EvidenceID == req.EvidenceID && b.MetricID == req.MetricID && b.Unit == req.Unit {
					if b.MappingRevision != a.Plan.ReportMappingRevision {
						return ErrClaimIntegrity
					}
					mapped = true
				}
			}
			if !mapped {
				return ErrClaimIntegrity
			}
		}
		for _, m := range metrics {
			if m.ActualOutOfRangeDecimal == "" {
				continue
			}
			counterMapped := false
			for _, req := range a.Plan.Requirements {
				if req.MetricID != m.MetricID || req.Unit != m.Unit {
					continue
				}
				for _, resourceID := range req.ResourceIDs {
					for _, allocation := range a.Allocations {
						if allocation.ResourceID == resourceID && allocation.Kind == "counter" {
							counterMapped = true
						}
					}
				}
			}
			if !counterMapped {
				return ErrSettlementInvalid
			}
		}
		var outcomes []domain.ResourceOutcome
		var reasons []domain.Reason
		blocked := facts.Blocked
		// Precompute waterfall attribution by certified provider order, so actual
		// dimensions are never charged in full to every entitlement bucket.
		actuals := map[domain.ID]*domain.Units{}
		for _, req := range a.Plan.Requirements {
			m := metrics[req.MetricID]
			if m.ActualOutOfRangeDecimal != "" {
				continue
			}
			if req.Mode != "provider_entitlement_waterfall" {
				for _, id := range req.ResourceIDs {
					actuals[id] = m.Actual
				}
				continue
			}
			if m.Actual == nil {
				continue
			}
			remaining := *m.Actual
			ordered := make([]domain.ReservationState, 0)
			for _, id := range req.ResourceIDs {
				for _, r := range a.Allocations {
					if r.ResourceID == id {
						ordered = append(ordered, r)
					}
				}
			}
			// Stored plan allocation order is not an attribution fact: use provider order.
			for i := 0; i < len(ordered); i++ {
				for j := i + 1; j < len(ordered); j++ {
					var oi, oj *int64
					for _, x := range facts.Resources {
						if x.Definition.ResourceID == ordered[i].ResourceID {
							oi = x.Definition.ProviderOrder
						}
						if x.Definition.ResourceID == ordered[j].ResourceID {
							oj = x.Definition.ProviderOrder
						}
					}
					if oi == nil || oj == nil || *oi == *oj {
						return ErrSettlementInvalid
					}
					if *oj < *oi {
						ordered[i], ordered[j] = ordered[j], ordered[i]
					}
				}
			}
			for i, r := range ordered {
				v := remaining
				if i < len(ordered)-1 && v > r.Units {
					v = r.Units
				}
				remaining -= v
				copy := v
				actuals[r.ResourceID] = &copy
			}
		}
		for _, r := range a.Allocations {
			var x *store.SettlementResource
			for i := range facts.Resources {
				if facts.Resources[i].Definition.ResourceID == r.ResourceID && facts.Resources[i].Snapshot.WindowKey == r.WindowKey {
					x = &facts.Resources[i]
					break
				}
			}
			if x == nil {
				return ErrClaimIntegrity
			}
			metricMatched := false
			for _, req := range a.Plan.Requirements {
				if req.MetricID != x.Definition.MetricID || req.Unit != x.Definition.Unit {
					continue
				}
				for _, id := range req.ResourceIDs {
					if id == r.ResourceID {
						metricMatched = true
					}
				}
			}
			if !metricMatched {
				return ErrClaimIntegrity
			}
			rc := domain.ResourceContext{Now: now, Definition: x.Definition, Snapshot: x.Snapshot}
			m := metrics[x.Definition.MetricID]
			zero := domain.Units(0)
			out := domain.ResourceOutcome{ResourceID: r.ResourceID, WindowKey: r.WindowKey, MetricID: x.Definition.MetricID, Unit: x.Definition.Unit, Consumed: &zero, EvidenceRefs: m.EvidenceRefs, Occupancy: "not_applicable"}
			var p domain.ResourceChangePlan
			phase := "settled"
			var at *domain.Millis
			if r.Kind == "counter" {
				if r.Phase != "reserved" {
					return ErrClaimIntegrity
				}
				if m.ActualOutOfRangeDecimal != "" {
					out.Consumed = nil
					out.ConsumptionBasis = domain.ConsumptionUnrepresentable
					phase = "settled"
					blocked = true
					p = domain.ResourceChangePlan{
						Admitted: true,
						Deltas: []domain.ResourceDelta{{
							ResourceID: r.ResourceID, WindowKey: r.WindowKey,
							ExpectedVersion: x.Snapshot.Version, Reserved: -r.Units,
						}},
						Reasons: []domain.Reason{{Group: domain.ReasonGroupAccounting, Code: domain.ReasonActualOutOfRange, ScopeRef: r.ResourceID}},
					}
				} else {
					end := domain.EndProofNone
					if report.ExternalEnded {
						end = domain.EndProofExternalEnded
					}
					attr := domain.AttributionUnknown
					switch {
					case x.Definition.Window.Kind == "lifetime":
						attr = domain.AttributionKnown
					case m.ChargedAt == nil:
						// No charge instant: the charge stays unattributed and
						// isolates below instead of silently reopening capacity.
					case x.Definition.Window.Kind == "rolling":
						// A rolling bucket has one stable identity per rule
						// revision, so a key comparison is vacuous; attribution is
						// temporal — the charge counts against this bucket only
						// inside the current rolling interval, the same cutoff
						// preparation availability uses. A zero/negative duration
						// never attributes (fail closed).
						if *m.ChargedAt > now-domain.Millis(x.Definition.Window.DurationMillis) {
							attr = domain.AttributionKnown
						}
					default:
						key, _, _, e := resources.WindowAt(*m.ChargedAt, x.Definition.Window, a.Plan.WindowRuleRevision)
						if e != nil {
							return e
						}
						if key == r.WindowKey {
							attr = domain.AttributionKnown
						}
					}
					p, e = resources.CounterSettle(rc, r, domain.SettlementProjection{Actual: actuals[r.ResourceID], ChargedAt: m.ChargedAt, End: end, Attribution: attr, EvidenceRefs: m.EvidenceRefs}, r.Units)
					at = m.ChargedAt
					out.ConsumptionBasis = domain.ConsumptionProviderReported
					if actuals[r.ResourceID] == nil || metrics[x.Definition.MetricID].Conservative {
						out.ConsumptionBasis = domain.ConsumptionConservativeBound
					}
					if len(p.Deltas) > 0 {
						consumed := p.Deltas[0].Committed
						out.Consumed = &consumed
					}
				}
			} else if r.Kind == "occupancy" {
				if r.Phase != "executing" {
					return ErrClaimIntegrity
				}
				out.ConsumptionBasis = domain.ConsumptionNotApplicable
				out.EvidenceRefs = report.EndEvidenceRefs
				if report.ExternalEnded {
					p, e = resources.OccupancyEndProved(rc, r.Units)
					phase = "released"
					out.Occupancy = "released"
				} else {
					p, e = resources.OccupancyToUncertain(rc, r.Units)
					phase = "uncertain"
					out.Occupancy = "uncertain"
					blocked = true
					if e == nil {
						e = sx.InsertSettlementIsolation(a.AttemptID, r.ResourceID, r.WindowKey, "occupancy", r.Units, report.EndEvidenceRefs, now)
					}
				}
			} else {
				return ErrSettlementInvalid
			}
			if e != nil {
				return e
			}
			for _, d := range p.Deltas {
				if e = resources.ValidateChange(x.Snapshot, d); e != nil {
					return e
				}
				applied, e := sx.ApplySettlementDelta(d, a.AttemptID, scope, at, out.EvidenceRefs, now)
				if e != nil {
					return e
				}
				if !applied {
					return ErrPreparationStale
				}
			}
			for _, iso := range p.IsolationRequests {
				blocked = true
				if e = sx.InsertSettlementIsolation(a.AttemptID, r.ResourceID, iso.WindowKey, "window_attribution", iso.Units, m.EvidenceRefs, now); e != nil {
					return e
				}
				if e = sx.InsertSettlementHold(a.AttemptID, r.ResourceID, "window_attribution_unverified", m.EvidenceRefs, now); e != nil {
					return e
				}
			}
			for _, reason := range p.Reasons {
				if reason.Group == "overrun" {
					blocked = true
					if e = sx.InsertSettlementHold(a.AttemptID, r.ResourceID, reason.Code, m.EvidenceRefs, now); e != nil {
						return e
					}
				}
				if reason.Group == domain.ReasonGroupAccounting && reason.Code == domain.ReasonActualOutOfRange {
					if e = sx.InsertSettlementHold(a.AttemptID, r.ResourceID, reason.Code, m.EvidenceRefs, now); e != nil {
						return e
					}
				}
			}
			reasons = append(reasons, p.Reasons...)
			applied, e := sx.SettleReservation(r.ReservationID, r.Phase, phase)
			if e != nil {
				return e
			}
			if !applied {
				return ErrPreparationStale
			}
			outcomes = append(outcomes, out)
		}
		canContinue := report.Outcome == "known_failure" && report.ExternalEnded && report.RetrySafety == domain.ProvedSafe && s.policy.AllowContinue && s.policy.RetryAllowed != nil && s.policy.RetryAllowed(a.Plan, report) && op.CancelRequestedAt == nil && op.DeadlineAt > now && op.PermitsIssued < op.MaxAttempts && op.VisibleOutputAt == nil && !facts.OutputStored && !blocked
		var unrepresentableMetrics []domain.UnrepresentableMetric
		for _, m := range report.Metrics {
			if m.ActualOutOfRangeDecimal != "" {
				unrepresentableMetrics = append(unrepresentableMetrics, domain.UnrepresentableMetric{
					MetricID: m.MetricID, Unit: m.Unit,
					ActualOutOfRangeDecimal: m.ActualOutOfRangeDecimal,
					EvidenceRefs:            append([]domain.ID(nil), m.EvidenceRefs...),
				})
			}
		}
		rf := domain.ResultFacts{ConfirmedSuccess: report.Outcome == "known_success", UnconfirmedDispatchPending: report.Outcome == "unknown" || (!report.ExternalEnded && report.Outcome != "known_success"), AccountingUnrepresentable: len(unrepresentableMetrics) > 0, CancelRequested: op.CancelRequestedAt != nil, AllActionsNotExecutedOrEnded: report.ExternalEnded && facts.AllPriorEnded, DeadlineExceeded: op.DeadlineAt <= now, AttemptsExhausted: op.PermitsIssued >= op.MaxAttempts}
		if !canContinue && report.Outcome == "known_failure" {
			rf.DomainFailureReasons = append([]domain.Reason{{Group: "domain", Code: "known_failure"}}, report.Reasons...)
		}
		state, code, resultReasons := domain.ResolveResult(rf)
		reply = ledger.SettleReply{Continue: canContinue && state == domain.Running}
		if !reply.Continue && state == domain.Running {
			return fmt.Errorf("settlement: unresolved terminal facts")
		}
		if state != domain.Running {
			all := append(facts.Outcomes, outcomes...)
			suggestedAction := ""
			resultRetrySafety := report.RetrySafety
			if len(unrepresentableMetrics) > 0 {
				suggestedAction = "review_usage"
				resultRetrySafety = domain.Undetermined
			}
			resultReasons = append([]domain.Reason{}, resultReasons...)
			resultReasons = append(resultReasons, reasons...)
			reply.Result = &domain.OperationResult{OperationID: a.Key.OperationID, State: state, Code: code, Reasons: resultReasons, RetrySafety: resultRetrySafety, Attempts: facts.Attempts, Resources: all, UnrepresentableMetrics: unrepresentableMetrics, DecisionRefs: []domain.ID{}, OutputAvailability: "none", SuggestedAction: suggestedAction}
			if facts.OutputStored {
				reply.Result.OutputAvailability = "available"
				reply.Result.OutputRef = domain.ID("operation/" + string(a.Key.OperationID))
			}
		}
		replyRaw, e := json.Marshal(reply)
		if e != nil {
			return e
		}
		applied, e := sx.PersistSettlement(op, a, store.SettlementWrite{AttemptID: a.AttemptID, Report: report, ReportHash: hash, ReportJSON: raw, ReplyJSON: replyRaw, Result: reply.Result, Outcomes: outcomes, Now: now})
		if e != nil {
			return e
		}
		if !applied {
			return ErrPreparationStale
		}
		return tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: a.AttemptID, Kind: "settle", InputHash: hash, ResultJSON: replyRaw, CreatedAt: now})
	}})
	if err != nil {
		return ledger.SettleReply{}, err
	}
	if conflict {
		return ledger.SettleReply{}, ErrSettlementConflict
	}
	// Settle always terminalizes this attempt, whether the operation itself
	// completes or is allowed to continue with a fresh attempt. Release its
	// runtime-local permit/handle only after the durable transaction commits.
	s.prep.releaseTerminalAttempt(report.AttemptID)
	return reply, nil
}
