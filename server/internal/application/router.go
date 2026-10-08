package application

import (
	"context"
	"errors"
	"slices"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/runtime"
	"ant/server/internal/store"
)

var (
	ErrSubmitInvalid      = errors.New("operation submit: invalid input")
	ErrSubmitForbidden    = errors.New("operation submit: forbidden")
	ErrSubmitBusy         = errors.New("operation submit: host is at capacity")
	ErrSubmitNotReady     = errors.New("operation submit: execution is not ready")
	ErrSubmitConflict     = errors.New("operation submit: operation id conflicts")
	ErrSubmitUnavailable  = errors.New("operation submit: storage unavailable")
	ErrSubmitHostTakeover = errors.New("operation submit: operation claimed but host takeover failed")
)

type ClaimCommand func(context.Context, domain.AuthContext, domain.ExecuteInput, string, ledger.ClaimPolicy) (ledger.ClaimReply, error)

type OperationExecutor interface {
	Execute(context.Context, domain.AuthContext, domain.OwnerToken, domain.ExecuteInput)
}

type OperationRouterConfig struct {
	RuntimeID          domain.ID
	LeaseMillis        domain.Millis
	MaxRuntimeAttempts int
	// ValidateRequest is pure domain-specific validation. New operations are
	// never claimed unless the composition root provides their request policy.
	ValidateRequest func(domain.DomainRequest) error
}

// Router owns the public operation submission boundary. Claim remains the
// sole durable owner issuer; Host capacity is reserved first, and execution
// receives a server-owned context only after the claim commits.
type Router struct {
	claim    ClaimCommand
	store    store.Store
	host     *runtime.ExecutionHost
	clock    interface{ Now() domain.Millis }
	executor OperationExecutor
	cfg      OperationRouterConfig
}

func NewRouter(st store.Store, claim ClaimCommand, host *runtime.ExecutionHost, clock interface{ Now() domain.Millis }, executor OperationExecutor, cfg OperationRouterConfig) (*Router, error) {
	if st == nil || claim == nil || host == nil || clock == nil || executor == nil || cfg.RuntimeID == "" || cfg.LeaseMillis <= 0 || cfg.MaxRuntimeAttempts < 1 || cfg.ValidateRequest == nil {
		return nil, ErrSubmitInvalid
	}
	return &Router{claim: claim, store: st, host: host, clock: clock, executor: executor, cfg: cfg}, nil
}

