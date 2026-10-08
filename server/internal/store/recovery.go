package store

import "ant/server/internal/domain"

// Recovery capability (LED-04): cancel intent, lease renewal, due-operation
// takeover and the AbortBeforeDispatch K3 transaction. This port is a narrow
// additive extension of the frozen T1/T2 transaction surfaces; every method
// executes inside the same SubmitWrite transaction as its caller. SQL lives
// only in store/sqlite (recovery_statements.go); no schema change, no
// generic SQL entry point, and the frozen WriterTx/ReaderTx/Store signatures
// are untouched.

// RecoveryAttemptOutcome is the durable per-attempt fact the recovery
// resolver needs: whether each attempt of an operation is confirmed
// not-executed, externally ended, or still unconfirmed.
type RecoveryAttemptOutcome struct {
	AttemptID       domain.ID
	Phase           string // prepared / executing / terminal
	ExternalOutcome string // terminal external_outcome; empty while non-terminal
	ReportEnded     bool   // terminal with a persisted report whose ExternalEnded is true
}

type ControlSignalRecord struct {
	Namespace       domain.ID
	OperationID     domain.ID
	CancelRequested bool
	ControlVersion  int64
}

// ControlSignalReaderTx exposes only running operations owned by one runtime
// that have durable cancellation intent. Runtime scoping keeps the result
// bounded by that process's active-task limit.
type ControlSignalReaderTx interface {
	ReaderTx
	ListRuntimeControlSignals(namespace, runtimeID domain.ID, limit int) ([]ControlSignalRecord, error)
}

// RecoveryWriterTx is implemented by stores supporting LED-04. Guarded
// UPDATEs return applied=false when the CAS precondition no longer holds;
// callers must roll the whole command back (no partial effects, K3).
type RecoveryWriterTx interface {
	WriterTx
	SettlementWriterTx
	// GetActiveRecoveryAttempt returns the single prepared/executing attempt
	// of an operation with its phase (attempts_one_active guarantees at most
	// one). Only a prepared attempt may be closed inline by cancel.
	GetActiveRecoveryAttempt(key domain.OperationKey) (attemptID domain.ID, phase string, found bool, err error)
	// ListRecoveryAttemptOutcomes reads every attempt's terminal fact.
	ListRecoveryAttemptOutcomes(domain.OperationKey) ([]RecoveryAttemptOutcome, error)
	// MarkRecoveryCancelRequested records the cancel instant once
	// (COALESCE keeps the first timestamp) under an optimistic version CAS.
	MarkRecoveryCancelRequested(key domain.OperationKey, expectedVersion int64, at domain.Millis) (bool, error)
	// RenewRecoveryLease conditionally extends lease_until for the still
	// running, still owned operation; it competes with sweep's takeover via
	// the same row (03-EXECUTION §7).
	RenewRecoveryLease(key domain.OperationKey, runtimeID, ownerID domain.ID, generation int64, leaseUntil, now domain.Millis) (bool, error)
	// ExtendRecoveryAttemptLease keeps active attempts' advisory lease
	// coherent with the renewed operation lease (informational column).
	ExtendRecoveryAttemptLease(key domain.OperationKey, generation int64, leaseUntil domain.Millis) error
	// ListDueRecoveryOperations returns running operations whose lease has
	// expired, in due order, at most limit rows.
	ListDueRecoveryOperations(now domain.Millis, limit int) ([]ClaimOperationRecord, error)
	// FinishRecoveryAttempt terminalizes an attempt under the same
	// (phase, generation) guard the abort matrix competes on.
	FinishRecoveryAttempt(attemptID domain.ID, fromPhase, externalOutcome string, now domain.Millis) (bool, error)
	// RevokeRecoveryPermit is the granted→revoked CAS for unconsumed
	// permits (K3 revoke path; consumed permits keep their fact).
	RevokeRecoveryPermit(permitID domain.ID, now domain.Millis) (bool, error)
	// ApplyRecoveryDelta applies one resource delta with an explicit event
	// kind: recovery releases are "release"/"encumber", not "consume".
	ApplyRecoveryDelta(delta domain.ResourceDelta, attempt, scope domain.ID, eventKind string, refs []domain.ID, now domain.Millis) (bool, error)
	// CompleteRecoveryOperation terminalizes a running operation with its
	// resolver-produced result; bumpGeneration marks takeover (sweep) so the
	// previous owner permanently loses write/send standing.
	CompleteRecoveryOperation(key domain.OperationKey, expectedVersion int64, result *domain.OperationResult, bumpGeneration bool, leaseUntil, now domain.Millis) (bool, error)
	// BumpRecoveryGeneration takes over a still-running operation without a
	// terminal result (nothing pending; the resolver keeps it running until
	// its deadline). leaseUntil parks it out of the due set until then.
	BumpRecoveryGeneration(key domain.OperationKey, expectedVersion int64, leaseUntil, now domain.Millis) (bool, error)
	// AppendRecoveryEvent appends one operation_events row (append-only).
	AppendRecoveryEvent(key domain.OperationKey, eventType string, payload []byte, now domain.Millis) error
}

// OperationEventWriterTx appends a typed lifecycle event in the same writer
// transaction as its state transition. Event payloads contain references and
// facts, never output bodies or credentials.
type OperationEventWriterTx interface {
	AppendOperationEvent(key domain.OperationKey, eventType string, payload []byte, now domain.Millis) error
}
