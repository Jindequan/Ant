package commands

import (
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
)

func TestReleaseTerminalAttemptDropsHandleAndRegistryRecord(t *testing.T) {
	registry := ledger.NewPermitRegistry()
	handle, err := registry.Issue("attempt", "runtime", "digest", "credential")
	if err != nil {
		t.Fatal(err)
	}
	prep := &PreparationService{
		registry: registry,
		handles:  map[domain.ID]ledger.DispatchPermit{"attempt": handle},
	}
	prep.releaseTerminalAttempt("attempt")
	if _, exists := prep.handles["attempt"]; exists {
		t.Fatal("cached permit handle was retained")
	}
	if err := registry.Validate(handle, "runtime", "digest", "credential"); err != ledger.ErrPermitUnknown {
		t.Fatalf("terminal handle must be removed from registry, got %v", err)
	}
}
