package store

import "ant/server/internal/domain"

// Preparation records are narrow local transaction projections, not API DTOs.
// Their definitions come from activated configuration rows, never requests.
type PreparationResource struct {
	Definition         domain.ResourceDefinition
	States             []domain.ResourceSnapshot
	AdjustmentVersions map[domain.ID]int64
	Observations       []domain.ObservationEvidence
	Charges            []PreparationCharge
	HoldIDs            []domain.ID
	EncumbranceIDs     []domain.ID
	Isolated           bool
}
type PreparationCharge struct {
	Units      domain.Units
	ChargedAt  domain.Millis
	Attributed bool
}
type PreparationBundle struct {
	Pool                              PoolRecord
	Member                            PoolMemberRecord
	Offer                             OfferRecord
	ProviderTypeID                    domain.ID
	Endpoint                          string
	AccountID                         domain.ID
	AccountEnabled, BindingEnabled    bool
	CredentialRef, CredentialRevision domain.ID
	AdapterRef                        domain.ID
	ActiveRevision                    domain.ID
	ControlVersion                    int64
	RestoreGuard                      int
	AllowMetered                      bool // current account/binding policy, fail closed if absent
	Held                              bool
	HoldIDs                           []domain.ID
	Resources                         []PreparationResource
	Guards                            []domain.GuardEvidence
}
type PreparationAttempt struct {
	AttemptID     domain.ID
	Key           domain.OperationKey
	Generation    int64
	Phase         string
	Plan          domain.ExecutionPlan
	RequestDigest string
	Guards        []domain.GuardEvidence
	Allocations   []domain.ReservationState
	LeaseUntil    domain.Millis
}
type PreparationPermit struct {
	PermitID, AttemptID, RuntimeID domain.ID
	Generation, ControlVersion     int64
	CredentialRevision             domain.ID
	ExpiresAt                      domain.Millis
	RequestDigest, Status          string
	GrantedAt                      domain.Millis
}

// PreparationReaderTx provides a consistent quote without writing reservations.
type PreparationReaderTx interface {
	GetPreparationBundle(domain.ExecutionPlan, domain.Millis) (PreparationBundle, error)
}

// PreparationWriterTx adds only the rows needed for prepare/authorize/consume.
// All methods are invoked within the existing SubmitWrite transaction.
type PreparationWriterTx interface {
	ClaimWriterTx
	PreparationReaderTx
	GetPreparationAttempt(domain.ID) (PreparationAttempt, bool, error)
	HasActivePreparationAttempt(domain.OperationKey) (bool, error)
	InsertPreparationAttempt(PreparationAttempt, string, domain.Millis) error
	ApplyPreparationDelta(domain.ResourceDelta, domain.ID, domain.ID, string, domain.Millis) (bool, error)
	MovePreparationReservation(domain.ID, domain.ID, domain.ID, string) (bool, error)
	AuthorizePreparationAttempt(domain.ID, int64, PreparationPermit, domain.Millis) (bool, error)
	GetPreparationPermit(domain.ID) (PreparationPermit, bool, error)
	ConsumePreparationPermit(domain.ID, domain.Millis) (bool, error)
	InsertPreparationDecision(domain.OperationKey, domain.ID, string, []byte, []byte, domain.Millis) error
}
