// ANT V0.7 design contract. Types and ports only; no runtime implementation.
// Internal ports and permit types must never be exposed as HTTP DTOs.
package contracts

import (
	"context"
	"encoding/json"
	"io"
)

type ID string

// Units: exact minimal-unit integer with custom JSON marshal/unmarshal.
// The wire form is always a canonical decimal STRING; the former `,string`
// tag is gone (double-quoted marshaler output, and the built-in decode loses
// the original spelling before strict checks can run). Rework R01/R04.
type Units int64
type Millis int64
type Permission string
type OperationState string
type RetrySafety string
type ConsumptionBasis string

const (
	Running                      OperationState   = "running"
	Succeeded                    OperationState   = "succeeded"
	Failed                       OperationState   = "failed"
	Cancelled                    OperationState   = "cancelled"
	Unknown                      OperationState   = "unknown"
	ProvedSafe                   RetrySafety      = "proved_safe"
	Unsafe                       RetrySafety      = "unsafe"
	Undetermined                 RetrySafety      = "undetermined"
	ConsumptionProviderReported  ConsumptionBasis = "provider_reported"
	ConsumptionConservativeBound ConsumptionBasis = "conservative_bound"
	ConsumptionNotExecuted       ConsumptionBasis = "not_executed"
	ConsumptionAdjustment        ConsumptionBasis = "adjustment"
	ConsumptionNotApplicable     ConsumptionBasis = "not_applicable"
	ConsumptionUnrepresentable   ConsumptionBasis = "unrepresentable"
)

type AuthContext struct {
	PrincipalID ID
	Namespace   ID
	Permissions []Permission
}
type OperationKey struct {
	Namespace   ID
	OperationID ID
}
type OwnerToken struct {
	Key        OperationKey
	RuntimeID  ID
	OwnerID    ID
	Generation int64
}
type Reason struct {
	Group       string `json:"group"`
	Code        string `json:"code"`
	ScopeRef    ID     `json:"scopeRef,omitempty"`
	EvidenceRef ID     `json:"evidenceRef,omitempty"`
}
type FieldIssue struct {
	Path       string `json:"path"`
	MessageKey string `json:"messageKey"`
}
type ErrorDTO struct {
	Code        string            `json:"code"`
	MessageKey  string            `json:"messageKey"`
	Params      map[string]string `json:"params,omitempty"`
	FieldIssues []FieldIssue      `json:"fieldIssues,omitempty"`
	OperationID ID                `json:"operationId,omitempty"`
	Retryable   bool              `json:"retryable"`
	Action      string            `json:"action"`
}

