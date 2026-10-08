// Package ledger defines the Ledger port: the only path for durable state
// changes (claims, reservations, permits, settlement, config activation) and
// the owner of the DispatchPermit handle (K2). The AbortBeforeDispatch
// transaction (K3) is protocol-frozen here; its transactional implementation
// lands in T2.2 — this batch must not claim runtime verification for it.
package ledger

import (
	"context"
	"errors"

	"ant/server/internal/domain"
)

var (
	ErrClaimInvalid   = errors.New("ledger claim: invalid input")
	ErrClaimForbidden = errors.New("ledger claim: forbidden")
	ErrClaimNotReady  = errors.New("ledger claim: execution not ready")
)

type ClaimReply struct {
	Kind      string // new / inProgress / replay / conflict
	Owner     *domain.OwnerToken
	Operation *domain.OperationView
	Result    *domain.OperationResult
}

type ClaimPolicy struct {
	RuntimeID          domain.ID
	Now                domain.Millis
	LeaseMillis        domain.Millis
	MaxRuntimeAttempts int
}

type PrepareReply struct {
	Kind    string // prepared / blocked / terminal
	Attempt *domain.PreparedAttempt
	Reasons []domain.Reason
}

type SettleReply struct {
	Continue bool
	Result   *domain.OperationResult
}

type ControlSignal = domain.ControlSignal

type RefreshResult = domain.RefreshResult

type Ledger interface {
	Claim(context.Context, domain.AuthContext, domain.ExecuteInput, string) (ClaimReply, error)
	Quote(context.Context, domain.AuthContext, domain.ExecutionPlan) ([]domain.ResourceSnapshot, error)
	TryPrepare(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID, domain.ExecutionPlan, []domain.GuardEvidence, string, []byte) (PrepareReply, error)
	AuthorizeDispatch(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID) (DispatchPermit, error)
	ConsumeDispatchPermit(context.Context, domain.AuthContext, domain.OwnerToken, DispatchPermit, string) error
	// AbortBeforeDispatch (K3, protocol frozen): closes a prepared/granted
	// attempt without external execution or conserves it as unknown. Full
	// transactional semantics in T2.2 — see docs/design/03-EXECUTION.md §9.
	AbortBeforeDispatch(context.Context, domain.AuthContext, AbortBeforeDispatchInput) (AbortOutcome, error)
	RenewLease(context.Context, domain.AuthContext, []domain.OwnerToken, domain.Millis) error
	ReadControlSignals(context.Context, domain.AuthContext, domain.ID, int) ([]ControlSignal, error)
	RequestCancel(context.Context, domain.AuthContext, domain.OperationKey, domain.ID) (domain.CancellationAck, error)
	AppendOutput(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID, domain.OutputChunk) error
	Settle(context.Context, domain.AuthContext, domain.OwnerToken, domain.ExecutionReport) (SettleReply, error)
	CompleteWithoutAttempt(context.Context, domain.AuthContext, domain.OwnerToken, []domain.Reason) (domain.OperationResult, error)
	Sweep(context.Context, domain.AuthContext, domain.Millis, int) error
	ActivateConfig(context.Context, domain.AuthContext, domain.ActivationInput) (domain.ActivationResult, error)
	ReorderPool(context.Context, domain.AuthContext, domain.ID, domain.ReorderPoolInput) (domain.ResourcePool, error)
	RecordGuardEvidence(context.Context, domain.AuthContext, domain.GuardEvidence) error
	RecordObservation(context.Context, domain.AuthContext, domain.ID, domain.ObservationEvidence) (RefreshResult, error)
	Adjust(context.Context, domain.AuthContext, domain.AdjustmentInput) (domain.CommandAck, error)
	ClearHold(context.Context, domain.AuthContext, domain.ClearHoldInput) (domain.CommandAck, error)
	Reconcile(context.Context, domain.AuthContext, domain.ReconciliationInput) (domain.ReconciliationView, error)
}
