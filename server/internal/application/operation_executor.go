package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/hooks"
	"ant/server/internal/ledger"
	"ant/server/internal/transport"
)

var (
	ErrExecutorInvalid       = errors.New("operation executor: invalid dependencies")
	ErrExecutorPlanIntegrity = errors.New("operation executor: planner returned inconsistent candidate")
	ErrExecutorStepFailed    = errors.New("operation executor: execution step failed")
)

type ExecutionPlanner interface {
	PlanPool(context.Context, domain.AuthContext, domain.ExecuteInput, domain.ID) (CandidatePlan, error)
}

type ExecutionLedger interface {
	Quote(context.Context, domain.AuthContext, domain.ExecutionPlan) ([]domain.ResourceSnapshot, error)
	TryPrepare(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID, domain.ExecutionPlan, []domain.GuardEvidence, string, []byte) (ledger.PrepareReply, error)
	AuthorizeDispatch(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID) (ledger.DispatchPermit, error)
	AbortBeforeDispatch(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID, ledger.UnsentProof, ledger.DispatchPermit) (ledger.AbortOutcome, error)
	AppendOutput(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID, domain.OutputChunk) error
	Settle(context.Context, domain.AuthContext, domain.OwnerToken, domain.ExecutionReport) (ledger.SettleReply, error)
	CompleteWithoutAttempt(context.Context, domain.AuthContext, domain.OwnerToken, []domain.Reason) (domain.OperationResult, error)
}

type AdapterLookup interface {
	Adapter(domain.ID) (adapters.ProviderAdapter, bool)
}

type GuardEvidenceProvider interface {
	ForCandidate(context.Context, domain.AuthContext, domain.ExecutionPlan) ([]domain.GuardEvidence, error)
}

type ExecutionAdapterMap map[domain.ID]adapters.ProviderAdapter

func (m ExecutionAdapterMap) Adapter(providerType domain.ID) (adapters.ProviderAdapter, bool) {
	a, ok := m[providerType]
	return a, ok
}

type ExecutorConfig struct {
	Planner     ExecutionPlanner
	Ledger      ExecutionLedger
	Adapters    AdapterLookup
	Transport   transport.Port
	Guards      GuardEvidenceProvider
	Hooks       *hooks.Registry
	HookTimeout time.Duration
	OnError     func(error)
}

// OperationExecutor coordinates one accepted operation through planning,
// durable preparation, one authorized send, output persistence, and
// settlement. Provider-specific behavior remains behind registered adapters
// and domain policies. An ambiguous post-send failure is conservatively
// settled as unknown and is never automatically resent.
type CandidateOperationExecutor struct {
	planner      ExecutionPlanner
	ledger       ExecutionLedger
	adapters     AdapterLookup
	transport    transport.Port
	guards       GuardEvidenceProvider
	hooks        *hooks.Registry
	hookTimeout  time.Duration
	hookRevision string
	hookManifest json.RawMessage
	onError      func(error)
}

const maxExecutionReasons = 128
const maxCandidateAttemptsPerPass = 64

func NewCandidateOperationExecutor(cfg ExecutorConfig) (*CandidateOperationExecutor, error) {
	if cfg.Planner == nil || cfg.Ledger == nil || cfg.Adapters == nil || cfg.Transport == nil {
		return nil, ErrExecutorInvalid
	}
	if cfg.Hooks != nil {
		if cfg.HookTimeout <= 0 {
			return nil, ErrExecutorInvalid
		}
		revision, err := cfg.Hooks.Revision()
		if err != nil {
			return nil, ErrExecutorInvalid
		}
		manifest, err := cfg.Hooks.Manifest()
		if err != nil {
			return nil, ErrExecutorInvalid
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			return nil, ErrExecutorInvalid
		}
		return &CandidateOperationExecutor{planner: cfg.Planner, ledger: cfg.Ledger, adapters: cfg.Adapters, transport: cfg.Transport, guards: cfg.Guards, hooks: cfg.Hooks, hookTimeout: cfg.HookTimeout, hookRevision: revision, hookManifest: manifestJSON, onError: cfg.OnError}, nil
	}
	return &CandidateOperationExecutor{planner: cfg.Planner, ledger: cfg.Ledger, adapters: cfg.Adapters, transport: cfg.Transport, guards: cfg.Guards, hooks: cfg.Hooks, hookTimeout: cfg.HookTimeout, onError: cfg.OnError}, nil
}

