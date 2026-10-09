package sqlite

// The single-host profile numbers are a product decision (02-STORAGE §3:
// busy_timeout 10ms, command budget 50ms, at most 2 full retries). They are
// pinned here structurally, not by timing real commands: test fixtures whose
// subject is not the budget run with an explicit anti-hang budget instead
// (see Config.CommandBudget), so this test is what keeps the product default
// from drifting.

import (
	"testing"
	"time"

	"ant/server/internal/store"
)

func TestProfileDefaultsMatchStorageDesign(t *testing.T) {
	cfg, err := validateConfig(Config{Clock: internalTestClock{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BusyTimeout != 10*time.Millisecond || cfg.CommandBudget != 50*time.Millisecond || cfg.MaxRetries != 2 {
		t.Fatalf("profile defaults = busy %v, budget %v, retries %d; 02-STORAGE §3 says 10ms, 50ms, 2",
			cfg.BusyTimeout, cfg.CommandBudget, cfg.MaxRetries)
	}
	s := &Store{cfg: cfg}
	if got := s.commandBudget(store.WriteCommand{}); got != 50*time.Millisecond {
		t.Fatalf("command without its own budget runs under %v, want the 50ms default", got)
	}
	if got := s.commandBudget(store.WriteCommand{Budget: time.Second}); got != time.Second {
		t.Fatalf("explicit command budget resolved to %v, want 1s", got)
	}

	explicit, err := validateConfig(Config{Clock: internalTestClock{}, CommandBudget: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.CommandBudget != 10*time.Second {
		t.Fatalf("explicit CommandBudget became %v", explicit.CommandBudget)
	}
}
