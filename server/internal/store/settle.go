package store

import "ant/server/internal/domain"

// SettlementResource is the pinned reservation's authoritative state. Reads
// deliberately bypass current admission controls: dispatched facts still settle.
type SettlementResource struct {
	Definition domain.ResourceDefinition
	Snapshot   domain.ResourceSnapshot
}
type SettlementFacts struct {
	ReportHash    string
	ReportJSON    []byte
	Resources     []SettlementResource
	Attempts      []domain.ID
	Outcomes      []domain.ResourceOutcome
	OutputStored  bool
	AllPriorEnded bool
	Blocked       bool
}
type SettlementWrite struct {
	AttemptID  domain.ID
	Report     domain.ExecutionReport
	ReportHash string
	ReportJSON []byte
	ReplyJSON  []byte
	Result     *domain.OperationResult
	Outcomes   []domain.ResourceOutcome
	Now        domain.Millis
}

// SettlementWriterTx is additive to the frozen T1 transaction interfaces.
// Implementations perform every method within the same SubmitWrite transaction.
type SettlementWriterTx interface {
	PreparationWriterTx
	GetSettlementFacts(PreparationAttempt, domain.Millis) (SettlementFacts, error)
	ApplySettlementDelta(domain.ResourceDelta, domain.ID, domain.ID, *domain.Millis, []domain.ID, domain.Millis) (bool, error)
	SettleReservation(domain.ID, string, string) (bool, error)
	InsertSettlementIsolation(domain.ID, domain.ID, domain.ID, string, domain.Units, []domain.ID, domain.Millis) error
	InsertSettlementHold(domain.ID, domain.ID, string, []domain.ID, domain.Millis) error
	PersistSettlement(ClaimOperationRecord, PreparationAttempt, SettlementWrite) (bool, error)
	AppendSettlementEvidence(domain.OperationKey, domain.ExecutionReport, string, domain.ID, domain.Millis) error
}
