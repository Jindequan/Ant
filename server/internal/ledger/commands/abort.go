package commands

import (
	"context"
	"encoding/json"
	"errors"

	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

// AbortBeforeDispatch (K3, 03-EXECUTION §9). The transaction reads
// (attempt.phase, permit.status, generation) inside one BEGIN IMMEDIATE,
// runs the frozen DecideAbort table and applies the whole outcome atomically,
// competing with consume/sweep through the same CAS guards. A losing racer
// gets ledger.ErrAbortConflict and no partial effect is applied.

// AbortRequest identifies the attempt to close and the evidence class the
// caller presents. ProofReliableUnsent additionally requires the in-process
// DispatchPermit handle: the unforgeable handle plus the durable permit row
// (same permit_id/runtime_id/request_digest) is the "same-source" proof
// verification §9 demands — caller claims alone are never sufficient.
type AbortRequest struct {
	Owner   domain.OwnerToken
	Attempt domain.ID
	Proof   ledger.UnsentProof
	Permit  ledger.DispatchPermit // required iff Proof == ProofReliableUnsent
}

// recoveryAbortResult is the durable conclusion stored in the abort receipt;
// replays unmarshal it verbatim.
type recoveryAbortResult struct {
	Outcome ledger.AbortOutcome     `json:"outcome"`
	Result  *domain.OperationResult `json:"result,omitempty"`
}

var errAbortAlreadyTerminal = errors.New("ledger abort: terminal operation still has an active attempt")

func (s *PreparationService) AbortBeforeDispatch(ctx context.Context, auth domain.AuthContext, req AbortRequest) (ledger.AbortOutcome, error) {
	if s == nil || s.st == nil || s.clock == nil || s.runtimeID == "" {
		return ledger.AbortOutcome{}, ErrPreparationInvalid
	}
	if req.Owner.RuntimeID != s.runtimeID || req.Attempt == "" || !authorized(auth, req.Owner.Key.Namespace) {
		return ledger.AbortOutcome{}, ErrPreparationForbidden
	}
	if req.Proof != ledger.ProofNone && req.Proof != ledger.ProofReliableUnsent {
		return ledger.AbortOutcome{}, ledger.ErrAbortInvalidState
	}
	if req.Proof == ledger.ProofReliableUnsent && (req.Permit.ID() == "" || req.Permit.AttemptID() != req.Attempt || req.Permit.RuntimeID() != s.runtimeID) {
		return ledger.AbortOutcome{}, ledger.ErrAbortInvalidState
	}
	var result recoveryAbortResult
	err := s.st.SubmitWrite(ctx, store.WriteCommand{ID: "abort/" + string(req.Owner.Key.Namespace) + "/" + string(req.Attempt), Control: true, Run: func(w store.WriterTx) error {
		rx, ok := w.(store.RecoveryWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrPreparationInvalid
		}
		var e error
		result, e = applyRecoveryAbort(rx, s.runtimeID, auth.PrincipalID, req.Owner.Key, req.Attempt, req.Owner.Generation, req.Proof, req.Permit, false, now)
		return e
	}})
	if err != nil {
		return ledger.AbortOutcome{}, err
	}
	// In-memory handle lifecycle: after commit, a terminal attempt can never
	// re-issue or consume its permit, so the cached handle is released and a
	// revoked granted permit is closed in the runtime registry too.
	s.releaseTerminalAttempt(req.Attempt)
	return result.Outcome, nil
}

// applyRecoveryAbort is the shared K3 transaction core used by the public
// owner command, cancel's inline prepared close, and sweep's takeover.
// system marks the sweep path: only it may complete the operation with the
// ExecutionInterrupted recovery reason and bump the generation.
func applyRecoveryAbort(rx store.RecoveryWriterTx, runtimeID, actor domain.ID, key domain.OperationKey, attemptID domain.ID, expectedGeneration int64, proof ledger.UnsentProof, permitHandle ledger.DispatchPermit, system bool, now domain.Millis) (recoveryAbortResult, error) {
	scope := domain.ID("abort/" + string(key.Namespace))
	hash, _, err := commandHash(struct {
		Key      domain.OperationKey
		Attempt  domain.ID
		Proof    ledger.UnsentProof
		PermitID domain.ID
		System   bool
	}{key, attemptID, proof, permitHandle.ID(), system})
	if err != nil {
		return recoveryAbortResult{}, err
	}
	if receipt, found, err := rx.GetReceipt(scope, attemptID); err != nil {
		return recoveryAbortResult{}, err
	} else if found {
		if receipt.Kind != "abort" || receipt.InputHash != hash {
			return recoveryAbortResult{}, ledger.ErrAbortConflict
		}
		var stored recoveryAbortResult
		if err := json.Unmarshal(receipt.ResultJSON, &stored); err != nil {
			return recoveryAbortResult{}, ErrClaimIntegrity
		}
		return stored, nil
	}
	op, found, err := rx.GetClaimOperation(key.Namespace, key.OperationID)
	if err != nil {
		return recoveryAbortResult{}, err
	}
	if !found {
		return recoveryAbortResult{}, ErrPreparationForbidden
	}
	attempt, found, err := rx.GetPreparationAttempt(attemptID)
	if err != nil {
		return recoveryAbortResult{}, err
	}
	if !found || attempt.Key != key || attempt.Generation != expectedGeneration {
		return recoveryAbortResult{}, ErrPreparationStale
	}
	permitStatus := ledger.PermitNone
	if permit, found, err := rx.GetPreparationPermit(attemptID); err != nil {
		return recoveryAbortResult{}, err
	} else if found {
		switch permit.Status {
		case "granted":
			permitStatus = ledger.PermitGranted
		case "consumed":
			permitStatus = ledger.PermitConsumed
		case "revoked":
			permitStatus = ledger.PermitRevoked
		default:
			return recoveryAbortResult{}, ErrClaimIntegrity
		}
		if proof == ledger.ProofReliableUnsent {
			// Same-source verification: the unforgeable handle must identify
			// exactly the durable permit row bound to this attempt and runtime.
			if permitStatus != ledger.PermitConsumed || permit.PermitID != permitHandle.ID() ||
				permit.RuntimeID != runtimeID || permit.AttemptID != attemptID ||
				permit.RuntimeID != permitHandle.RuntimeID() || permit.RequestDigest != attempt.RequestDigest {
				return recoveryAbortResult{}, ledger.ErrAbortInvalidState
			}
		}
	} else if proof == ledger.ProofReliableUnsent {
		return recoveryAbortResult{}, ledger.ErrAbortInvalidState
	}
	if op.State != domain.Running && attempt.Phase != "terminal" {
		return recoveryAbortResult{}, errAbortAlreadyTerminal
	}
	outcome, err := ledger.DecideAbort(ledger.AbortBeforeDispatchInput{
		AttemptPhase:    attempt.Phase,
		PermitStatus:    permitStatus,
		Proof:           proof,
		CancelRequested: op.CancelRequestedAt != nil,
	})
	if err != nil {
		return recoveryAbortResult{}, err
	}
	result := recoveryAbortResult{Outcome: outcome}
	if outcome.Kind == ledger.AbortAlreadyClosed {
		raw, err := json.Marshal(result)
		if err != nil {
			return recoveryAbortResult{}, err
		}
		if err := rx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: attemptID, Kind: "abort", InputHash: hash, ResultJSON: raw, CreatedAt: now}); err != nil {
			return recoveryAbortResult{}, err
		}
		return result, nil
	}
	facts, err := rx.GetSettlementFacts(attempt, now)
	if err != nil {
		return recoveryAbortResult{}, err
	}
	if facts.ReportHash != "" {
		return recoveryAbortResult{}, ErrClaimIntegrity
	}
	var outcomes []domain.ResourceOutcome
	for _, r := range attempt.Allocations {
		var x *store.SettlementResource
		for i := range facts.Resources {
			if facts.Resources[i].Definition.ResourceID == r.ResourceID && facts.Resources[i].Snapshot.WindowKey == r.WindowKey {
				x = &facts.Resources[i]
				break
			}
		}
		if x == nil {
			return recoveryAbortResult{}, ErrClaimIntegrity
		}
		rc := domain.ResourceContext{Now: now, Definition: x.Definition, Snapshot: x.Snapshot}
		zero := domain.Units(0)
		out := domain.ResourceOutcome{ResourceID: r.ResourceID, WindowKey: r.WindowKey, MetricID: x.Definition.MetricID, Unit: x.Definition.Unit, Consumed: &zero, Occupancy: "not_applicable"}
		var deltas []domain.ResourceDelta
		var phase string
		switch {
		case !outcome.ConserveCounter && !outcome.ConserveOccupancy: // not_executed release
			out.Occupancy = "released"
			phase = "released"
			if r.Kind == "counter" {
				if r.Phase != "reserved" {
					return recoveryAbortResult{}, ErrClaimIntegrity
				}
				// Proven never sent (never authorized, CAS-unconsumed, or
				// Transport-proven unsent): the fact path releases the
				// reservation and charges nothing.
				plan, err := resources.CounterSettle(rc, r, domain.SettlementProjection{End: domain.EndProofReliableUnsent, Attribution: domain.AttributionKnown}, r.Units)
				if err != nil {
					return recoveryAbortResult{}, err
				}
				deltas = plan.Deltas
				out.ConsumptionBasis = domain.ConsumptionNotExecuted
			} else {
				// Occupancy never externally started: reserved (prepared) or
				// active (executing) slots return to availability. This is not
				// OccupancyEndProved — that requires provider end evidence.
				var delta domain.ResourceDelta
				switch r.Phase {
				case "reserved":
					delta = domain.ResourceDelta{ResourceID: r.ResourceID, WindowKey: r.WindowKey, ExpectedVersion: x.Snapshot.Version, Reserved: -r.Units}
				case "executing":
					delta = domain.ResourceDelta{ResourceID: r.ResourceID, WindowKey: r.WindowKey, ExpectedVersion: x.Snapshot.Version, Active: -r.Units}
				default:
					return recoveryAbortResult{}, ErrClaimIntegrity
				}
				if err := validateReleaseDelta(delta, r.Units); err != nil {
					return recoveryAbortResult{}, err
				}
				deltas = []domain.ResourceDelta{delta}
				out.ConsumptionBasis = domain.ConsumptionNotApplicable
			}
		case outcome.ConserveCounter && r.Kind == "counter": // unknown charge
			if r.Phase != "reserved" {
				return recoveryAbortResult{}, ErrClaimIntegrity
			}
			// No report, no charge instant: lifetime buckets attribute to
			// themselves; every other rule isolates the bound charge.
			attribution := domain.AttributionUnknown
			if x.Definition.Window.Kind == "lifetime" {
				attribution = domain.AttributionKnown
			}
			plan, err := resources.CounterSettle(rc, r, domain.SettlementProjection{End: domain.EndProofNone, Attribution: attribution}, r.Units)
			if err != nil {
				return recoveryAbortResult{}, err
			}
			deltas = plan.Deltas
			phase = "settled"
			out.ConsumptionBasis = domain.ConsumptionConservativeBound
			consumed := r.Units
			out.Consumed = &consumed
			for _, iso := range plan.IsolationRequests {
				if err := rx.InsertSettlementIsolation(attemptID, r.ResourceID, iso.WindowKey, "window_attribution", iso.Units, nil, now); err != nil {
					return recoveryAbortResult{}, err
				}
				if err := rx.InsertSettlementHold(attemptID, r.ResourceID, "window_attribution_unverified", nil, now); err != nil {
					return recoveryAbortResult{}, err
				}
			}
		case outcome.ConserveOccupancy && r.Kind == "occupancy":
			if r.Phase != "executing" {
				return recoveryAbortResult{}, ErrClaimIntegrity
			}
			plan, err := resources.OccupancyToUncertain(rc, r.Units)
			if err != nil {
				return recoveryAbortResult{}, err
			}
			deltas = plan.Deltas
			phase = "uncertain"
			out.ConsumptionBasis = domain.ConsumptionNotApplicable
			out.Occupancy = "uncertain"
			if err := rx.InsertSettlementIsolation(attemptID, r.ResourceID, r.WindowKey, "occupancy", r.Units, nil, now); err != nil {
				return recoveryAbortResult{}, err
			}
		default:
			return recoveryAbortResult{}, ErrClaimIntegrity
		}
		for _, d := range deltas {
			if err := resources.ValidateChange(x.Snapshot, d); err != nil {
				return recoveryAbortResult{}, err
			}
			eventKind := "release"
			if outcome.ConserveCounter && r.Kind == "counter" {
				eventKind = "consume"
			} else if outcome.ConserveOccupancy && r.Kind == "occupancy" {
				eventKind = "encumber"
			}
			applied, err := rx.ApplyRecoveryDelta(d, attemptID, scope, eventKind, nil, now)
			if err != nil {
				return recoveryAbortResult{}, err
			}
			if !applied {
				return recoveryAbortResult{}, ledger.ErrAbortConflict
			}
		}
		applied, err := rx.SettleReservation(r.ReservationID, r.Phase, phase)
		if err != nil {
			return recoveryAbortResult{}, err
		}
		if !applied {
			return recoveryAbortResult{}, ledger.ErrAbortConflict
		}
		outcomes = append(outcomes, out)
	}
	if outcome.RevokePermit {
		permit, found, err := rx.GetPreparationPermit(attemptID)
		if err != nil || !found {
			return recoveryAbortResult{}, ErrClaimIntegrity
		}
		applied, err := rx.RevokeRecoveryPermit(permit.PermitID, now)
		if err != nil {
			return recoveryAbortResult{}, err
		}
		if !applied {
			return recoveryAbortResult{}, ledger.ErrAbortConflict
		}
	}
	if applied, err := rx.FinishRecoveryAttempt(attemptID, attempt.Phase, outcome.TerminalOutcome, now); err != nil {
		return recoveryAbortResult{}, err
	} else if !applied {
		return recoveryAbortResult{}, ledger.ErrAbortConflict
	}
	// Read after the attempt is terminal: the list is the complete durable
	// fact base (including the attempt closed above) for the resolver.
	attemptFacts, err := rx.ListRecoveryAttemptOutcomes(key)
	if err != nil {
		return recoveryAbortResult{}, err
	}
	state, code, reasons := domain.ResolveResult(recoveryResultFacts(op, attemptFacts, system, now))
	if state != domain.Running {
		result.Result = &domain.OperationResult{OperationID: key.OperationID, State: state, Code: code, Reasons: reasons, RetrySafety: domain.Undetermined, Attempts: attemptIDs(attemptFacts), Resources: outcomes, DecisionRefs: []domain.ID{}, OutputAvailability: "none"}
		if facts.OutputStored {
			result.Result.OutputAvailability = "available"
			result.Result.OutputRef = domain.ID("operation/" + string(key.OperationID))
		}
		raw, err := json.Marshal(result.Result)
		if err != nil {
			return recoveryAbortResult{}, err
		}
		applied, err := rx.CompleteRecoveryOperation(key, op.Version, result.Result, system, op.LeaseUntil, now)
		if err != nil {
			return recoveryAbortResult{}, err
		}
		if !applied {
			return recoveryAbortResult{}, ledger.ErrAbortConflict
		}
		if err := rx.AppendRecoveryEvent(key, "operation_terminal", raw, now); err != nil {
			return recoveryAbortResult{}, err
		}
	} else if system {
		// Takeover without a terminal fact (§7: bump generation, revoke
		// standing; the resolver keeps the orphan running until its deadline).
		// Parking lease_until at the deadline keeps it out of the due set
		// until the resolver can complete it, without resurrecting ownership.
		if applied, err := rx.BumpRecoveryGeneration(key, op.Version, op.DeadlineAt, now); err != nil {
			return recoveryAbortResult{}, err
		} else if !applied {
			return recoveryAbortResult{}, ledger.ErrAbortConflict
		}
	}
	outcomesRaw, err := json.Marshal(outcomes)
	if err != nil {
		return recoveryAbortResult{}, err
	}
	if err := rx.AppendRecoveryEvent(key, "attempt_settled", outcomesRaw, now); err != nil {
		return recoveryAbortResult{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return recoveryAbortResult{}, err
	}
	if err := rx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: attemptID, Kind: "abort", InputHash: hash, ResultJSON: raw, CreatedAt: now}); err != nil {
		return recoveryAbortResult{}, err
	}
	if err := rx.InsertAudit(store.AuditRecord{AuditID: domain.ID("abort/" + string(attemptID)), PrincipalID: actor, Namespace: key.Namespace, Action: "abort", TargetRef: key.OperationID, EvidenceRef: attemptID, CreatedAt: now}); err != nil {
		return recoveryAbortResult{}, err
	}
	return result, nil
}

