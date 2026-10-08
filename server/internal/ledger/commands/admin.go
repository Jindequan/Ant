package commands

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
)

var (
	ErrReconciliationInvalid   = errors.New("ledger reconcile: invalid report or evidence")
	ErrReconciliationForbidden = errors.New("ledger reconcile: forbidden")
	ErrReconciliationConflict  = errors.New("ledger reconcile: command conflict or attempt is not reconcilable")
)

type ReconciliationService struct {
	st    store.Store
	clock PreparationClock
}

func NewReconciliationService(st store.Store, clock PreparationClock) *ReconciliationService {
	return &ReconciliationService{st: st, clock: clock}
}

// Reconcile appends evidence-referenced management findings and corrects conservative
// resource accounting in one transaction. It deliberately leaves the
// original unknown OperationResult untouched and never schedules execution.
func (s *ReconciliationService) Reconcile(ctx context.Context, auth domain.AuthContext, input domain.ReconciliationInput) (domain.ReconciliationView, error) {
	var view domain.ReconciliationView
	if s == nil || s.st == nil || s.clock == nil || auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermManage) {
		return view, ErrReconciliationForbidden
	}
	if err := validateReconciliationInput(input); err != nil {
		return view, err
	}
	hash, _, err := commandHash(input)
	if err != nil {
		return view, err
	}
	scope := domain.ID("reconcile/" + string(auth.Namespace) + "/" + string(input.AttemptID))
	err = s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(input.CommandID), Control: true, Run: func(tx store.WriterTx) error {
		rx, ok := tx.(store.ReconciliationWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrReconciliationInvalid
		}
		receipt, exists, e := tx.GetReceipt(scope, input.CommandID)
		if e != nil {
			return e
		}
		if exists {
			if receipt.Kind == "reconcile" && receipt.InputHash == hash {
				return json.Unmarshal(receipt.ResultJSON, &view)
			}
			return ErrReconciliationConflict
		}
		a, found, e := rx.GetPreparationAttempt(input.AttemptID)
		if e != nil {
			return e
		}
		if !found || a.Key.Namespace != auth.Namespace || a.Phase != "terminal" {
			return ErrReconciliationConflict
		}
		attemptOutcomes, e := rx.ListRecoveryAttemptOutcomes(a.Key)
		if e != nil {
			return e
		}
		isUnknownAttempt := false
		for _, outcome := range attemptOutcomes {
			if outcome.AttemptID == a.AttemptID && outcome.ExternalOutcome == "unknown" {
				isUnknownAttempt = true
				break
			}
		}
		op, found, e := rx.GetClaimOperation(a.Key.Namespace, a.Key.OperationID)
		if e != nil {
			return e
		}
		if !found || op.State != domain.Unknown {
			return ErrReconciliationConflict
		}
		facts, e := rx.GetSettlementFacts(a, now)
		if e != nil {
			return e
		}
		var original *domain.ExecutionReport
		hasUnrepresentable := false
		if len(facts.ReportJSON) > 0 {
			var persisted domain.ExecutionReport
			if json.Unmarshal(facts.ReportJSON, &persisted) != nil {
				return ErrReconciliationConflict
			}
			for _, metric := range persisted.Metrics {
				if metric.ActualOutOfRangeDecimal != "" {
					hasUnrepresentable = true
				}
			}
			if persisted.Outcome != "unknown" && !hasUnrepresentable {
				return ErrReconciliationConflict
			}
			original = &persisted
		}
		if !isUnknownAttempt && !hasUnrepresentable {
			return ErrReconciliationConflict
		}
		previousCorrections, _, e := rx.GetLatestReconciliationCorrections(a.AttemptID)
		if e != nil {
			return e
		}
		corrections, deltas, e := buildReconciliationCorrections(a, facts.Resources, original, previousCorrections, input)
		if e != nil {
			return e
		}
		allEvidence := append([]domain.ID{}, input.EvidenceRefs...)
		for _, correction := range input.Corrections {
			allEvidence = append(allEvidence, correction.EvidenceRefs...)
		}
		allEvidence = uniqueIDs(allEvidence)
		for i := range corrections {
			corrections[i].EvidenceRefs = append([]domain.ID{}, allEvidence...)
		}
		for _, d := range deltas {
			if err = resources.ValidateChange(snapshotForResource(facts.Resources, d.ResourceID, d.WindowKey), d); err != nil {
				return fmt.Errorf("reconcile resource validation: %w", err)
			}
			applied, applyErr := rx.ApplyReconciliationDelta(d, a.AttemptID, scope, input.CommandID, allEvidence, now)
			if applyErr != nil {
				return applyErr
			}
			if !applied {
				return ErrPreparationStale
			}
			if d.Uncertain < 0 {
				applied, applyErr = rx.ReleaseReconciledOccupancy(a.AttemptID, d.ResourceID, -d.Uncertain, allEvidence, now)
				if applyErr != nil {
					return applyErr
				}
				if !applied {
					return ErrReconciliationConflict
				}
				allocation, found := allocationFor(a.Allocations, d.ResourceID)
				if !found {
					return ErrClaimIntegrity
				}
				applied, applyErr = rx.SettleReservation(allocation.ReservationID, "uncertain", "released")
				if applyErr != nil {
					return applyErr
				}
				if !applied {
					return ErrReconciliationConflict
				}
			}
		}
		for _, allocation := range a.Allocations {
			if allocation.Kind != "counter" {
				continue
			}
			if err = rx.ResolveReconciliationHolds(a.AttemptID, allocation.ResourceID, allEvidence, now); err != nil {
				return err
			}
			correction, found := correctionByResource(input.Corrections, allocation.ResourceID)
			if !found || correction.Consumed == nil {
				return ErrReconciliationInvalid
			}
			if *correction.Consumed > allocation.Units {
				if err = rx.InsertReconciliationHold(a.AttemptID, allocation.ResourceID, input.CommandID, "reconciled_usage_overrun", allEvidence, now); err != nil {
					return err
				}
			}
			if input.ConfirmedOutcome != "unknown" {
				if _, err = rx.ReleaseReconciledAttribution(a.AttemptID, allocation.ResourceID, allEvidence, now); err != nil {
					return err
				}
			}
		}
		view = domain.ReconciliationView{
			ReconciliationID: domain.ID("reconcile/" + string(input.AttemptID) + "/" + string(input.CommandID)),
			ConfirmedOutcome: input.ConfirmedOutcome,
			RetrySafety:      input.RetrySafety,
			EvidenceRefs:     allEvidence,
			Corrections:      corrections,
			CreatedAt:        now,
		}
		evidenceJSON, e := json.Marshal(struct {
			ConfirmedOutcome string                   `json:"confirmedOutcome"`
			RetrySafety      domain.RetrySafety       `json:"retrySafety"`
			EvidenceRefs     []domain.ID              `json:"evidenceRefs"`
			Corrections      []domain.ResourceOutcome `json:"corrections"`
		}{input.ConfirmedOutcome, input.RetrySafety, allEvidence, input.Corrections})
		if e != nil {
			return e
		}
		correctionJSON, e := json.Marshal(corrections)
		if e != nil {
			return e
		}
		if err = rx.InsertReconciliation(view.ReconciliationID, a.AttemptID, auth.PrincipalID, hash, evidenceJSON, correctionJSON, now); err != nil {
			return err
		}
		resultJSON, e := json.Marshal(view)
		if e != nil {
			return e
		}
		if err = tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: input.CommandID, Kind: "reconcile", InputHash: hash, ResultJSON: resultJSON, CreatedAt: now}); err != nil {
			return err
		}
		if err = tx.InsertAudit(store.AuditRecord{AuditID: domain.ID("reconcile/" + string(auth.Namespace) + "/" + string(input.AttemptID) + "/" + string(input.CommandID)), PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: "operation.reconcile", TargetRef: a.AttemptID, EvidenceRef: allEvidence[0], CreatedAt: now}); err != nil {
			return err
		}
		if err = rx.AppendOperationEvent(a.Key, "reconciled", resultJSON, now); err != nil {
			return err
		}
		return nil
	}})
	if err != nil {
		return domain.ReconciliationView{}, err
	}
	return view, nil
}