func (e *CandidateOperationExecutor) Execute(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, input domain.ExecuteInput) {
	if e == nil || ctx == nil || owner.Key.Namespace != auth.Namespace || owner.Key.OperationID != input.OperationID || owner.OwnerID == "" || owner.Generation < 1 {
		return
	}
	var outputSequence int64
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		reasons := []domain.Reason{}
		triedOffers := map[domain.ID]struct{}{}
		var settled bool
		for candidateRound := 0; candidateRound < maxCandidateAttemptsPerPass; candidateRound++ {
			if err := ctx.Err(); err != nil {
				return
			}
			attemptID, err := newAttemptID()
			if err != nil {
				reasons = append(reasons, executorReason("internal", "attempt_identity_unavailable"))
				break
			}
			fresh, err := e.planner.PlanPool(ctx, auth, input, attemptID)
			if err != nil {
				reasons = append(reasons, plannerFailureReason(err))
				break
			}
			reasons = append(reasons, rejectedReasons(fresh.Rejected)...)
			candidates := fresh.Candidates
			plannerOrder := candidateOfferIDs(candidates)
			if e.hooks != nil {
				ordered, orderErr := e.orderCandidates(ctx, input, candidates)
				if orderErr != nil {
					e.report(orderErr)
					reasons = append(reasons, executorReason("hook", "candidate_order_failed"))
					break
				}
				candidates = ordered
			}
			candidate, ok := firstUntriedCandidate(candidates, triedOffers)
			if !ok {
				break
			}
			triedOffers[candidate.Offer.OfferID] = struct{}{}
			if candidate.Plan.PlanID == "" || candidate.Plan.ProviderTypeID == "" || candidate.Plan.OfferID != candidate.Offer.OfferID {
				e.report(ErrExecutorPlanIntegrity)
				return
			}
			if err := e.checkLifecycleGuard(ctx, hooks.PointCandidatePrepare, input, attemptID, candidate.Plan, struct {
				OfferID        domain.ID `json:"offerId"`
				ProviderTypeID domain.ID `json:"providerTypeId"`
			}{candidate.Offer.OfferID, candidate.Plan.ProviderTypeID}); err != nil {
				e.report(err)
				reasons = append(reasons, domain.Reason{Group: "hook", Code: "candidate_rejected", ScopeRef: candidate.Offer.OfferID})
				continue
			}
			decisionTrace, err := e.decisionTrace(fresh, plannerOrder, candidates, candidate)
			if err != nil {
				e.report(err)
				e.failWithoutAttempt(ctx, auth, owner, []domain.Reason{executorReason("hook", "decision_trace_unavailable")})
				return
			}
			quote, err := e.ledger.Quote(ctx, auth, candidate.Plan)
			if err != nil {
				reasons = append(reasons, executorReason("admission", "quote_unavailable"))
				continue
			}
			if reason, fits := quoteFits(candidate.Plan, quote); !fits {
				reasons = append(reasons, reason)
				continue
			}
			adapter, ok := e.adapters.Adapter(candidate.Plan.ProviderTypeID)
			if !ok || adapter == nil {
				reasons = append(reasons, executorReason("capability", "adapter_unavailable"))
				continue
			}
			descriptor := adapter.Descriptor()
			if descriptor.TypeID != candidate.Plan.ProviderTypeID || descriptor.AdapterRevision != candidate.Plan.AdapterRevision || !slices.Contains(descriptor.SupportedDomains, input.Request.Domain) {
				reasons = append(reasons, executorReason("capability", "adapter_unavailable"))
				continue
			}
			encoded, err := adapter.Encode(input.Request, candidate.Plan)
			if err != nil {
				reasons = append(reasons, executorReason("request", "encoding_failed"))
				continue
			}
			var guards []domain.GuardEvidence
			if e.guards != nil {
				guards, err = e.guards.ForCandidate(ctx, auth, candidate.Plan)
				if err != nil {
					reasons = append(reasons, executorReason("admission", "guard_evidence_unavailable"))
					continue
				}
			}
			handle, err := e.transport.Prepare(ctx, auth, owner, attemptID, encoded, candidate.Plan)
			if err != nil {
				reasons = append(reasons, transportFailureReason(err))
				continue
			}
			prepare, err := e.ledger.TryPrepare(ctx, auth, owner, attemptID, candidate.Plan, guards, handle.RequestDigest(), decisionTrace)
			if err != nil {
				e.transport.Discard(handle)
				e.report(ErrExecutorStepFailed)
				return
			}
			if prepare.Kind == "blocked" {
				e.transport.Discard(handle)
				reasons = append(reasons, slices.Clone(prepare.Reasons)...)
				continue
			}
			if prepare.Kind != "prepared" || prepare.Attempt == nil || prepare.Attempt.AttemptID != attemptID || prepare.Attempt.RequestDigest != handle.RequestDigest() {
				e.transport.Discard(handle)
				e.report(ErrExecutorPlanIntegrity)
				return
			}
			if err := e.checkLifecycleGuard(ctx, hooks.PointAttemptAuthorize, input, attemptID, candidate.Plan, struct {
				OfferID domain.ID `json:"offerId"`
				PlanID  domain.ID `json:"planId"`
			}{candidate.Offer.OfferID, candidate.Plan.PlanID}); err != nil {
				e.transport.Discard(handle)
				e.abort(ctx, auth, owner, attemptID, ledger.ProofNone, ledger.DispatchPermit{})
				e.report(err)
				return
			}
			permit, err := e.ledger.AuthorizeDispatch(ctx, auth, owner, attemptID)
			if err != nil {
				e.transport.Discard(handle)
				e.abort(ctx, auth, owner, attemptID, ledger.ProofNone, ledger.DispatchPermit{})
				e.report(ErrExecutorStepFailed)
				return
			}
			if err := e.checkLifecycleGuard(ctx, hooks.PointDispatchBeforeSend, input, attemptID, candidate.Plan, struct {
				OfferID     domain.ID `json:"offerId"`
				PlanID      domain.ID `json:"planId"`
				RequestHash string    `json:"requestHash"`
			}{candidate.Offer.OfferID, candidate.Plan.PlanID, handle.RequestDigest()}); err != nil {
				e.transport.Discard(handle)
				e.abort(ctx, auth, owner, attemptID, ledger.ProofNone, permit)
				e.report(err)
				return
			}
			response, err := e.dispatch(ctx, permit, handle)
			if err != nil {
				e.abort(ctx, auth, owner, attemptID, ledger.ProofNone, permit)
				e.report(ErrExecutorStepFailed)
				return
			}
			if response.Body == nil {
				e.abort(ctx, auth, owner, attemptID, ledger.ProofNone, permit)
				e.report(ErrExecutorStepFailed)
				return
			}
			report, decodeErr := adapter.Decode(ctx, response, candidate.Plan, attemptID, func(data []byte) error {
				outputSequence++
				return e.ledger.AppendOutput(ctx, auth, owner, attemptID, domain.OutputChunk{Sequence: outputSequence, Data: slices.Clone(data)})
			})
			closeErr := response.Body.Close()
			if decodeErr != nil || closeErr != nil || report.AttemptID != attemptID || report.ReportID == "" {
				e.abort(ctx, auth, owner, attemptID, ledger.ProofNone, permit)
				e.report(ErrExecutorStepFailed)
				return
			}
			if err := e.checkLifecycleGuard(ctx, hooks.PointReportBeforeSettle, input, attemptID, candidate.Plan, struct {
				ReportID  domain.ID `json:"reportId"`
				Outcome   string    `json:"outcome"`
				MetricNum int       `json:"metricCount"`
				RawDigest string    `json:"rawDigest,omitempty"`
			}{report.ReportID, report.Outcome, len(report.Metrics), report.RawDigest}); err != nil {
				e.abort(ctx, auth, owner, attemptID, ledger.ProofNone, permit)
				e.report(err)
				return
			}
			settlement, err := e.ledger.Settle(ctx, auth, owner, report)
			if err != nil {
				e.report(ErrExecutorStepFailed)
				return
			}
			if settlement.Result != nil || !settlement.Continue {
				return
			}
			settled = true
			break
		}
		if settled {
			continue
		}
		if len(triedOffers) == maxCandidateAttemptsPerPass {
			reasons = append(reasons, executorReason("admission", "candidate_search_limit_reached"))
		}
		e.failWithoutAttempt(ctx, auth, owner, reasons)
		return
	}
}