// Payload is validated by a server-registered DomainPolicy, not arbitrary code.
type DomainRequest struct {
	Domain            string          `json:"domain"`
	CapabilityProfile ID              `json:"capabilityProfile"`
	Payload           json.RawMessage `json:"payload"`
}
type Affinity struct {
	Mode      string `json:"mode"`
	AccountID ID     `json:"accountId,omitempty"`
	BindingID ID     `json:"bindingId,omitempty"`
}
type ExecuteInput struct {
	OperationID  ID            `json:"operationId"`
	PoolID       ID            `json:"poolId"`
	Request      DomainRequest `json:"request"`
	DeadlineAt   Millis        `json:"deadlineAt"`
	MaxAttempts  int           `json:"maxAttempts"`
	Affinity     *Affinity     `json:"affinity,omitempty"`
	AllowMetered bool          `json:"allowMetered"`
}
type ResourceOutcome struct {
	ResourceID       ID               `json:"resourceId"`
	WindowKey        ID               `json:"windowKey"`
	MetricID         ID               `json:"metricId"`
	Unit             string           `json:"unit"`
	Consumed         *Units           `json:"consumed"`
	ConsumptionBasis ConsumptionBasis `json:"consumptionBasis"`
	Occupancy        string           `json:"occupancy"`
	EvidenceRefs     []ID             `json:"evidenceRefs"`
}
type UnrepresentableMetric struct {
	MetricID                ID     `json:"metricId"`
	Unit                    string `json:"unit"`
	ActualOutOfRangeDecimal string `json:"actualOutOfRangeDecimal"`
	EvidenceRefs            []ID   `json:"evidenceRefs"`
}
type OperationResult struct {
	OperationID            ID                      `json:"operationId"`
	State                  OperationState          `json:"state"`
	Code                   string                  `json:"code"`
	Reasons                []Reason                `json:"reasons"`
	RetrySafety            RetrySafety             `json:"retrySafety"`
	Attempts               []ID                    `json:"attempts"`
	Resources              []ResourceOutcome       `json:"resources"`
	UnrepresentableMetrics []UnrepresentableMetric `json:"unrepresentableMetrics,omitempty"`
	DecisionRefs           []ID                    `json:"decisionRefs"`
	OutputRef              ID                      `json:"outputRef,omitempty"`
	OutputAvailability     string                  `json:"outputAvailability"`
	SuggestedAction        string                  `json:"suggestedAction"`
}
type AttemptView struct {
	AttemptID         ID     `json:"attemptId"`
	OfferID           ID     `json:"offerId"`
	BindingID         ID     `json:"bindingId"`
	AccountID         ID     `json:"accountId"`
	Phase             string `json:"phase"`
	Outcome           string `json:"outcome,omitempty"`
	ProviderRequestID ID     `json:"providerRequestId,omitempty"`
}
type ReconciliationView struct {
	ReconciliationID ID                `json:"reconciliationId"`
	ConfirmedOutcome string            `json:"confirmedOutcome"`
	RetrySafety      RetrySafety       `json:"retrySafety"`
	EvidenceRefs     []ID              `json:"evidenceRefs"`
	Corrections      []ResourceOutcome `json:"corrections"`
	CreatedAt        Millis            `json:"createdAt"`
}
type OperationView struct {
	OperationID     ID                   `json:"operationId"`
	PoolID          ID                   `json:"poolId"`
	State           OperationState       `json:"state"`
	CancelRequested bool                 `json:"cancelRequested"`
	Attempts        []AttemptView        `json:"attempts"`
	Resources       []ResourceOutcome    `json:"resources"`
	Result          *OperationResult     `json:"result,omitempty"`
	Reconciliations []ReconciliationView `json:"reconciliations"`
	UpdatedAt       Millis               `json:"updatedAt"`
}
type SubmitReply struct {
	Kind         string           `json:"kind"`            // accepted/inProgress/replay/rejected; codec guards variants.
	State        OperationState   `json:"state,omitempty"` // K6: accepted carries state="running" always.
	OperationID  ID               `json:"operationId"`
	OperationURL string           `json:"operationUrl,omitempty"`
	EventsURL    string           `json:"eventsUrl,omitempty"`
	Operation    *OperationView   `json:"operation,omitempty"`
	Result       *OperationResult `json:"result,omitempty"`
	Error        *ErrorDTO        `json:"error,omitempty"`
}
type CancellationAck struct {
	Status string `json:"status"`
}
type OutputChunk struct {
	Sequence int64  `json:"sequence"`
	Data     []byte `json:"data"` // base64 on JSON; text rendering follows domain decoder.
}
type OutputPage struct {
	Chunks       []OutputChunk `json:"chunks"`
	NextSequence *int64        `json:"nextSequence,omitempty"`
	Complete     bool          `json:"complete"`
	Availability string        `json:"availability"`
}
type OperationEvent struct {
	OperationID ID              `json:"operationId"`
	Sequence    int64           `json:"sequence"`
	Type        string          `json:"type"`
	CreatedAt   Millis          `json:"createdAt"`
	Payload     json.RawMessage `json:"payload"`
}

type LoginInput struct {
	Token string `json:"token"` // high-entropy credential, never logged or stored in Web storage.
}
type SessionView struct {
	PrincipalID ID           `json:"principalId"`
	Namespace   ID           `json:"namespace"`
	Permissions []Permission `json:"permissions"`
	CSRFToken   string       `json:"csrfToken"`
	ExpiresAt   Millis       `json:"expiresAt"`
}
type SecretWriteInput struct {
	CommandID        ID     `json:"commandId"`
	ExpectedRevision ID     `json:"expectedRevision,omitempty"`
	Value            string `json:"value"` // write-only; never returned in any projection.
}
type SecretMetadata struct {
	Ref      ID     `json:"ref"`
	Revision ID     `json:"revision"`
	Mask     string `json:"mask"`
}

