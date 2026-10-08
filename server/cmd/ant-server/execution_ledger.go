package main

import (
	"context"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
)

// executionLedger adapts the time-stamped ledger command services to the
// executor's narrow command interface. Every mutation still lands in the
// durable command service; this type owns no state or policy.
type executionLedger struct {
	preparation *commands.PreparationService
	output      *commands.OutputService
	settlement  *commands.SettlementService
	clock       commands.PreparationClock
}

func (l executionLedger) Quote(ctx context.Context, auth domain.AuthContext, plan domain.ExecutionPlan) ([]domain.ResourceSnapshot, error) {
	return l.preparation.Quote(ctx, auth, plan, l.clock.Now())
}

func (l executionLedger) TryPrepare(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, plan domain.ExecutionPlan, guards []domain.GuardEvidence, digest string, decisionTrace []byte) (ledger.PrepareReply, error) {
	return l.preparation.TryPrepare(ctx, auth, owner, attemptID, plan, guards, digest, decisionTrace, l.clock.Now())
}

func (l executionLedger) AuthorizeDispatch(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID) (ledger.DispatchPermit, error) {
	return l.preparation.AuthorizeDispatch(ctx, auth, owner, attemptID, l.clock.Now())
}

func (l executionLedger) AbortBeforeDispatch(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, proof ledger.UnsentProof, permit ledger.DispatchPermit) (ledger.AbortOutcome, error) {
	return l.preparation.AbortBeforeDispatch(ctx, auth, commands.AbortRequest{Owner: owner, Attempt: attemptID, Proof: proof, Permit: permit})
}

func (l executionLedger) AppendOutput(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, chunk domain.OutputChunk) error {
	return l.output.AppendOutput(ctx, auth, owner, attemptID, chunk)
}

func (l executionLedger) Settle(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, report domain.ExecutionReport) (ledger.SettleReply, error) {
	return l.settlement.Settle(ctx, auth, owner, report)
}

func (l executionLedger) CompleteWithoutAttempt(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, reasons []domain.Reason) (domain.OperationResult, error) {
	return l.preparation.CompleteWithoutAttempt(ctx, auth, owner, reasons)
}