func (e *CandidateOperationExecutor) checkLifecycleGuard(ctx context.Context, point hooks.Point, input domain.ExecuteInput, attemptID domain.ID, plan domain.ExecutionPlan, payload any) error {
	if e == nil || e.hooks == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return hooks.ErrHookFailed
	}
	hookCtx, cancel := context.WithTimeout(ctx, e.hookTimeout)
	defer cancel()
	return e.hooks.Check(hookCtx, hooks.Event{
		Point: point, OperationID: string(input.OperationID), AttemptID: string(attemptID),
		ConfigRevision: string(plan.ConfigRevision), OccurredAtUnixMs: time.Now().UnixMilli(), Payload: raw,
	})
}

// orderCandidates exposes only stable offer/provider identifiers to trusted
// orderers. The core planner remains responsible for eligibility and the
// registry permits only an exact permutation, so Hooks cannot add candidates.
func (e *CandidateOperationExecutor) orderCandidates(ctx context.Context, input domain.ExecuteInput, candidates []domain.Candidate) ([]domain.Candidate, error) {
	type candidateSummary struct {
		OfferID        domain.ID `json:"offerId"`
		ProviderTypeID domain.ID `json:"providerTypeId"`
	}
	summaries := make([]candidateSummary, 0, len(candidates))
	byID := make(map[domain.ID]domain.Candidate, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := candidate.Offer.OfferID
		if id == "" || candidate.Plan.ProviderTypeID == "" {
			return nil, ErrExecutorPlanIntegrity
		}
		if _, exists := byID[id]; exists {
			return nil, ErrExecutorPlanIntegrity
		}
		byID[id] = candidate
		ids = append(ids, string(id))
		summaries = append(summaries, candidateSummary{OfferID: id, ProviderTypeID: candidate.Plan.ProviderTypeID})
	}
	payload, err := json.Marshal(summaries)
	if err != nil {
		return nil, ErrExecutorPlanIntegrity
	}
	hookCtx, cancel := context.WithTimeout(ctx, e.hookTimeout)
	defer cancel()
	orderedIDs, err := e.hooks.OrderCandidates(hookCtx, hooks.Event{
		Point: hooks.PointCandidatesReady, OperationID: string(input.OperationID), Payload: payload,
	}, ids)
	if err != nil {
		return nil, err
	}
	ordered := make([]domain.Candidate, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		candidate, exists := byID[domain.ID(id)]
		if !exists {
			return nil, ErrExecutorPlanIntegrity
		}
		ordered = append(ordered, candidate)
	}
	return ordered, nil
}