// Read projections use their own wire quantities, never ResourceSnapshot directly.
type ResourceView struct {
	ResourceID               ID     `json:"resourceId"`
	WindowKey                ID     `json:"windowKey"`
	MetricID                 ID     `json:"metricId"`
	Unit                     string `json:"unit"`
	Available                *Units `json:"available,string,omitempty"`
	Reserved                 Units  `json:"reserved,string"`
	Active                   Units  `json:"active,string"`
	Uncertain                Units  `json:"uncertain,string"`
	EvidenceStatus           string `json:"evidenceStatus"`
	HoldIDs                  []ID   `json:"holdIds"`
	EncumbranceIDs           []ID   `json:"encumbranceIds"`
	ObservationRefreshNeeded bool   `json:"observationRefreshNeeded"`
}
type DecisionView struct {
	DecisionID     ID                  `json:"decisionId"`
	AttemptID      ID                  `json:"attemptId,omitempty"`
	PoolID         ID                  `json:"poolId"`
	PoolVersion    int64               `json:"poolVersion"`
	OfferID        ID                  `json:"offerId,omitempty"`
	MemberPriority int64               `json:"memberPriority"`
	ConfigRevision ID                  `json:"configRevision"`
	Stage          string              `json:"stage"`
	Candidates     []CandidateDecision `json:"candidates"`
	TraceTruncated bool                `json:"traceTruncated"`
}
type CandidateDecision struct {
	OfferID  ID       `json:"offerId"`
	Priority int64    `json:"priority"`
	Reasons  []Reason `json:"reasons"`
}
type AuthorizedView struct {
	Kind      string         `json:"kind"`
	TargetID  ID             `json:"targetId"`
	Resources []ResourceView `json:"resources"`
	Decisions []DecisionView `json:"decisions"`
	Operation *OperationView `json:"operation,omitempty"`
}

type ProviderDescriptor struct {
	TypeID           ID              `json:"typeId"`
	AdapterRevision  ID              `json:"adapterRevision"`
	SupportedDomains []string        `json:"supportedDomains"`
	ConfigSchema     json.RawMessage `json:"configSchema"`
	ResourceSchema   json.RawMessage `json:"resourceSchema"`
	CapabilitySchema json.RawMessage `json:"capabilitySchema"`
	// Rework R12: closed extension schema + provider-verified bucket facts.
	ExtensionSchema  json.RawMessage   `json:"extensionSchema"`
	EntitlementFacts []EntitlementFact `json:"entitlementFacts"`
}