func (r *Router) Submit(ctx context.Context, auth domain.AuthContext, input domain.ExecuteInput) (domain.SubmitReply, error) {
	if r == nil || r.claim == nil || r.host == nil || r.executor == nil || ctx == nil {
		return domain.SubmitReply{}, ErrSubmitInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermExecute) {
		return domain.SubmitReply{}, ErrSubmitForbidden
	}
	if input.OperationID == "" || input.PoolID == "" || input.Request.Domain == "" || input.Request.CapabilityProfile == "" || input.DeadlineAt <= 0 {
		return domain.SubmitReply{}, ErrSubmitInvalid
	}
	fingerprint, err := domain.SemanticFingerprint(domain.FingerprintInput{
		Domain: input.Request.Domain, PoolID: input.PoolID,
		CapabilityProfile: input.Request.CapabilityProfile, RequiredAffinity: input.Affinity,
		Payload: input.Request.Payload,
	})
	if err != nil {
		return domain.SubmitReply{}, ErrSubmitInvalid
	}
	if reply, found, err := r.resolveExisting(ctx, auth, input, fingerprint); err != nil {
		return domain.SubmitReply{}, err
	} else if found {
		return reply, nil
	}
	if err := r.cfg.ValidateRequest(input.Request); err != nil {
		return domain.SubmitReply{}, ErrSubmitInvalid
	}
	reservation, err := r.host.Reserve()
	if err != nil {
		if errors.Is(err, runtime.ErrHostBusy) {
			return domain.SubmitReply{}, ErrSubmitBusy
		}
		return domain.SubmitReply{}, ErrSubmitUnavailable
	}
	claimedAt := r.clock.Now()
	if claimedAt < 0 {
		reservation.Release()
		return domain.SubmitReply{}, ErrSubmitUnavailable
	}
	claim, err := r.claim(ctx, auth, input, fingerprint, ledger.ClaimPolicy{
		RuntimeID: r.cfg.RuntimeID, Now: claimedAt, LeaseMillis: r.cfg.LeaseMillis,
		MaxRuntimeAttempts: r.cfg.MaxRuntimeAttempts,
	})
	if err != nil {
		reservation.Release()
		switch {
		case errors.Is(err, ledger.ErrClaimInvalid):
			return domain.SubmitReply{}, ErrSubmitInvalid
		case errors.Is(err, ledger.ErrClaimForbidden):
			return domain.SubmitReply{}, ErrSubmitForbidden
		case errors.Is(err, ledger.ErrClaimNotReady):
			return domain.SubmitReply{}, ErrSubmitNotReady
		default:
			return domain.SubmitReply{}, ErrSubmitUnavailable
		}
	}
	switch claim.Kind {
	case "conflict":
		reservation.Release()
		return domain.SubmitReply{Kind: "rejected", OperationID: input.OperationID, Error: &domain.ErrorDTO{Code: "OperationIdConflict", MessageKey: "ant.error.operationIdConflict", OperationID: input.OperationID, Action: "use_new_id"}}, nil
	case "inProgress":
		reservation.Release()
		if claim.Operation == nil {
			return domain.SubmitReply{}, ErrOperationQueryIntegrity
		}
		return domain.SubmitReply{Kind: "inProgress", OperationID: input.OperationID, State: domain.Running, Operation: claim.Operation}, nil
	case "replay":
		reservation.Release()
		if claim.Result == nil {
			return domain.SubmitReply{}, ErrOperationQueryIntegrity
		}
		return domain.SubmitReply{Kind: "replay", OperationID: input.OperationID, State: claim.Result.State, Result: claim.Result}, nil
	case "new":
		if claim.Owner == nil || claim.Operation == nil || claim.Owner.Key.Namespace != auth.Namespace || claim.Owner.Key.OperationID != input.OperationID || claim.Owner.RuntimeID != r.cfg.RuntimeID {
			reservation.Release()
			return domain.SubmitReply{}, ErrOperationQueryIntegrity
		}
		input = cloneExecuteInput(input)
		if err := reservation.Start(auth, *claim.Owner, input.DeadlineAt, func(taskCtx context.Context) {
			r.executor.Execute(taskCtx, auth, *claim.Owner, input)
		}); err != nil {
			reservation.Release()
			return domain.SubmitReply{}, ErrSubmitHostTakeover
		}
		return domain.SubmitReply{
			Kind: "accepted", State: domain.Running, OperationID: input.OperationID,
			OperationURL: "/v1/operations/" + string(input.OperationID),
			EventsURL:    "/v1/operations/" + string(input.OperationID) + "/events",
		}, nil
	default:
		reservation.Release()
		return domain.SubmitReply{}, ErrSubmitUnavailable
	}
}

func (r *Router) resolveExisting(ctx context.Context, auth domain.AuthContext, input domain.ExecuteInput, fingerprint string) (domain.SubmitReply, bool, error) {
	var reply domain.SubmitReply
	found := false
	err := r.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.OperationQueryReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		record, exists, err := reader.GetOperation(auth.Namespace, input.OperationID)
		if err != nil || !exists {
			return err
		}
		found = true
		if record.Fingerprint != fingerprint || record.PoolID != input.PoolID {
			reply = domain.SubmitReply{Kind: "rejected", OperationID: input.OperationID, Error: &domain.ErrorDTO{Code: "OperationIdConflict", MessageKey: "ant.error.operationIdConflict", OperationID: input.OperationID, Action: "use_new_id"}}
			return nil
		}
		view, err := projectOperation(reader, record)
		if err != nil {
			return err
		}
		if record.State == domain.Running {
			reply = domain.SubmitReply{Kind: "inProgress", State: domain.Running, OperationID: input.OperationID, Operation: &view}
			return nil
		}
		if view.Result == nil {
			return ErrOperationQueryIntegrity
		}
		reply = domain.SubmitReply{Kind: "replay", State: view.Result.State, OperationID: input.OperationID, Result: view.Result}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrOperationQueryStore) || errors.Is(err, ErrOperationQueryIntegrity) {
			return domain.SubmitReply{}, false, err
		}
		return domain.SubmitReply{}, false, ErrSubmitUnavailable
	}
	return reply, found, nil
}

func cloneExecuteInput(in domain.ExecuteInput) domain.ExecuteInput {
	in.Request.Payload = append([]byte(nil), in.Request.Payload...)
	if in.Affinity != nil {
		a := *in.Affinity
		in.Affinity = &a
	}
	return in
}