func validateReconciliationInput(in domain.ReconciliationInput) error {
	if in.CommandID == "" || in.AttemptID == "" || len(in.EvidenceRefs) == 0 || in.Corrections == nil {
		return ErrReconciliationInvalid
	}
	if in.ConfirmedOutcome != "known_success" && in.ConfirmedOutcome != "known_failure" && in.ConfirmedOutcome != "not_executed" && in.ConfirmedOutcome != "unknown" {
		return ErrReconciliationInvalid
	}
	if in.RetrySafety != domain.ProvedSafe && in.RetrySafety != domain.Unsafe && in.RetrySafety != domain.Undetermined {
		return ErrReconciliationInvalid
	}
	if len(uniqueIDs(in.EvidenceRefs)) != len(in.EvidenceRefs) {
		return ErrReconciliationInvalid
	}
	for _, id := range in.EvidenceRefs {
		if id == "" {
			return ErrReconciliationInvalid
		}
	}
	seen := map[domain.ID]bool{}
	for _, correction := range in.Corrections {
		if correction.ResourceID == "" || correction.WindowKey == "" || correction.MetricID == "" || correction.Unit == "" || seen[correction.ResourceID] {
			return ErrReconciliationInvalid
		}
		seen[correction.ResourceID] = true
		for _, id := range correction.EvidenceRefs {
			if id == "" {
				return ErrReconciliationInvalid
			}
		}
	}
	return nil
}

