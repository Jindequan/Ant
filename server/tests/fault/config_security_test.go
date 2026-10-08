package fault_test

import (
	"context"
	"errors"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
)

func TestAdminMutationsFailClosedBeforeStoreAccess(t *testing.T) {
	clock := &faultClock{}
	service := commands.NewAdminService(nil, clock)
	readOnly := domain.AuthContext{PrincipalID: "reader", Namespace: "ns", Permissions: []domain.Permission{domain.PermRead}}
	if _, err := service.RecordObservation(context.Background(), readOnly, "observe-1", domain.ObservationEvidence{ResourceID: "r", SourceRef: "source", ObservedAt: 1, ExpiresAt: 2}); !errors.Is(err, commands.ErrAdminForbidden) {
		t.Fatalf("read-only observation = %v, want forbidden", err)
	}
	if _, err := service.Adjust(context.Background(), readOnly, domain.AdjustmentInput{CommandID: "adjust-1", ResourceID: "r", WindowKey: "w", Delta: 1, EvidenceRefs: []domain.ID{"evidence"}}); !errors.Is(err, commands.ErrAdminForbidden) {
		t.Fatalf("read-only adjustment = %v, want forbidden", err)
	}
	if _, err := service.ClearHold(context.Background(), readOnly, domain.ClearHoldInput{CommandID: "clear-1", HoldID: "h", EvidenceIDs: []domain.ID{"evidence"}}); !errors.Is(err, commands.ErrAdminForbidden) {
		t.Fatalf("read-only hold clear = %v, want forbidden", err)
	}
}
