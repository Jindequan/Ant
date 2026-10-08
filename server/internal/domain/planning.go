package domain

import "encoding/json"

// ProviderDescriptor is the server-registered capability catalogue entry; the
// UI renders fields from its schemas but never invents new protocols.
// ProviderDescriptor is the server-registered capability catalogue entry.
// R12 freeze: ExtensionSchema is a RESTRICTED JSON Schema validating every
// providerExtensions object in a ConfigDocument (closed field set, types,
// ranges; secret material unrepresentable by construction — credentials only
// ever appear as credentialRef/revision). EntitlementFacts lists the
// verified commercial bucket identities of this provider; configuration
// references them via ResourceDef.ProviderFactID. Both are real, testable
// schemas shipped with the local/test descriptor — no free-form JSON.
type ProviderDescriptor struct {
	TypeID           ID                `json:"typeId"`
	AdapterRevision  ID                `json:"adapterRevision"`
	SupportedDomains []string          `json:"supportedDomains"`
	ConfigSchema     json.RawMessage   `json:"configSchema"`
	ResourceSchema   json.RawMessage   `json:"resourceSchema"`
	CapabilitySchema json.RawMessage   `json:"capabilitySchema"`
	ExtensionSchema  json.RawMessage   `json:"extensionSchema"`
	EntitlementFacts []EntitlementFact `json:"entitlementFacts"`
	// Q1/C3: registered resource semantics and capability profiles — the
	// catalogue defines which semantics and profile bundles are executable.
	Semantics []RegisteredSemantics `json:"semantics"`
	Profiles  []RegisteredProfile   `json:"profiles"`
}

type RegisteredSemantics struct {
	SemanticsRef      ID   `json:"semanticsRef"`
	AcceptedRevisions []ID `json:"acceptedRevisions"`
	// Trusted execution type comes from this server registration, never from
	// a resource declaration or a profile's accidental coverage of it.
	ResourceKind   string `json:"resourceKind"`   // counter|occupancy
	CommercialRole string `json:"commercialRole"` // limit|entitlement
}

type RequiredDimension struct {
	MetricID ID     `json:"metricId"`
	Role     string `json:"role"` // limit|entitlement|occupancy
}

type RegisteredProfile struct {
	ProfileID          ID                  `json:"profileId"`
	Domain             string              `json:"domain"`
	RequiredDimensions []RequiredDimension `json:"requiredDimensions"`
}

