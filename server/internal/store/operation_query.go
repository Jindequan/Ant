package store

import "ant/server/internal/domain"

// OperationQueryReaderTx is an additive read model for operation history.
// Queries remain namespace-scoped and run in the Store's consistent read
// snapshot; raw strategy/fingerprint data is never part of the projection.
type OperationQueryReaderTx interface {
	GetOperation(namespace, operationID domain.ID) (ClaimOperationRecord, bool, error)
	ListOperations(namespace domain.ID, state string, beforeUpdatedAt domain.Millis, beforeOperationID domain.ID, limit int) ([]ClaimOperationRecord, error)
	ListOperationAttempts(namespace, operationID domain.ID) ([]OperationAttemptRecord, error)
	ListOperationReconciliations(namespace, operationID domain.ID) ([]OperationReconciliationRecord, error)
	ListOperationEvents(namespace, operationID domain.ID, afterSequence int64, limit int) ([]OperationEventRecord, error)
	GetOperationEventWatermark(namespace, operationID domain.ID) (int64, error)
	ListOperationDecisions(namespace, operationID domain.ID, limit int) ([]OperationDecisionRecord, error)
}

type OperationEventRecord struct {
	Sequence    int64
	EventType   string
	PayloadJSON []byte
	CreatedAt   domain.Millis
}

type OperationDecisionRecord struct {
	DecisionID     domain.ID
	AttemptID      domain.ID
	Stage          string
	SnapshotJSON   []byte
	TraceJSON      []byte
	TraceTruncated bool
	CreatedAt      domain.Millis
}

type OperationReconciliationRecord struct {
	ReconciliationID domain.ID
	AttemptID        domain.ID
	EvidenceJSON     []byte
	CorrectionJSON   []byte
	CreatedAt        domain.Millis
}

type OperationAttemptRecord struct {
	AttemptID         domain.ID
	OfferID           domain.ID
	BindingID         domain.ID
	AccountID         domain.ID
	Phase             string
	Outcome           string
	ProviderRequestID domain.ID
}
