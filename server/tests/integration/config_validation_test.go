package integration

// R08 acceptance: defaults first, then validation of every explicit value and
// cross-constraint; every violation is reported with its field name; no
// caller-supplied Config combination panics; failure paths return no live
// store and leave the process healthy.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ant/server/internal/runtime"
	"ant/server/internal/store/sqlite"
)

func TestOpenRejectsInvalidConfigs(t *testing.T) {
	clock := runtime.NewClock()
	cases := []struct {
		name string
		cfg  sqlite.Config
		want string // substring the returned error must contain
	}{
		{"probe-writer-queue-1", sqlite.Config{Clock: clock, WriterQueueLimit: 1}, "WriterQueueLimit"},
		{"writer-queue-7", sqlite.Config{Clock: clock, WriterQueueLimit: 7}, "WriterQueueLimit"},
		{"negative-writer-queue", sqlite.Config{Clock: clock, WriterQueueLimit: -5}, "WriterQueueLimit"},
		{"control-reserved-equals-limit", sqlite.Config{Clock: clock, WriterQueueLimit: 8, ControlReserved: 8}, "ControlReserved"},
		{"control-reserved-above-limit", sqlite.Config{Clock: clock, WriterQueueLimit: 16, ControlReserved: 17}, "ControlReserved"},
		{"negative-control-reserved", sqlite.Config{Clock: clock, ControlReserved: -1}, "ControlReserved"},
		// An explicitly small queue keeps the default ControlReserved (128)
		// and is therefore refused with both fields named -- the R08 defect
		// was exactly this combination being silently coerced.
		{"small-queue-vs-default-reserved", sqlite.Config{Clock: clock, WriterQueueLimit: 8}, "ControlReserved"},
		{"read-connections-65", sqlite.Config{Clock: clock, ReadConnections: 65}, "ReadConnections"},
		{"negative-read-connections", sqlite.Config{Clock: clock, ReadConnections: -2}, "ReadConnections"},
		{"negative-busy-timeout", sqlite.Config{Clock: clock, BusyTimeout: -time.Millisecond}, "BusyTimeout"},
		{"negative-command-budget", sqlite.Config{Clock: clock, CommandBudget: -time.Millisecond}, "CommandBudget"},
		{"max-retries-6", sqlite.Config{Clock: clock, MaxRetries: 6}, "MaxRetries"},
		{"negative-max-retries", sqlite.Config{Clock: clock, MaxRetries: -1}, "MaxRetries"},
		{"nil-clock", sqlite.Config{}, "Clock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("Open panicked instead of returning a validation error: %v", p)
				}
			}()
			st, err := sqlite.Open(filepath.Join(t.TempDir(), "cfg.db"), tc.cfg)
			if st != nil {
				_ = st.Close(context.Background())
				t.Fatal("Open returned a live store for an invalid config")
			}
			if err == nil {
				t.Fatal("Open accepted an invalid config")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name the offending field %q", err, tc.want)
			}
		})
	}
}

// TestOpenDefaultsAreValid proves the zero-value profile (besides the
// required Clock) opens, migrates and serves one command.
func TestOpenDefaultsAreValid(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, sqlite.Config{Clock: runtime.NewClock()})
	_ = path
	if err := st.SubmitWrite(ctx, noopCmd("defaults-ok")); err != nil {
		t.Fatalf("default-profile store rejected a plain command: %v", err)
	}
}

// TestOpenFailurePathIsClean covers the non-validation failure path: a
// database file that cannot be created. Open reports an error, returns no
// store, and the process stays healthy for a subsequent Open. The validation
// path needs no cleanup test: it runs before any pool exists (validateConfig
// precedes sql.Open in Open), so there is nothing to leak by construction.
func TestOpenFailurePathIsClean(t *testing.T) {
	dir := t.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, "missing", "ant.db"), sqlite.Config{Clock: runtime.NewClock()})
	if st != nil {
		_ = st.Close(context.Background())
		t.Fatal("Open returned a store for an unopenable database path")
	}
	if err == nil {
		t.Fatal("Open accepted an unopenable database path")
	}
	st2, err := sqlite.Open(filepath.Join(dir, "ok.db"), sqlite.Config{Clock: runtime.NewClock()})
	if err != nil {
		t.Fatalf("Open after a failed Open: %v", err)
	}
	_ = st2.Close(context.Background())
}
