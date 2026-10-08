package store

import "ant/server/internal/domain"

// ManagedResourceRecord is the current resource state that a namespace
// manager may inspect or change. The definition comes from the active config
// projection; adjustmentVersion is the authoritative observation watermark.
type ManagedResourceRecord struct {
	Definition        domain.ResourceDefinition
	Snapshot          domain.ResourceSnapshot
	AdjustmentVersion int64
}

type ManagedHoldRecord struct {
	HoldID       domain.ID
	ScopeKind    string
	ScopeID      domain.ID
	Reason       string
	EvidenceRefs []domain.ID
	CanClear     bool
}

type AdjustableChargeRecord struct {
	EventID             domain.ID
	ResourceID          domain.ID
	WindowKey           domain.ID
	MetricID            domain.ID
	Unit                string
	RemainingReversible domain.Units
}

// AdminResourceReaderTx limits management actions to resources reachable
// from a pool in the caller's namespace.
type AdminResourceReaderTx interface {
	ReaderTx
	GetManagedResourceDefinition(domain.ID, domain.ID) (domain.ResourceDefinition, bool, error)
	GetManagedResource(domain.ID, domain.ID, domain.ID) (ManagedResourceRecord, bool, error)
	GetManagedHold(domain.ID, domain.ID) (ManagedHoldRecord, bool, error)
	ListManagedHoldsForResource(domain.ID, domain.ID) ([]ManagedHoldRecord, error)
	ListAdjustableChargesForOperation(domain.ID, domain.ID) ([]AdjustableChargeRecord, error)
}

// AdminWriterTx adds the atomic resource observation, adjustment and hold
// clearing writes used by manager commands.
type AdminWriterTx interface {
	WriterTx
	GetManagedResourceDefinition(domain.ID, domain.ID) (domain.ResourceDefinition, bool, error)
	GetManagedResource(domain.ID, domain.ID, domain.ID) (ManagedResourceRecord, bool, error)
	GetManagedHold(domain.ID, domain.ID) (ManagedHoldRecord, bool, error)
	InsertResourceObservation(domain.ObservationEvidence, domain.ID, domain.ID, []domain.ID, domain.Millis) error
	ApplyAdminAdjustment(domain.ResourceDelta, domain.ID, domain.ID, domain.ID, []domain.ID, domain.Millis) (bool, error)
	GetOriginalCharge(domain.ID, domain.ID, domain.ID) (domain.Units, bool, error)
	InsertAdminOverrunHold(domain.ID, domain.ID, domain.ID, []domain.ID, domain.Millis) error
	ClearManagedHold(domain.ID, domain.ID, []domain.ID, domain.Millis) (bool, error)
}