// validateReleaseDelta guards the not-executed occupancy release: exactly one
// component returns the reserved/active quantity and no accounting column
// (committed/adjustment/uncertain) may move.
func validateReleaseDelta(d domain.ResourceDelta, q domain.Units) error {
	if d.Committed != 0 || d.Adjustment != 0 || d.Uncertain != 0 {
		return ErrClaimIntegrity
	}
	reservedRelease := d.Reserved == -q && d.Active == 0
	activeRelease := d.Reserved == 0 && d.Active == -q
	if !reservedRelease && !activeRelease {
		return ErrClaimIntegrity
	}
	return nil
}

// recoveryResultFacts derives the resolver inputs from durable state read
// after the aborted attempt reached its terminal outcome. system marks the
// sweep path — the only caller allowed to assert the ExecutionInterrupted
// recovery reason, and only when no permit was ever granted (03 §6: an owner
// that lost the lease while holding an unconfirmed send permit resolves to
// unknown instead, which UnconfirmedDispatchPending already covers).
func recoveryResultFacts(op store.ClaimOperationRecord, attemptFacts []store.RecoveryAttemptOutcome, system bool, now domain.Millis) domain.ResultFacts {
	allNotExecutedOrEnded := true
	unconfirmed := false
	for _, p := range attemptFacts {
		switch {
		case p.Phase != "terminal":
			allNotExecutedOrEnded, unconfirmed = false, true
		case p.ExternalOutcome == "not_executed":
		case p.ExternalOutcome == "unknown":
			allNotExecutedOrEnded, unconfirmed = false, true
		case p.ReportEnded:
		default:
			allNotExecutedOrEnded = false
		}
	}
	return domain.ResultFacts{
		UnconfirmedDispatchPending:   unconfirmed,
		CancelRequested:              op.CancelRequestedAt != nil,
		AllActionsNotExecutedOrEnded: allNotExecutedOrEnded,
		DeadlineExceeded:             op.DeadlineAt <= now,
		AttemptsExhausted:            op.PermitsIssued >= op.MaxAttempts,
		ExecutionInterrupted:         system && op.PermitsIssued == 0 && allNotExecutedOrEnded,
	}
}

func attemptIDs(outcomes []store.RecoveryAttemptOutcome) []domain.ID {
	ids := make([]domain.ID, 0, len(outcomes))
	for _, o := range outcomes {
		ids = append(ids, o.AttemptID)
	}
	return ids
}