type EntitlementFact struct {
	FactID      ID     `json:"factId"`
	MetricID    ID     `json:"metricId"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
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
type PoolMemberView struct {
	Member    PoolMember     `json:"member"`
	Offer     ResourceOffer  `json:"offer"`
	Status    string         `json:"status"`
	Reasons   []Reason       `json:"reasons"`
	Resources []ResourceView `json:"resources"`
}
type PoolGroup struct {
	Domain            string           `json:"domain"`
	CapabilityProfile ID               `json:"capabilityProfile"`
	Members           []PoolMemberView `json:"members"`
}
type PoolView struct {
	Pool   ResourcePool `json:"pool"`
	Groups []PoolGroup  `json:"groups"` // no sum of heterogeneous or overlapping capacity.
}

// K4: the authoritative write path submits the full group; every member
// appears exactly once with an explicit numeric priority (drag-and-drop
// generates concrete numbers). Equal priorities tie-break by offerId.
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
type ConfigDraft struct {
	DraftID      ID              `json:"draftId"`
	BaseRevision ID              `json:"baseRevision"`
	Version      int64           `json:"version"`
	Status       string          `json:"status"`
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
	Mode        string // all_constraints/provider_entitlement_waterfall
	ResourceIDs []ID
}
type ExecutionPlan struct {
	PlanID                ID
	ConfigRevision        ID
	DomainRevision        ID
	AdapterRevision       ID
	ProviderTypeID        ID
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
	Kind           string
	Period         string
	TimeZone       string
	AnchorISO      string
	DurationMillis int64
}
type ResourceDefinition struct {
	ResourceID                 ID
	OwnerKind                  string
	OwnerID                    ID
	MetricID                   ID
	Unit                       string
	QuantumExp                 int
	Kind                       string
	CommercialRole             string
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
type ResourceChangePlan struct {
	Admitted bool
	Deltas   []ResourceDelta
	Reasons  []Reason
	// IsolationRequests must accompany any unattributed settle (Rework R11):
	// the Ledger attaches the encumbrance/hold in the same transaction.
	IsolationRequests []AttributionIsolation
}

type AttributionIsolation struct {
	ResourceID ID
	WindowKey  ID // empty = cross-window until ownership proof
	Units      Units
	Reason     Reason
}
type ReservationAllocation struct {
	ResourceID ID
	WindowKey  ID
	Units      Units
	Kind       string
}
type ReservationState struct {
	ReservationAllocation
	ReservationID ID
	AttemptID     ID
	Phase         string
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
	MetricID                ID
	Unit                    string
	Actual                  *Units
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
	// AdjustmentVersion is the ledger's authoritative current value; rules
	// compare observations against it, never against the observation's own
	// version (Rework R11).
	AdjustmentVersion int64
}
type EndProofKind string

const (
	EndProofNone           EndProofKind = "none"
	EndProofReliableUnsent EndProofKind = "reliable_unsent"
	EndProofExternalEnded  EndProofKind = "external_ended"
)

type WindowAttribution string

const (
	AttributionKnown   WindowAttribution = "attributed"
	AttributionUnknown WindowAttribution = "unattributed"
)

// SettlementProjection (Rework R11): structured proof facts; a non-empty
// evidence ID list never substitutes for the proof itself.
type SettlementProjection struct {
	Actual       *Units
	ChargedAt    *Millis
	End          EndProofKind
	EvidenceRefs []ID
	Attribution  WindowAttribution
}
type ResourceSemantics interface {
	Quote(ResourceContext) (Units, []Reason, error)
	Reserve(ResourceContext, Units) (ResourceChangePlan, error)
	Activate(ResourceContext, ReservationState) (ResourceChangePlan, error)
	Settle(ResourceContext, ReservationState, SettlementProjection) (ResourceChangePlan, error)
}
type DomainPolicy interface {
	Normalize(DomainRequest) (json.RawMessage, error)
	Compile(DomainRequest, BindingDefinition, ResourceOffer) (ExecutionPlan, error)
	Classify(ExecutionReport, ExecutionPlan) (ExecutionReport, error)
}
type DomainGuard interface {
	Collect(context.Context, DomainRequest, BindingDefinition) ([]GuardEvidence, error)
}
type Clock interface{ Now() Millis }
type SecretResolver interface {
	Resolve(context.Context, ID, ID) (map[string]string, error)
}
type EncodedRequest struct {
	Endpoint         string
	Method           string
	Headers          map[string]string
	Body             []byte
	CredentialRef    ID
	CredentialHeader string
	CredentialField  string
	CredentialPrefix string
	MaxResponseBytes int64
}
type RawResponse struct {
	Status  int
	Headers map[string]string
	Body    io.ReadCloser
}
type ProviderAdapter interface {
	Descriptor() ProviderDescriptor
	Encode(DomainRequest, ExecutionPlan) (EncodedRequest, error)
	Decode(context.Context, RawResponse, ExecutionPlan, ID, func([]byte) error) (ExecutionReport, error)
}

// Optional read capability, registered separately from public DomainRequest.
type CapacityProbeRequest struct {
	ResourceID ID
	BindingID  ID
}
type CapacityReader interface {
	EncodeProbe(CapacityProbeRequest, ExecutionPlan) (EncodedRequest, error)
	DecodeObservation(context.Context, RawResponse, ExecutionPlan) (ObservationEvidence, error)
}

// K2: these handles are owned by server-internal packages — DispatchPermit by
// internal/ledger (issued via PermitRegistry), PreparedHTTPRequest by
// internal/transport (HandleTable with a local one-shot marker). No public
// constructor, no JSON fields; identity is read back through controlled
// accessors only.
type DispatchPermit struct {
	permitID  ID
	attemptID ID
	runtimeID ID
	nonce     ID
}
type PreparedHTTPRequest struct {
	handleID           ID
	requestDigest      string
	credentialRevision ID
}
type Transport interface {
	Prepare(context.Context, AuthContext, OwnerToken, ID, EncodedRequest, ExecutionPlan) (PreparedHTTPRequest, error)
	Discard(PreparedHTTPRequest)
	Dispatch(context.Context, DispatchPermit, PreparedHTTPRequest) (RawResponse, error)
}
type ClaimReply struct {
	Kind      string
	Owner     *OwnerToken
	Operation *OperationView
	Result    *OperationResult
}
type PrepareReply struct {
	Kind    string
	Attempt *PreparedAttempt
	Reasons []Reason
}
type SettleReply struct {
	Continue bool
	Result   *OperationResult
}
type ControlSignal struct {
	Key             OperationKey
	CancelRequested bool
	ControlVersion  int64
}
type RefreshResult struct {
	Status  string   `json:"status"`
	Reasons []Reason `json:"reasons"`
}
type AdjustmentInput struct {
	CommandID       ID
	ResourceID      ID
	WindowKey       ID
	Delta           Units
	OriginalEventID ID
	EvidenceRefs    []ID
}
type ReconcileInput struct {
	CommandID        ID
	AttemptID        ID
	ConfirmedOutcome string
	RetrySafety      RetrySafety
	EvidenceRefs     []ID
	Corrections      []ResourceOutcome
}

// K3 (protocol frozen; transactional implementation in T2.2, see
// 03-EXECUTION.md §9): closes a prepared/granted attempt without external
// execution, or conserves it as unknown when evidence is missing.
type AbortBeforeDispatchInput struct {
	AttemptID       ID
	Owner           OwnerToken
	AttemptPhase    string // prepared | executing
	PermitStatus    string // none | granted | consumed | revoked
	Proof           string // none | reliable_unsent | external_ended
	CancelRequested bool
	EvidenceRefs    []ID
	CommandID       ID
}

type AbortOutcome struct {
	Kind          string // close_prepared / revoke_granted / settle_not_executed / conserve_unknown / already_closed / not_abortible
	ReplanAllowed bool
	AttemptID     ID
	Reasons       []Reason
}

type Ledger interface {
	Claim(context.Context, AuthContext, ExecuteInput, string) (ClaimReply, error)
	Quote(context.Context, AuthContext, ExecutionPlan) ([]ResourceSnapshot, error)
	TryPrepare(context.Context, AuthContext, OwnerToken, ID, ExecutionPlan, []GuardEvidence, string) (PrepareReply, error)
	AuthorizeDispatch(context.Context, AuthContext, OwnerToken, ID) (DispatchPermit, error)
	ConsumeDispatchPermit(context.Context, AuthContext, OwnerToken, DispatchPermit, string) error
	AbortBeforeDispatch(context.Context, AuthContext, AbortBeforeDispatchInput) (AbortOutcome, error)
	RenewLease(context.Context, AuthContext, []OwnerToken, Millis) error
	ReadControlSignals(context.Context, AuthContext, ID, int) ([]ControlSignal, error)
	RequestCancel(context.Context, AuthContext, OperationKey, ID) (CancellationAck, error)
	AppendOutput(context.Context, AuthContext, OwnerToken, ID, OutputChunk) error
	Settle(context.Context, AuthContext, OwnerToken, ExecutionReport) (SettleReply, error)
	CompleteWithoutAttempt(context.Context, AuthContext, OwnerToken, []Reason) (OperationResult, error)
	Sweep(context.Context, AuthContext, Millis, int) error
	ActivateConfig(context.Context, AuthContext, ActivationInput) (ActivationResult, error)
	ReorderPool(context.Context, AuthContext, ID, ReorderPoolInput) (ResourcePool, error)
	RecordGuardEvidence(context.Context, AuthContext, GuardEvidence) error
	RecordObservation(context.Context, AuthContext, ObservationEvidence) (RefreshResult, error)
	Adjust(context.Context, AuthContext, AdjustmentInput) error
	ClearHold(context.Context, AuthContext, ID, ID, []ID) error
	Reconcile(context.Context, AuthContext, ReconcileInput) (ReconciliationView, error)
}
type ReadModels interface {
	GetOperation(context.Context, AuthContext, ID) (*OperationView, error)
	ReadOutput(context.Context, AuthContext, ID, int64, int) (OutputPage, error)
	ReadEvents(context.Context, AuthContext, ID, int64, int) ([]OperationEvent, error)
	GetPool(context.Context, AuthContext, ID) (PoolView, error)
	GetDraft(context.Context, AuthContext, ID) (ConfigDraft, error)
	Inspect(context.Context, AuthContext, string, ID) (AuthorizedView, error)
}
type RuntimeSettings struct {
	MaxActiveOperations   int
	WriterQueueLimit      int
	ControlReserved       int
	ReadConnections       int
	LeaseMillis           int64
	HeartbeatMillis       int64
	SweepMillis           int64
	ControlPollMillis     int64
	DBBusyMillis          int64
	DBCommandBudgetMillis int64
	StreamBufferBytes     int
	OutputBatchBytes      int
	ConsumerIdleMillis    int64
	ResponseIdleMillis    int64
	MaxOutputBytes        int64
	PlanningMaxRounds     int
	MaxAttempts           int
	MaxDeadlineMillis     int64
}
