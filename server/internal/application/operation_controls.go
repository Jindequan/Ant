package application

import (
	"context"
	"errors"
	"slices"

	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
)

var (
	ErrOperationCancelForbidden = errors.New("operation cancel: forbidden")
	ErrOperationCancelInvalid   = errors.New("operation cancel: invalid input")
	ErrOperationCancelConflict  = errors.New("operation cancel: command conflict")
)

var (
	ErrAdminForbidden = errors.New("admin command: forbidden")
	ErrAdminInvalid   = errors.New("admin command: invalid input")
	ErrAdminNotFound  = errors.New("admin command: not found")
	ErrAdminConflict  = errors.New("admin command: conflict")
)

type adminLedger interface {
	RecordObservation(context.Context, domain.AuthContext, domain.ID, domain.ObservationEvidence) (domain.RefreshResult, error)
	Adjust(context.Context, domain.AuthContext, domain.AdjustmentInput) (domain.CommandAck, error)
	ClearHold(context.Context, domain.AuthContext, domain.ClearHoldInput) (domain.CommandAck, error)
}

type AdminOperations struct{ ledger adminLedger }

func NewAdminOperations(l adminLedger) (*AdminOperations, error) {
	if l == nil {
		return nil, ErrAdminInvalid
	}
	return &AdminOperations{ledger: l}, nil
}

func (c *AdminOperations) RefreshObservation(ctx context.Context, auth domain.AuthContext, commandID domain.ID, evidence domain.ObservationEvidence) (domain.RefreshResult, error) {
	if c == nil || c.ledger == nil || commandID == "" {
		return domain.RefreshResult{}, ErrAdminInvalid
	}
	result, err := c.ledger.RecordObservation(ctx, auth, commandID, evidence)
	return result, mapAdminError(err)
}

func (c *AdminOperations) Adjust(ctx context.Context, auth domain.AuthContext, input domain.AdjustmentInput) (domain.CommandAck, error) {
	if c == nil || c.ledger == nil || input.CommandID == "" {
		return domain.CommandAck{}, ErrAdminInvalid
	}
	ack, err := c.ledger.Adjust(ctx, auth, input)
	return ack, mapAdminError(err)
}

func (c *AdminOperations) ClearHold(ctx context.Context, auth domain.AuthContext, input domain.ClearHoldInput) (domain.CommandAck, error) {
	if c == nil || c.ledger == nil || input.CommandID == "" || input.HoldID == "" {
		return domain.CommandAck{}, ErrAdminInvalid
	}
	ack, err := c.ledger.ClearHold(ctx, auth, input)
	return ack, mapAdminError(err)
}

func mapAdminError(err error) error {
	switch {
	case errors.Is(err, commands.ErrAdminForbidden):
		return ErrAdminForbidden
	case errors.Is(err, commands.ErrAdminInvalid):
		return ErrAdminInvalid
	case errors.Is(err, commands.ErrAdminNotFound):
		return ErrAdminNotFound
	case errors.Is(err, commands.ErrAdminConflict):
		return ErrAdminConflict
	default:
		return err
	}
}

type cancelLedger interface {
	RequestCancel(context.Context, domain.AuthContext, domain.OperationKey, domain.ID) (domain.CancellationAck, error)
}

var (
	ErrOperationReconcileForbidden = errors.New("operation reconcile: forbidden")
	ErrOperationReconcileInvalid   = errors.New("operation reconcile: invalid input")
	ErrOperationReconcileConflict  = errors.New("operation reconcile: conflict")
)

type reconcileLedger interface {
	Reconcile(context.Context, domain.AuthContext, domain.ReconciliationInput) (domain.ReconciliationView, error)
}

// OperationReconciliations is the management boundary for late provider
// evidence. Authorization is checked here as well as at the HTTP boundary so
// internal callers cannot bypass the manage permission.
type OperationReconciliations struct{ reconciler reconcileLedger }

func NewOperationReconciliations(reconciler reconcileLedger) (*OperationReconciliations, error) {
	if reconciler == nil {
		return nil, ErrOperationReconcileInvalid
	}
	return &OperationReconciliations{reconciler: reconciler}, nil
}

func (c *OperationReconciliations) Reconcile(ctx context.Context, auth domain.AuthContext, input domain.ReconciliationInput) (domain.ReconciliationView, error) {
	if c == nil || c.reconciler == nil || input.CommandID == "" || input.AttemptID == "" || len(input.EvidenceRefs) == 0 {
		return domain.ReconciliationView{}, ErrOperationReconcileInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermManage) {
		return domain.ReconciliationView{}, ErrOperationReconcileForbidden
	}
	view, err := c.reconciler.Reconcile(ctx, auth, input)
	switch {
	case errors.Is(err, commands.ErrReconciliationForbidden):
		return domain.ReconciliationView{}, ErrOperationReconcileForbidden
	case errors.Is(err, commands.ErrReconciliationInvalid):
		return domain.ReconciliationView{}, ErrOperationReconcileInvalid
	case errors.Is(err, commands.ErrReconciliationConflict):
		return domain.ReconciliationView{}, ErrOperationReconcileConflict
	default:
		return view, err
	}
}

// OperationControls adapts the transactional cancellation command for the
// application/API boundary. The acknowledgement records intent only; it
// never claims the provider has stopped or the operation is terminal.
type OperationControls struct{ canceler cancelLedger }

func NewOperationControls(canceler cancelLedger) (*OperationControls, error) {
	if canceler == nil {
		return nil, ErrOperationCancelInvalid
	}
	return &OperationControls{canceler: canceler}, nil
}

func (c *OperationControls) RequestCancel(ctx context.Context, auth domain.AuthContext, key domain.OperationKey, commandID domain.ID) (domain.CancellationAck, error) {
	if c == nil || c.canceler == nil {
		return domain.CancellationAck{}, ErrOperationCancelInvalid
	}
	ack, err := c.canceler.RequestCancel(ctx, auth, key, commandID)
	switch {
	case errors.Is(err, commands.ErrCancelForbidden):
		return domain.CancellationAck{}, ErrOperationCancelForbidden
	case errors.Is(err, commands.ErrCancelInvalid):
		return domain.CancellationAck{}, ErrOperationCancelInvalid
	case errors.Is(err, commands.ErrCancelConflict):
		return domain.CancellationAck{}, ErrOperationCancelConflict
	default:
		return ack, err
	}
}