// EntitlementFact is one verified commercial bucket identity at the provider
// (e.g. a specific grant or prepaid balance). factId values are stable,
// provider-verified names — never user-invented or random IDs.
type EntitlementFact struct {
	FactID      ID     `json:"factId"`
	MetricID    ID     `json:"metricId"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
}

// VerifiedResourceFact is the SERVER-verified instance of a capacity fact
// for one billing scope (Q1): unique physical key, real scope, dimensions,
// verified commercial nature, canonical window and the verified capacity
// basis. Configured values are constrained BY this instance — never the
// other way round. Loaded from controlled local/test snapshots in T1; real
// instances come from the verification port out-of-band (T0/T1.4).
type VerifiedResourceFact struct {
	PhysicalKey       ID     `json:"physicalKey"`
	FactID            ID     `json:"factId"`
	ScopeID           ID     `json:"scopeId"`
	ProviderTypeID    ID     `json:"providerTypeId"`
	MetricID          ID     `json:"metricId"`
	Unit              string `json:"unit"`
	QuantumExp        int    `json:"quantumExp"`
	Currency          string `json:"currency,omitempty"`
	Metered           bool   `json:"metered"`
	SemanticsRef      ID     `json:"semanticsRef"`
	SemanticsRevision ID     `json:"semanticsRevision"`
	// Canonical window of the verified fact: Kind/Period/TimeZone/Anchor per
	// the config WindowDef shape (identity uses the normalized rule, not samples).
	WindowKind       string `json:"windowKind"`
	WindowPeriod     string `json:"windowPeriod,omitempty"`
	WindowTimeZone   string `json:"windowTimeZone,omitempty"`
	WindowAnchorISO  string `json:"windowAnchorISO,omitempty"`
	WindowDurationMs int64  `json:"windowDurationMillis,omitempty"`
	CapacityBound    Units  `json:"capacityBound"`
	InitialRemaining Units  `json:"initialRemaining"`
}

// VerifiedScope is one server-verified billing scope: an account scope or a
// shared scope (with aliasing accounts/bindings) carrying its fact instances.
type VerifiedScope struct {
	ScopeID        ID                     `json:"scopeId"`
	Kind           string                 `json:"kind"` // account|shared
	Aliases        []ID                   `json:"aliases,omitempty"`
	ProviderTypeID ID                     `json:"providerTypeId"`
	Facts          []VerifiedResourceFact `json:"facts"`
}

// WindowDefShape renders the verified fact's window as the config WindowDef
// for canonical comparison.
func (f VerifiedResourceFact) WindowRule() WindowRule {
	return WindowRule{Kind: f.WindowKind, Period: f.WindowPeriod, TimeZone: f.WindowTimeZone, AnchorISO: f.WindowAnchorISO, DurationMillis: f.WindowDurationMs}
}

type ResourceOffer struct {
	OfferID           ID     `json:"offerId"`
	BindingID         ID     `json:"bindingId"`
	Domain            string `json:"domain"`
	CapabilityProfile ID     `json:"capabilityProfile"`
	Enabled           bool   `json:"enabled"`
	ResourceRefs      []ID   `json:"resourceRefs"`
	Version           int64  `json:"version"`
}

type PoolMember struct {
	OfferID  ID    `json:"offerId"`
	Priority int64 `json:"priority"`
	Enabled  bool  `json:"enabled"`
	Version  int64 `json:"version"`
}

type ResourcePool struct {
	PoolID    ID           `json:"poolId"`
	Namespace ID           `json:"namespace"`
	Name      string       `json:"name"`
	Enabled   bool         `json:"enabled"`
	Version   int64        `json:"version"`
	Members   []PoolMember `json:"members"`
}

type ConfigDraft struct {
	DraftID      ID              `json:"draftId"`
	BaseRevision ID              `json:"baseRevision"`
	Version      int64           `json:"version"`
	Status       string          `json:"status"` // editing/validated/activated/archived
	Definition   json.RawMessage `json:"definition"`
	FieldIssues  []FieldIssue    `json:"fieldIssues"`
}

type ActivationInput struct {
	CommandID              ID    `json:"commandId"`
	DraftID                ID    `json:"draftId"`
	ExpectedConfigRevision ID    `json:"expectedConfigRevision"`
	ExpectedDraftVersion   int64 `json:"expectedDraftVersion"`
}

type ActivationResult struct {
	ConfigRevision ID    `json:"configRevision"`
	ControlVersion int64 `json:"controlVersion"`
}

// ReorderPoolInput: K4 freezes the authoritative write path — the full group
// is submitted with one explicit numeric priority per member. Drag-and-drop UIs
// generate concrete numbers (10,20,30…); equal values tie-break by offerId.
// There is no second orderedOfferIds entry point.
type ReorderPoolInput struct {
	CommandID              ID                 `json:"commandId"`
	ExpectedPoolVersion    int64              `json:"expectedPoolVersion"`
	ExpectedConfigRevision ID                 `json:"expectedConfigRevision"`
	Entries                []ReorderPoolEntry `json:"entries"`
}

type ReorderPoolEntry struct {
	OfferID  ID    `json:"offerId"`
	Priority int64 `json:"priority"`
}

type BoundEvidence struct {
	EvidenceID         ID
	MetricID           ID
	Unit               string
	UpperBound         Units
	EnforcementSource  string
	MappingRevision    ID
	EndpointRef        ID
	EnforcedParameters json.RawMessage
	SourceRef          ID
	VerifiedAt         Millis
	ExpiresAt          Millis
}

type ResourceRequirement struct {
	MetricID    ID
	Unit        string
	UpperBound  Units
	EvidenceID  ID
	Mode        string // all_constraints / provider_entitlement_waterfall
	ResourceIDs []ID
}

type ExecutionPlan struct {
	PlanID                ID
	ConfigRevision        ID
	DomainRevision        ID
	AdapterRevision       ID
	ProviderTypeID        ID
	TargetModelID         ID
	ResponseModelIDs      []ID
	Endpoint              string
	CredentialRef         ID
	PoolID                ID
	PoolVersion           int64
	OfferID               ID
	MemberPriority        int64
	AccountID             ID
	BindingID             ID
	CredentialRevision    ID
	Requirements          []ResourceRequirement
	Bounds                []BoundEvidence
	WindowRuleRevision    ID
	ReportMappingRevision ID
}

type GuardEvidence struct {
	ID         ID
	Scope      ID
	Revision   int64
	Allowed    bool
	ObservedAt Millis
	ExpiresAt  Millis
	SourceRef  ID
}

type LiveAdmission struct {
	ControlVersion     int64
	PoolVersion        int64
	Enabled            bool
	CredentialRevision ID
	AllowMetered       bool
	Holds              []ID
	Guards             []GuardEvidence
}

type Candidate struct {
	Member  PoolMember
	Offer   ResourceOffer
	Plan    ExecutionPlan
	Reasons []Reason
}

type WindowRule struct {
	Kind           string // fixed / rolling / lifetime
	Period         string // minute/hour/day/week/month (fixed)
	TimeZone       string
	AnchorISO      string
	DurationMillis int64 // rolling duration
}

type ResourceDefinition struct {
	ResourceID                 ID
	PhysicalKey                ID
	OwnerKind                  string // account / binding
	OwnerID                    ID
	MetricID                   ID
	Unit                       string
	QuantumExp                 int
	Kind                       string // counter / occupancy
	CommercialRole             string // limit / entitlement
	SemanticsRef               ID
	SemanticsRevision          ID
	Window                     WindowRule
	Metered                    bool
	Currency                   string
	ProviderOrder              *int64
	ProviderFactID             ID
	CapacityEvidenceRef        ID
	ConfiguredCapacity         Units
	InitialStateEvidenceRef    ID
	ExpiresAt                  *Millis
	RefillRule                 json.RawMessage
	ProviderEntitlementGroupID ID
}

type BindingDefinition struct {
	BindingID          ID
	AccountID          ID
	AdapterRef         ID
	Endpoint           string
	Domain             string
	CredentialRef      ID
	CredentialRevision ID
	ResourceRefs       []ID
	Bounds             []BoundEvidence
	Definition         json.RawMessage
}

type ResourceSnapshot struct {
	ResourceID ID
	WindowKey  ID
	Kind       string
	MetricID   ID
	Unit       string
	Version    int64
	Capacity   Units
	Committed  Units
	Adjustment Units
	Reserved   Units
	Active     Units
	Uncertain  Units
	Available  Units
}

type ResourceDelta struct {
	ResourceID      ID
	WindowKey       ID
	ExpectedVersion int64
	Committed       Units
	Adjustment      Units
	Reserved        Units
	Active          Units
	Uncertain       Units
	EvidenceRefs    []ID
}

// ResourceChangePlan is the only way a pure rule proposes persistence; Ledger
// validates it against I01–I10 before commit. No SQL, no capacity minting.
// AttributionIsolation asks the Ledger to attach an open window-attribution
// encumbrance + scoped hold in the SAME transaction that applies the plan.
// It is the only way an unattributed charge may conclude a settle: releasing
// reservations while silently reopening capacity is forbidden (R11/I06).
type AttributionIsolation struct {
	ResourceID ID
	WindowKey  ID // empty = cross-window isolation until proof of ownership
	Units      Units
	Reason     Reason
}

type ResourceChangePlan struct {
	Admitted bool
	Deltas   []ResourceDelta
	Reasons  []Reason
	// IsolationRequests must be non-empty whenever an unattributed charge is
	// settled (Ledger validates this contract; pure rules produce it).
	IsolationRequests []AttributionIsolation
}

type ReservationAllocation struct {
	ResourceID ID
	WindowKey  ID
	Units      Units
	Kind       string // counter / occupancy
}

type ReservationState struct {
	ReservationAllocation
	ReservationID ID
	AttemptID     ID
	Phase         string // reserved/executing/settled/released/uncertain
}

type PreparedAttempt struct {
	AttemptID     ID
	Plan          ExecutionPlan
	RequestDigest string
	Allocations   []ReservationAllocation
}

type ObservationEvidence struct {
	ID                       ID
	ResourceID               ID
	WindowKey                ID
	Remaining                Units
	CommittedAtStart         Units
	AdjustmentVersionAtStart int64
	ObservedAt               Millis
	ExpiresAt                Millis
	SourceRef                ID
}

type MetricReport struct {
	MetricID ID
	Unit     string
	Actual   *Units
	// Conservative marks an explicit upper-bound projection (for example,
	// pricing at the provider's peak rate when the response omits billed cost).
	// The amount remains exact as a ledger quantity, but is not represented as
	// the provider's actual charge.
	Conservative            bool   `json:"conservative,omitempty"`
	ActualOutOfRangeDecimal string `json:"ActualOutOfRangeDecimal,omitempty"`
	ChargedAt               *Millis
	EvidenceRefs            []ID
}

type ExecutionReport struct {
	ReportID          ID
	AttemptID         ID
	Outcome           string
	RetrySafety       RetrySafety
	RetryClass        ID
	ProviderRequestID ID
	Metrics           []MetricReport
	ExternalEnded     bool
	EndEvidenceRefs   []ID
	Reasons           []Reason
	RawDigest         string
	Observation       *ObservationEvidence
}

type ResourceContext struct {
	Now                       Millis
	Definition                ResourceDefinition
	Snapshot                  ResourceSnapshot
	Observation               *ObservationEvidence
	RollingUsed               *Units
	HoldIDs                   []ID
	AttributionEncumbranceIDs []ID
	// AdjustmentVersion is the AUTHORITATIVE current adjustment version from
	// the ledger snapshot (R11). Rules must compare observations against
	// this value, never against Observation.AdjustmentVersionAtStart — the
	// evidence's own version is the past, not the present.
	AdjustmentVersion int64
}

// EndProofKind separates the distinct proof facts (R11): an arbitrary
// evidence ID never constitutes any of them.
type EndProofKind string

const (
	// EndProofNone: no proof about external execution; conservation rules apply.
	EndProofNone EndProofKind = "none"
	// EndProofReliableUnsent: the controlled Transport proves the request
	// never left the process (socket write not started).
	EndProofReliableUnsent EndProofKind = "reliable_unsent"
	// EndProofExternalEnded: verified provider-side termination of this call.
	EndProofExternalEnded EndProofKind = "external_ended"
)

// WindowAttribution states whether the consumption is provably attributable
// to the reservation's window.
type WindowAttribution string

const (
	// AttributionKnown: charge time/ownership provably inside this window.
	AttributionKnown WindowAttribution = "attributed"
	// AttributionUnknown: charge window/ownership unproven — the plan must
	// request attribution isolation instead of silently freeing capacity.
	AttributionUnknown WindowAttribution = "unattributed"
)

type SettlementProjection struct {
	Actual    *Units
	ChargedAt *Millis
	// End is the structured end-of-execution proof; EndProofNone means no
	// claim, and EvidenceRefs only document the proof — they never stand in
	// for it.
	End          EndProofKind
	EvidenceRefs []ID
	// Attribution is the verified window/ownership attribution of the usage.
	Attribution WindowAttribution
}