const maxDecisionCandidateIDs = 64

type executionDecisionTrace struct {
	HookRegistryRevision      string                     `json:"hookRegistryRevision,omitempty"`
	HookRegistryManifest      json.RawMessage            `json:"hookRegistryManifest,omitempty"`
	PlannerCandidateOrder     []domain.ID                `json:"plannerCandidateOrder"`
	HookCandidateOrder        []domain.ID                `json:"hookCandidateOrder"`
	SelectedOfferID           domain.ID                  `json:"selectedOfferId"`
	PoolID                    domain.ID                  `json:"poolId"`
	PoolVersion               int64                      `json:"poolVersion"`
	MemberPriority            int64                      `json:"memberPriority"`
	ConfigRevision            domain.ID                  `json:"configRevision"`
	Candidates                []domain.CandidateDecision `json:"candidates"`
	PlannerCandidateTruncated bool                       `json:"plannerCandidateTruncated"`
	HookCandidateTruncated    bool                       `json:"hookCandidateTruncated"`
}

func (e *CandidateOperationExecutor) decisionTrace(plan CandidatePlan, plannerOrder []domain.ID, ordered []domain.Candidate, selected domain.Candidate) ([]byte, error) {
	if selected.Offer.OfferID == "" || selected.Plan.PoolID == "" || selected.Plan.ConfigRevision == "" {
		return nil, ErrExecutorPlanIntegrity
	}
	decisions := make([]domain.CandidateDecision, 0, min(len(ordered), maxDecisionCandidateIDs))
	for _, candidate := range ordered[:min(len(ordered), maxDecisionCandidateIDs)] {
		decisions = append(decisions, domain.CandidateDecision{
			OfferID: candidate.Offer.OfferID, Priority: candidate.Member.Priority,
			Reasons: slices.Clone(candidate.Reasons),
		})
	}
	trace := executionDecisionTrace{
		HookRegistryRevision:      e.hookRevision,
		PlannerCandidateOrder:     slices.Clone(plannerOrder[:min(len(plannerOrder), maxDecisionCandidateIDs)]),
		HookCandidateOrder:        slices.Clone(candidateOfferIDs(ordered)[:min(len(ordered), maxDecisionCandidateIDs)]),
		SelectedOfferID:           selected.Offer.OfferID,
		PoolID:                    selected.Plan.PoolID,
		PoolVersion:               selected.Plan.PoolVersion,
		MemberPriority:            selected.Plan.MemberPriority,
		ConfigRevision:            plan.ConfigRevision,
		Candidates:                decisions,
		PlannerCandidateTruncated: len(plannerOrder) > maxDecisionCandidateIDs,
		HookCandidateTruncated:    len(ordered) > maxDecisionCandidateIDs,
	}
	if len(e.hookManifest) > 0 {
		trace.HookRegistryManifest = slices.Clone(e.hookManifest)
	}
	raw, err := json.Marshal(trace)
	if err != nil || len(raw) > 64*1024 {
		return nil, ErrExecutorPlanIntegrity
	}
	return raw, nil
}