func buildReconciliationCorrections(a store.PreparationAttempt, facts []store.SettlementResource, original *domain.ExecutionReport, previousCorrections []domain.ResourceOutcome, input domain.ReconciliationInput) ([]domain.ResourceOutcome, []domain.ResourceDelta, error) {
	if len(input.Corrections) != len(a.Allocations) {
		return nil, nil, ErrReconciliationInvalid
	}
	resourceByID := map[domain.ID]store.SettlementResource{}
	for _, x := range facts {
		resourceByID[x.Definition.ResourceID] = x
	}
	correctionByID := make(map[domain.ID]domain.ResourceOutcome, len(input.Corrections))
	for _, correction := range input.Corrections {
		correctionByID[correction.ResourceID] = correction
	}
	oldActuals := map[domain.ID]domain.Units{}
	for _, allocation := range a.Allocations {
		if allocation.Kind == "counter" {
			oldActuals[allocation.ResourceID] = allocation.Units // recovery without a report conserves the certified bound
		}
	}
	if original != nil {
		oldMetrics := make(map[domain.ID]domain.MetricReport, len(original.Metrics))
		for _, m := range original.Metrics {
			oldMetrics[m.MetricID] = m
			if m.ActualOutOfRangeDecimal != "" {
				for _, req := range a.Plan.Requirements {
					if req.MetricID == m.MetricID {
						for _, id := range req.ResourceIDs {
							if x, ok := resourceByID[id]; ok && x.Definition.Kind == "counter" {
								oldActuals[id] = 0 // out-of-range settlement released its reservation without a representable charge
							}
						}
					}
				}
			}
		}
		projected, projectErr := projectReconciliationUsage(a, resourceByID, oldMetrics, false)
		if projectErr != nil {
			return nil, nil, projectErr
		}
		for id, amount := range projected {
			oldActuals[id] = amount
		}
	}
	for _, correction := range previousCorrections {
		if correction.Consumed != nil {
			oldActuals[correction.ResourceID] = *correction.Consumed
		}
	}
	var outcomes []domain.ResourceOutcome
	var deltas []domain.ResourceDelta
	for _, allocation := range a.Allocations {
		x, ok := resourceByID[allocation.ResourceID]
		if !ok {
			return nil, nil, ErrClaimIntegrity
		}
		out, correctionExists := correctionByID[allocation.ResourceID]
		if !correctionExists || out.WindowKey != allocation.WindowKey || out.MetricID != x.Definition.MetricID || out.Unit != x.Definition.Unit {
			return nil, nil, ErrReconciliationInvalid
		}
		out.EvidenceRefs = append([]domain.ID{}, out.EvidenceRefs...)
		switch allocation.Kind {
		case "counter":
			if out.Consumed == nil || *out.Consumed < 0 || out.Occupancy != "not_applicable" || out.ConsumptionBasis != domain.ConsumptionAdjustment {
				return nil, nil, ErrReconciliationInvalid
			}
			if input.ConfirmedOutcome == "not_executed" && *out.Consumed != 0 {
				return nil, nil, ErrReconciliationInvalid
			}
			actual := *out.Consumed
			out.ConsumptionBasis = domain.ConsumptionAdjustment
			delta, err := domain.SubUnits(actual, oldActuals[allocation.ResourceID])
			if err != nil {
				return nil, nil, err
			}
			if delta != 0 {
				deltas = append(deltas, domain.ResourceDelta{ResourceID: allocation.ResourceID, WindowKey: allocation.WindowKey, ExpectedVersion: x.Snapshot.Version, Adjustment: delta})
			}
		case "occupancy":
			if out.Consumed != nil || out.ConsumptionBasis != domain.ConsumptionNotApplicable {
				return nil, nil, ErrReconciliationInvalid
			}
			out.ConsumptionBasis = domain.ConsumptionNotApplicable
			if allocation.Phase == "uncertain" {
				if input.ConfirmedOutcome == "unknown" {
					if out.Occupancy != "uncertain" {
						return nil, nil, ErrReconciliationInvalid
					}
				} else {
					if out.Occupancy != "released" {
						return nil, nil, ErrReconciliationInvalid
					}
					deltas = append(deltas, domain.ResourceDelta{ResourceID: allocation.ResourceID, WindowKey: allocation.WindowKey, ExpectedVersion: x.Snapshot.Version, Uncertain: -allocation.Units})
				}
			} else if allocation.Phase == "released" {
				if out.Occupancy != "released" {
					return nil, nil, ErrReconciliationInvalid
				}
			} else {
				return nil, nil, ErrReconciliationConflict
			}
		default:
			return nil, nil, ErrClaimIntegrity
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, deltas, nil
}

func projectReconciliationUsage(a store.PreparationAttempt, resourcesByID map[domain.ID]store.SettlementResource, metrics map[domain.ID]domain.MetricReport, require bool) (map[domain.ID]domain.Units, error) {
	actuals := map[domain.ID]domain.Units{}
	for _, req := range a.Plan.Requirements {
		requiresUsage := false
		for _, id := range req.ResourceIDs {
			if x, ok := resourcesByID[id]; ok && x.Definition.Kind == "counter" {
				requiresUsage = true
			}
		}
		if !requiresUsage {
			continue
		}
		m, ok := metrics[req.MetricID]
		if !ok {
			if require {
				return nil, ErrReconciliationInvalid
			}
			continue
		}
		if m.Unit != req.Unit {
			return nil, ErrReconciliationInvalid
		}
		if m.Actual == nil { // missing or unrepresentable actual: caller retains its conservative baseline
			continue
		}
		if req.Mode != "provider_entitlement_waterfall" {
			for _, id := range req.ResourceIDs {
				if x, ok := resourcesByID[id]; ok && x.Definition.Kind == "counter" {
					actuals[id] = *m.Actual
				}
			}
			continue
		}
		ordered := make([]domain.ID, 0, len(req.ResourceIDs))
		for _, id := range req.ResourceIDs {
			if x, ok := resourcesByID[id]; ok && x.Definition.Kind == "counter" {
				if x.Definition.ProviderOrder == nil {
					return nil, ErrClaimIntegrity
				}
				ordered = append(ordered, id)
			}
		}
		sort.Slice(ordered, func(i, j int) bool {
			return *resourcesByID[ordered[i]].Definition.ProviderOrder < *resourcesByID[ordered[j]].Definition.ProviderOrder
		})
		remaining := *m.Actual
		for i, id := range ordered {
			allocation, ok := allocationFor(a.Allocations, id)
			if !ok {
				return nil, ErrClaimIntegrity
			}
			value := remaining
			if i < len(ordered)-1 && value > allocation.Units {
				value = allocation.Units
			}
			remaining -= value
			actuals[id] = value
		}
	}
	return actuals, nil
}

func allocationFor(allocations []domain.ReservationState, resourceID domain.ID) (domain.ReservationState, bool) {
	for _, allocation := range allocations {
		if allocation.ResourceID == resourceID {
			return allocation, true
		}
	}
	return domain.ReservationState{}, false
}

func correctionByResource(corrections []domain.ResourceOutcome, resourceID domain.ID) (domain.ResourceOutcome, bool) {
	for _, correction := range corrections {
		if correction.ResourceID == resourceID {
			return correction, true
		}
	}
	return domain.ResourceOutcome{}, false
}

func snapshotForResource(facts []store.SettlementResource, id, window domain.ID) domain.ResourceSnapshot {
	for _, x := range facts {
		if x.Definition.ResourceID == id && x.Snapshot.WindowKey == window {
			return x.Snapshot
		}
	}
	return domain.ResourceSnapshot{}
}

func uniqueIDs(ids []domain.ID) []domain.ID {
	seen := map[domain.ID]bool{}
	out := make([]domain.ID, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
