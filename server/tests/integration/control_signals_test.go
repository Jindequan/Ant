package integration

import (
	"context"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
)

func TestReadControlSignalsReturnsOnlyDurableCurrentRuntimeCancels(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	auth := claimAuth()
	auth.Permissions = append(auth.Permissions, domain.PermCancel)
	ack, err := f.s.RequestCancel(ctx, auth, f.owner.Key, "cancel-1")
	if err != nil || ack.Status != "recorded" {
		t.Fatalf("RequestCancel() = %+v, %v", ack, err)
	}
	signals, err := f.s.ReadControlSignals(ctx, claimAuth(), f.owner.Key.Namespace, 8)
	if err != nil || len(signals) != 1 {
		t.Fatalf("ReadControlSignals() = %+v, %v", signals, err)
	}
	if signals[0].Key != f.owner.Key || !signals[0].CancelRequested || signals[0].ControlVersion < 1 {
		t.Fatalf("unexpected durable signal: %+v", signals[0])
	}

	otherRuntime := commands.NewPreparationService(f.st, "rt-other", f.clock)
	otherSignals, err := otherRuntime.ReadControlSignals(ctx, claimAuth(), f.owner.Key.Namespace, 8)
	if err != nil || len(otherSignals) != 0 {
		t.Fatalf("other runtime saw signal: %+v, %v", otherSignals, err)
	}
}