func candidateOfferIDs(candidates []domain.Candidate) []domain.ID {
	ids := make([]domain.ID, len(candidates))
	for i, candidate := range candidates {
		ids[i] = candidate.Offer.OfferID
	}
	return ids
}

func (e *CandidateOperationExecutor) abort(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, proof ledger.UnsentProof, permit ledger.DispatchPermit) {
	_, err := e.ledger.AbortBeforeDispatch(ctx, auth, owner, attemptID, proof, permit)
	if err != nil {
		e.report(ErrExecutorStepFailed)
	}
}

func (e *CandidateOperationExecutor) dispatch(ctx context.Context, permit ledger.DispatchPermit, handle transport.PreparedHTTPRequest) (response adapters.RawResponse, err error) {
	defer e.transport.Discard(handle)
	return e.transport.Dispatch(ctx, permit, handle)
}

func (e *CandidateOperationExecutor) failWithoutAttempt(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, reasons []domain.Reason) {
	reasons = boundedUniqueReasons(reasons)
	if len(reasons) == 0 {
		reasons = []domain.Reason{executorReason("admission", "no_candidate_available")}
	}
	if _, err := e.ledger.CompleteWithoutAttempt(ctx, auth, owner, reasons); err != nil {
		e.report(ErrExecutorStepFailed)
	}
}

