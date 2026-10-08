// Package sqlite implements the store port (internal/store) over
// modernc.org/sqlite for the single-host profile: one write connection driven
// by a dedicated writer goroutine behind two bounded queues (control-reserved
// and normal), plus an independent query-only read pool. Every durable change
// goes through SubmitWrite; no other package runs SQL (02-STORAGE §3).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the database/sql driver as "sqlite"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// Clock is the local time-source port. It is deliberately not the runtime
// package type: *runtime.Clock satisfies it via Now() domain.Millis, so the
// composition root passes its clock unchanged while this package stays free
// of an internal dependency cycle risk.
type Clock interface {
	Now() domain.Millis
}

// Config tunes the store. Zero fields fall back to the single-host profile
// defaults (01-ARCHITECTURE §3, 02-STORAGE §3); every explicitly set field
// must satisfy its documented range -- Open validates the full matrix and
// refuses invalid combinations instead of coercing or panicking on them
// (R08). Note the interplay: an explicitly small WriterQueueLimit keeps the
// default ControlReserved invalid, so both must be set together.
type Config struct {
	// Clock supplies timestamps for receipts, migration version rows and
	// similar bookkeeping. Required: a nil Clock is a config error (the
	// composition root owns the time source; there is no implicit fallback).
	Clock Clock
	// WriterQueueLimit is the total bound across both writer lanes.
	// 0 → default (512); explicit values must be >= 8.
	WriterQueueLimit int
	// ControlReserved is the capacity of the control/settlement lane carved
	// out of WriterQueueLimit (128 of 512 by default). 0 → default (128);
	// explicit values must satisfy 0 < ControlReserved < WriterQueueLimit.
	ControlReserved int
	// ReadConnections is the size of the read-only connection pool.
	// 0 → default (4); explicit values must be in [1, 64].
	ReadConnections int
	// BusyTimeout is the per-connection SQLite busy_timeout.
	// 0 → default (10ms); explicit values must be >= 0.
	BusyTimeout time.Duration
	// CommandBudget is the default whole-command budget for WriteCommands
	// that do not set their own Budget. 0 → default (50ms); explicit values
	// must be > 0.
	CommandBudget time.Duration
	// MaxRetries is how many times a command is retried in full (after a
	// complete rollback) when SQLite reports busy/locked. 0 → default (2);
	// explicit values must be in [1, 5] (a zero value cannot express "no
	// retries" because it selects the default).
	MaxRetries int
}

const (
	defaultWriterQueueLimit = 512
	defaultControlReserved  = 128
	defaultReadConnections  = 4
	defaultBusyTimeout      = 10 * time.Millisecond
	defaultCommandBudget    = 50 * time.Millisecond
	defaultMaxRetries       = 2

	minWriterQueueLimit = 8
	maxReadConnections  = 64
	maxRetries          = 5

	// openTimeout bounds connection setup and the initial ping in Open.
	openTimeout = 5 * time.Second
)

// ErrInvalidConfig wraps every Open validation failure; errors.Join lists
// each violation with its Config field name.
var ErrInvalidConfig = errors.New("sqlite: invalid config")

// validateConfig applies the profile defaults to zero fields, then validates
// every explicit value and the cross-constraints. It never coerces an invalid
// explicit value (silent coercion was the R08 defect: ControlReserved was
// reset behind the caller's back and normal lane capacity went negative) and
// it never panics: Open must return an error for any caller-supplied Config.
func validateConfig(c Config) (Config, error) {
	var errs []error
	violation := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if c.Clock == nil {
		violation("Clock: nil; the composition root must supply a Clock")
	}
	switch {
	case c.WriterQueueLimit == 0:
		c.WriterQueueLimit = defaultWriterQueueLimit
	case c.WriterQueueLimit < minWriterQueueLimit:
		violation("WriterQueueLimit: %d must be >= %d", c.WriterQueueLimit, minWriterQueueLimit)
	}
	if c.ControlReserved == 0 {
		c.ControlReserved = defaultControlReserved
	}
	if c.ControlReserved <= 0 || c.ControlReserved >= c.WriterQueueLimit {
		violation("ControlReserved: %d must satisfy 0 < ControlReserved < WriterQueueLimit (%d)",
			c.ControlReserved, c.WriterQueueLimit)
	}
	switch {
	case c.ReadConnections == 0:
		c.ReadConnections = defaultReadConnections
	case c.ReadConnections < 1 || c.ReadConnections > maxReadConnections:
		violation("ReadConnections: %d must be in [1, %d]", c.ReadConnections, maxReadConnections)
	}
	if c.BusyTimeout == 0 {
		c.BusyTimeout = defaultBusyTimeout
	} else if c.BusyTimeout < 0 {
		violation("BusyTimeout: %s must be >= 0", c.BusyTimeout)
	}
	if c.CommandBudget == 0 {
		c.CommandBudget = defaultCommandBudget
	} else if c.CommandBudget < 0 {
		violation("CommandBudget: %s must be > 0", c.CommandBudget)
	}
	switch {
	case c.MaxRetries == 0:
		c.MaxRetries = defaultMaxRetries
	case c.MaxRetries < 0 || c.MaxRetries > maxRetries:
		violation("MaxRetries: %d must be in [0, %d]", c.MaxRetries, maxRetries)
	}
	return c, errors.Join(errs...)
}

