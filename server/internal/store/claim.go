package store

import "ant/server/internal/domain"

// ClaimOperationRecord is the persisted identity and ownership snapshot for
// the first transactional step of an operation. Later ledger commands extend
// this row under owner/generation guards without reinterpreting strategy.
type ClaimOperationRecord struct {
	Namespace         domain.ID
	OperationID       domain.ID
	PoolID            domain.ID
	Fingerprint       string
	State             domain.OperationState
	RuntimeID         domain.ID
	OwnerID           domain.ID
	Generation        int64
	LeaseUntil        domain.Millis
	DeadlineAt        domain.Millis
	MaxAttempts       int
	PermitsIssued     int
	StrategyJSON      []byte
	CancelRequestedAt *domain.Millis
	VisibleOutputAt   *domain.Millis
	ResultJSON        []byte
	CreatedAt         domain.Millis
	UpdatedAt         domain.Millis
	Version           int64
}

// ClaimWriterTx is a narrow additive extension implemented by stores that
// support the T2.2 claim transaction. It deliberately remains separate from
// the T1-frozen WriterTx interface; claim callbacks type-assert this capability
// inside SubmitWrite, preserving the existing Store contract for T1 callers.
type ClaimWriterTx interface {
	GetClaimOperation(namespace, operationID domain.ID) (ClaimOperationRecord, bool, error)
	InsertClaimOperation(record ClaimOperationRecord) error
}
