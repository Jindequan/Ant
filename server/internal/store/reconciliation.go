package store

import "ant/server/internal/domain"

// ReconciliationWriterTx contains the narrow append-only ledger operations
// used to correct a terminal unknown attempt after provider-side end evidence
// arrives. Operation results stay immutable.
type ReconciliationWriterTx interface {
	RecoveryWriterTx
	OperationEventWriterTx
	GetLatestReconciliationCorrections(domain.ID) ([]domain.ResourceOutcome, bool, error)
	ApplyReconciliationDelta(domain.ResourceDelta, domain.ID, domain.ID, domain.ID, []domain.ID, domain.Millis) (bool, error)
	ReleaseReconciledOccupancy(domain.ID, domain.ID, domain.Units, []domain.ID, domain.Millis) (bool, error)
	ReleaseReconciledAttribution(domain.ID, domain.ID, []domain.ID, domain.Millis) (bool, error)
	ResolveReconciliationHolds(domain.ID, domain.ID, []domain.ID, domain.Millis) error
	InsertReconciliationHold(domain.ID, domain.ID, domain.ID, string, []domain.ID, domain.Millis) error
	InsertReconciliation(domain.ID, domain.ID, domain.ID, string, []byte, []byte, domain.Millis) error
}