func boundedUniqueReasons(reasons []domain.Reason) []domain.Reason {
	result := make([]domain.Reason, 0, min(len(reasons), maxExecutionReasons))
	seen := make(map[domain.Reason]struct{}, cap(result))
	for _, reason := range reasons {
		if reason.Group == "" || reason.Code == "" {
			continue
		}
		if _, exists := seen[reason]; exists {
			continue
		}
		seen[reason] = struct{}{}
		result = append(result, reason)
		if len(result) == maxExecutionReasons {
			break
		}
	}
	return result
}

func (e *CandidateOperationExecutor) report(err error) {
	if err != nil && e.onError != nil {
		e.onError(err)
	}
}

func firstUntriedCandidate(candidates []domain.Candidate, tried map[domain.ID]struct{}) (domain.Candidate, bool) {
	for _, candidate := range candidates {
		if _, wasTried := tried[candidate.Offer.OfferID]; !wasTried {
			return candidate, true
		}
	}
	return domain.Candidate{}, false
}

func rejectedReasons(rejected []CandidateRejection) []domain.Reason {
	var reasons []domain.Reason
	for _, item := range rejected {
		for _, reason := range item.Reasons {
			reasons = append(reasons, reason)
		}
	}
	return reasons
}

func quoteFits(plan domain.ExecutionPlan, snapshots []domain.ResourceSnapshot) (domain.Reason, bool) {
	byID := make(map[domain.ID]domain.ResourceSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.ResourceID == "" || snapshot.Available < 0 {
			return executorReason("admission", "quote_invalid"), false
		}
		if _, exists := byID[snapshot.ResourceID]; exists {
			return executorReason("admission", "quote_invalid"), false
		}
		byID[snapshot.ResourceID] = snapshot
	}
	if len(plan.Requirements) == 0 {
		return executorReason("plan", "requirements_missing"), false
	}
	for _, requirement := range plan.Requirements {
		if requirement.UpperBound < 0 || len(requirement.ResourceIDs) == 0 {
			return executorReason("plan", "requirements_invalid"), false
		}
		switch requirement.Mode {
		case "all_constraints":
			for _, resourceID := range requirement.ResourceIDs {
				snapshot, exists := byID[resourceID]
				if !exists || snapshot.Available < requirement.UpperBound {
					return domain.Reason{Group: "capacity", Code: "capacity_exhausted", ScopeRef: resourceID}, false
				}
			}
		case "provider_entitlement_waterfall":
			var available domain.Units
			for _, resourceID := range requirement.ResourceIDs {
				snapshot, exists := byID[resourceID]
				if !exists {
					return executorReason("plan", "quote_resource_missing"), false
				}
				var err error
				available, err = domain.AddUnits(available, snapshot.Available)
				if err != nil {
					return executorReason("plan", "quote_invalid"), false
				}
			}
			if available < requirement.UpperBound {
				return executorReason("capacity", "capacity_exhausted"), false
			}
		default:
			return executorReason("plan", "requirements_invalid"), false
		}
	}
	return domain.Reason{}, true
}

func executorReason(group, code string) domain.Reason {
	return domain.Reason{Group: group, Code: code}
}

func plannerFailureReason(err error) domain.Reason {
	switch {
	case errors.Is(err, ErrCandidatePolicyMissing):
		return executorReason("domain", "policy_unavailable")
	case errors.Is(err, ErrCandidatePolicyFailure):
		return executorReason("domain", "policy_failed")
	case errors.Is(err, ErrCandidateEvidenceStale):
		return executorReason("evidence", "candidate_evidence_stale")
	case errors.Is(err, ErrCandidatePoolMissing):
		return executorReason("eligibility", "pool_unavailable")
	default:
		return executorReason("planning", "candidate_planning_failed")
	}
}

func transportFailureReason(err error) domain.Reason {
	if errors.Is(err, transport.ErrCredentialUnavailable) {
		return executorReason("credential", "credential_unavailable")
	}
	return executorReason("request", "request_preparation_failed")
}

func newAttemptID() (domain.ID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("attempt identity: %w", err)
	}
	return domain.ID("attempt-" + hex.EncodeToString(raw[:])), nil
}

var _ OperationExecutor = (*CandidateOperationExecutor)(nil)
