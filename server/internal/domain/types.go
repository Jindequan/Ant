// Package domain holds the shared core value types of ANT (V0.7 contract).
// Pure types only: no SQL, no network, no process environment. Wire envelopes
// live in internal/api; persistence records in internal/store; ports for the
// ledger/transport live in their own packages. Type names follow
// docs/design/contracts.go; deviations required by K-gate decisions are noted.
package domain

import "encoding/json"

type ID string

// Units is an exact minimal-unit integer. The wire form is ALWAYS a JSON
// string in canonical decimal spelling ("0", "-5", "9223372036854775807");
// bare JSON numbers, floats, exponents, leading zeros, "-0", null and
// out-of-int64-range values are rejected by UnmarshalJSON (R04). The custom
// marshal/unmarshal replaces the former `,string` tag: a `,string` tag on a
// Marshaler type would double-quote the output, and the built-in string
// decode loses the original spelling ("010"→10) before any strict check
// could run.
type Units int64
type Millis int64
type Permission string
type OperationState string
type RetrySafety string
type ConsumptionBasis string

const (
	ConsumptionProviderReported  ConsumptionBasis = "provider_reported"
	ConsumptionConservativeBound ConsumptionBasis = "conservative_bound"
	ConsumptionNotExecuted       ConsumptionBasis = "not_executed"
	ConsumptionAdjustment        ConsumptionBasis = "adjustment"
	ConsumptionNotApplicable     ConsumptionBasis = "not_applicable"
	ConsumptionUnrepresentable   ConsumptionBasis = "unrepresentable"
)

// ConsumptionBasisValues returns the exact set accepted by the versioned
// ResourceOutcome wire contract.
func ConsumptionBasisValues() []ConsumptionBasis {
	return []ConsumptionBasis{
		ConsumptionProviderReported,
		ConsumptionConservativeBound,
		ConsumptionNotExecuted,
		ConsumptionAdjustment,
		ConsumptionNotApplicable,
		ConsumptionUnrepresentable,
	}
}

const (
	Running   OperationState = "running"
	Succeeded OperationState = "succeeded"
	Failed    OperationState = "failed"
	Cancelled OperationState = "cancelled"
	Unknown   OperationState = "unknown"

	ProvedSafe   RetrySafety = "proved_safe"
	Unsafe       RetrySafety = "unsafe"
	Undetermined RetrySafety = "undetermined"

	PermExecute   Permission = "execute"
	PermRead      Permission = "read"
	PermCancel    Permission = "cancel"
	PermManage    Permission = "manage"
	PermConfigure Permission = "configure"
)

// OperationStateValues returns the complete persisted/wire state set.
func OperationStateValues() []OperationState {
	return []OperationState{Running, Succeeded, Failed, Cancelled, Unknown}
}

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

type ControlSignal struct {
	Key             OperationKey
	CancelRequested bool
	ControlVersion  int64
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

// DomainRequest.Payload is validated by a registered DomainPolicy, never
// arbitrary code; unknown executable fields are rejected at the boundary.
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
	Consumed         *Units           `json:"consumed"` // nil means no representable committed consumption is reported
	ConsumptionBasis ConsumptionBasis `json:"consumptionBasis"`
	Occupancy        string           `json:"occupancy"`
	EvidenceRefs     []ID             `json:"evidenceRefs"`
}

// UnrepresentableMetric carries an exact provider-reported value that exceeds
// the ledger's int64 accounting range. It is review evidence, not a committed
// ledger quantity.
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

// AdjustableChargeView identifies the remaining positive charge an authorized
// manager may correct. The database computes the reversible remainder; the
// client never constructs event IDs or replays raw ledger rows.
type AdjustableChargeView struct {
	EventID             ID     `json:"eventId"`
	ResourceID          ID     `json:"resourceId"`
	WindowKey           ID     `json:"windowKey"`
	MetricID            ID     `json:"metricId"`
	Unit                string `json:"unit"`
	RemainingReversible Units  `json:"remainingReversible"`
}

type ManagedHoldView struct {
	HoldID       ID     `json:"holdId"`
	ResourceID   ID     `json:"resourceId"`
	ScopeKind    string `json:"scopeKind"`
	ScopeID      ID     `json:"scopeId"`
	Reason       string `json:"reason"`
	EvidenceRefs []ID   `json:"evidenceRefs"`
	CanClear     bool   `json:"canClear"`
}