// Store is the sqlite implementation of store.Store. The writer goroutine and
// queue plumbing live in writer.go, SQL statements in statements.go, schema
// bootstrap in migrate.go.
type Store struct {
	cfg     Config
	writeDB *sql.DB // SetMaxOpenConns(1): exactly one writer connection
	readDB  *sql.DB // query-only pool

	controlCh chan *queueCommand
	normalCh  chan *queueCommand

	mu                sync.RWMutex
	submissionsClosed bool
	loopDone          chan struct{} // closed when the writer loop has drained and exited
}

// Open builds the write and read connection pools and starts the writer
// goroutine. dsn is a filesystem path (or a full "file:...?query" DSN, to
// which the profile pragmas are appended). Schema creation is a separate
// step: call Migrate before the first SubmitWrite.
func Open(dsn string, cfg Config) (store.Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("sqlite: empty dsn")
	}
	// Validate before anything is opened: an invalid config fails with
	// nothing to leak, and no caller-supplied combination can reach the
	// queue construction with a negative lane capacity (R08).
	cfg, err := validateConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	writeDB, err := sql.Open("sqlite", buildDSN(dsn, writeParams(cfg)))
	if err != nil {
		return nil, fmt.Errorf("%w: open write pool: %w", store.ErrUnavailable, err)
	}
	// Single-writer profile: all durable changes serialize through one
	// connection owned by the writer goroutine (01-ARCHITECTURE §3).
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	readDB, err := sql.Open("sqlite", buildDSN(dsn, readParams(cfg)))
	if err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("%w: open read pool: %w", store.ErrUnavailable, err)
	}
	readDB.SetMaxOpenConns(cfg.ReadConnections)
	readDB.SetMaxIdleConns(cfg.ReadConnections)

	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	if err := pingPools(ctx, writeDB, readDB); err != nil {
		_ = writeDB.Close()
		_ = readDB.Close()
		return nil, err
	}

	s := &Store{
		cfg:       cfg,
		writeDB:   writeDB,
		readDB:    readDB,
		controlCh: make(chan *queueCommand, cfg.ControlReserved),
		normalCh:  make(chan *queueCommand, cfg.WriterQueueLimit-cfg.ControlReserved),
		loopDone:  make(chan struct{}),
	}
	go s.writerLoop()
	return s, nil
}

func pingPools(ctx context.Context, writeDB, readDB *sql.DB) error {
	if err := writeDB.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: ping write pool: %w", store.ErrUnavailable, err)
	}
	if err := readDB.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: ping read pool: %w", store.ErrUnavailable, err)
	}
	return nil
}

// buildDSN turns a plain path into a modernc.org/sqlite DSN with the given
// connection parameters. Parameter syntax follows the driver's documented
// form (Driver.Open / NewConnector docs, modernc.org/sqlite v1.60.1):
//
//	file:app.db?_pragma=journal_mode(WAL)&_txlock=immediate
//
// Shorthands used: "_busy_timeout" (validated as an integer) and "_txlock"
// (deferred|immediate|exclusive); everything else goes through verbatim
// "_pragma" entries. If the caller already passes a DSN with a query string,
// the profile parameters are appended -- the driver applies them per opened
// connection, so re-adding them is idempotent. The DSN is generated here from
// fixed values only; no user-supplied pragma ever reaches it (02-STORAGE §3).
func buildDSN(dsn string, params [][2]string) string {
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	var b strings.Builder
	b.WriteString(dsn)
	b.WriteString(sep)
	for i, kv := range params {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(kv[0]))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(kv[1]))
	}
	return b.String()
}

func writeParams(cfg Config) [][2]string {
	return [][2]string{
		{"_busy_timeout", fmt.Sprint(cfg.BusyTimeout.Milliseconds())},
		{"_pragma", "journal_mode(WAL)"},
		{"_pragma", "synchronous(FULL)"},
		{"_pragma", "foreign_keys(1)"},
		{"_txlock", "immediate"}, // every sql.Tx on this pool opens BEGIN IMMEDIATE
	}
}

func readParams(cfg Config) [][2]string {
	return [][2]string{
		{"_busy_timeout", fmt.Sprint(cfg.BusyTimeout.Milliseconds())},
		{"_pragma", "query_only(1)"}, // read pool can never write (02-STORAGE §3)
	}
}