// ReconciliationInput is the management evidence and corrected resource state
// supplied for a terminal attempt whose provider outcome is unresolved.
type ReconciliationInput struct {
	CommandID        ID                `json:"commandId"`
	AttemptID        ID                `json:"attemptId,omitempty"`
	ConfirmedOutcome string            `json:"confirmedOutcome"`
	RetrySafety      RetrySafety       `json:"retrySafety"`
	EvidenceRefs     []ID              `json:"evidenceRefs"`
	Corrections      []ResourceOutcome `json:"corrections"`
}

type CommandAck struct {
	CommandID ID     `json:"commandId"`
	Status    string `json:"status"`
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

type ClearHoldInput struct {
	CommandID   ID
	HoldID      ID
	EvidenceIDs []ID
}

type OperationView struct {
	OperationID       ID                     `json:"operationId"`
	PoolID            ID                     `json:"poolId"`
	RequestDomain     string                 `json:"requestDomain"`
	State             OperationState         `json:"state"`
	CancelRequested   bool                   `json:"cancelRequested"`
	Attempts          []AttemptView          `json:"attempts"`
	Resources         []ResourceOutcome      `json:"resources"`
	Result            *OperationResult       `json:"result,omitempty"`
	Reconciliations   []ReconciliationView   `json:"reconciliations"`
	AdjustableCharges []AdjustableChargeView `json:"adjustableCharges,omitempty"`
	ManagedHolds      []ManagedHoldView      `json:"managedHolds,omitempty"`
	UpdatedAt         Millis                 `json:"updatedAt"`
}

// SubmitReply: K6 adds State to the accepted variant so wire examples and the
// codec agree (accepted is always state=running; running projection may evolve).
type SubmitReply struct {
	Kind         string           `json:"kind"` // accepted/inProgress/replay/rejected
	State        OperationState   `json:"state,omitempty"`
	OperationID  ID               `json:"operationId"`
	OperationURL string           `json:"operationUrl,omitempty"`
	EventsURL    string           `json:"eventsUrl,omitempty"`
	Operation    *OperationView   `json:"operation,omitempty"`
	Result       *OperationResult `json:"result,omitempty"`
	Error        *ErrorDTO        `json:"error,omitempty"`
}

// ErrorDTO is the inner error body; the HTTP envelope adds requestId (K6).
type ErrorDTO struct {
	Code        string            `json:"code"`
	MessageKey  string            `json:"messageKey"`
	Params      map[string]string `json:"params,omitempty"`
	FieldIssues []FieldIssue      `json:"fieldIssues,omitempty"`
	OperationID ID                `json:"operationId,omitempty"`
	Retryable   bool              `json:"retryable"`
	Action      string            `json:"action"`
}

type CancellationAck struct {
	Status string `json:"status"` // recorded/alreadyTerminal/notFound
}

type OutputChunk struct {
	Sequence int64  `json:"sequence"`
	Data     []byte `json:"data"` // base64 in JSON; text rendering follows domain decoder.
}

const (
	MaxOutputChunkBytes     = 32 << 10
	MaxOperationOutputBytes = 16 << 20
)

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

type DecisionView struct {
	DecisionID            ID                  `json:"decisionId"`
	AttemptID             ID                  `json:"attemptId,omitempty"`
	PoolID                ID                  `json:"poolId"`
	PoolVersion           int64               `json:"poolVersion"`
	OfferID               ID                  `json:"offerId,omitempty"`
	MemberPriority        int64               `json:"memberPriority"`
	ConfigRevision        ID                  `json:"configRevision"`
	Stage                 string              `json:"stage"`
	Candidates            []CandidateDecision `json:"candidates"`
	TraceTruncated        bool                `json:"traceTruncated"`
	HookRegistryRevision  string              `json:"hookRegistryRevision,omitempty"`
	HookRegistryManifest  json.RawMessage     `json:"hookRegistryManifest,omitempty"`
	PlannerCandidateOrder []ID                `json:"plannerCandidateOrder,omitempty"`
	HookCandidateOrder    []ID                `json:"hookCandidateOrder,omitempty"`
}

type CandidateDecision struct {
	OfferID  ID       `json:"offerId"`
	Priority int64    `json:"priority"`
	Reasons  []Reason `json:"reasons"`
}
