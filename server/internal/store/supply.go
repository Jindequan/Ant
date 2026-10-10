package store

import (
	"errors"

	"ant/server/internal/domain"
)

// Supply store (design 11 V0.2.1 §2.1, §2.4): observed, provider-verified
// facts. The supply engine is the only writer; configuration activation and
// the ledger never write these rows, and the supply engine never writes theirs.

var (
	// ErrSupplyIdentityConflict: a write would change an immutable identity
	// (a resource's physical key or dimensions, a package's account/template,
	// a credential's provider/origin) or reuse an id or unique key held by
	// another row of the namespace.
	ErrSupplyIdentityConflict = errors.New("store: supply row would change an immutable identity")
	// ErrSupplyMissingReference: a row names a parent (account, package,
	// resource, run, credential, or a secret of the same namespace) that does
	// not exist.
	ErrSupplyMissingReference = errors.New("store: supply row references a missing row")
	// ErrSupplyReadingOutOfOrder: a reading's observed_at is not strictly
	// newer than the latest reading of its resource (same millisecond or
	// older, including a replay of the latest reading).
	ErrSupplyReadingOutOfOrder = errors.New("store: supply reading is not newer than the latest reading")
	// ErrCollectorRunNotOpen: the run does not exist, is already finished,
	// or would finish before it started.
	ErrCollectorRunNotOpen      = errors.New("store: collector run is not open")
	ErrSupplyRouteOrderConflict = errors.New("store: supply route order version conflict")
	ErrSupplyRouteOrderInvalid  = errors.New("store: invalid supply route order")
	ErrSupplyCandidatesChanged  = errors.New("store: eligible supply routes changed")
)

// SupplyCredentialRecord is one way Ant authenticates to a provider. A found
// credential's secret stays where it lives (LocatorJSON); an added one is
// encrypted in secret_records and referenced by SecretRef.
type SupplyCredentialRecord struct {
	Namespace    domain.ID
	CredentialID domain.ID
	ProviderID   domain.ID
	Kind         string // api_key | oauth | session | sts | console_token | none
	Origin       string // found | added
	Strategy     string
	LocatorJSON  []byte
	SecretRef    domain.ID // "" = none
	AccountID    domain.ID // "" = not yet verified
	Status       string    // unverified | ok | signed_out | error | hidden | needs_key
	StatusDetail string
	VerifiedAt   *domain.Millis
	CreatedAt    domain.Millis
	Paused       bool // read only: set with SetSupplyPaused, never by an upsert
}

// SupplyOfferRecord is a model a credential can call at an endpoint, and
// the meters (resource template refs) a call there draws on.
type SupplyOfferRecord struct {
	Namespace     domain.ID
	OfferID       domain.ID
	CredentialID  domain.ID
	Endpoint      string
	Protocol      string
	UpstreamModel string
	ModelID       string
	DrawsJSON     []byte
	VerifiedAt    domain.Millis
}

// SupplyRouteOrderRecord is a per-model user preference. It orders offers
// only; current route eligibility is independently recalculated on every
// preview and cannot be changed by this record.
type SupplyRouteOrderRecord struct {
	Namespace domain.ID
	ModelID   string
	Revision  int64
	OfferIDs  []domain.ID
	UpdatedAt domain.Millis
}

// SupplyAccountRecord is one provider account: Q1 v3 VerifiedScope{kind: account}.
type SupplyAccountRecord struct {
	Namespace        domain.ID
	AccountID        domain.ID
	ProviderID       domain.ID
	ProviderIdentity string
	DisplayName      string
	UpdatedAt        domain.Millis
}

type SupplyPackageRecord struct {
	Namespace      domain.ID
	PackageID      domain.ID
	AccountID      domain.ID
	TemplateRef    string
	Kind           string // subscription | prepaid | payg | free_grant | credit_pack
	Basis          string // observed | declared
	Plan           string
	StartsAt       *domain.Millis
	RenewsAt       *domain.Millis
	EndsAt         *domain.Millis
	AutoRenew      *bool
	Live           bool
	DefinitionJSON []byte
	UpdatedAt      domain.Millis
}

// SupplyResourceRecord is one meter: Q1 v3 VerifiedResourceFact identity.
// PhysicalKey, MetricID, Unit, QuantumExp, Currency, WindowJSON and
// ScopeJSON never change for a ResourceID.
type SupplyResourceRecord struct {
	Namespace         domain.ID
	ResourceID        domain.ID
	PhysicalKey       domain.ID
	PackageID         domain.ID
	TemplateRef       string
	MetricID          domain.ID
	Unit              string
	QuantumExp        int
	Currency          string
	SemanticsRef      domain.ID
	SemanticsRevision domain.ID
	WindowJSON        []byte
	ScopeJSON         []byte
	UpdatedAt         domain.Millis
}

// SupplyReadingRecord is an immutable fact snapshot (§2.2.1).
type SupplyReadingRecord struct {
	Namespace    domain.ID
	ReadingID    domain.ID
	ResourceID   domain.ID
	ObservedAt   domain.Millis
	WindowStart  *domain.Millis
	WindowEnd    *domain.Millis
	ResetBasis   string // reported | computed | none
	Limit        *domain.Units
	Used         *domain.Units
	Remaining    *domain.Units // may be negative (arrears)
	RunID        domain.ID
	EvidenceJSON []byte
}

type CollectorRunRecord struct {
	Namespace    domain.ID
	RunID        domain.ID
	CredentialID domain.ID
	Capability   string // inventory | models | verify
	StartedAt    domain.Millis
	FinishedAt   *domain.Millis
	Outcome      string // "" while running; ok | signed_out | error | refused
	Detail       string
}

// SupplyCardRecord is a credential's last read as the page shows it
// (design 15): opaque JSON the source wrote, kept so Ant can show it at
// start. Nothing routes on it.
type SupplyCardRecord struct {
	Namespace    domain.ID
	CredentialID domain.ID
	CardJSON     []byte
	ReadAt       domain.Millis
}

// SupplyRoutePreviewTx reads every durable fact used to qualify a route.
// It is also available on the writer transaction so an order can validate
// the candidate set atomically with saving its preference.
type SupplyRoutePreviewTx interface {
	ListSupplyCredentials(namespace domain.ID) ([]SupplyCredentialRecord, error)
	ListSupplyResources(namespace domain.ID) ([]SupplyResourceRecord, error)
	LatestSupplyReading(namespace, resourceID domain.ID) (SupplyReadingRecord, bool, error)
	ListSupplyPackages(namespace domain.ID) ([]SupplyPackageRecord, error)
	// ListSupplyReadings returns a resource's readings observed at or after
	// since, oldest first.
	ListSupplyReadings(namespace, resourceID domain.ID, since domain.Millis) ([]SupplyReadingRecord, error)
	// ListSupplyOffers returns the offers of a canonical model.
	ListSupplyOffers(namespace domain.ID, modelID string) ([]SupplyOfferRecord, error)
	GetSupplyRouteOrder(namespace domain.ID, modelID string) (SupplyRouteOrderRecord, bool, error)
}

// SupplyReaderTx reads the supply store of one namespace.
type SupplyReaderTx interface {
	ReaderTx
	SupplyRoutePreviewTx
	GetSupplyReading(namespace, readingID domain.ID) (SupplyReadingRecord, bool, error)
	// ListSupplyCards returns every kept card, by credential id.
	ListSupplyCards(namespace domain.ID) ([]SupplyCardRecord, error)
}

// SupplyWriterTx is the supply engine's write surface.
type SupplyWriterTx interface {
	WriterTx
	SupplyRoutePreviewTx
	// EnsureLocalOwner creates the principal that owns the secrets added on
	// the local page, when it doesn't exist yet. Ant has no login (ruling
	// R8): this is an ownership record for secret_refs, nothing more.
	EnsureLocalOwner(principalID domain.ID, displayName string, at domain.Millis) error
	UpsertSupplyCredential(SupplyCredentialRecord) error
	// UpsertSupplyAccount merges by (namespace, provider, provider identity)
	// and returns the account id that holds that identity.
	UpsertSupplyAccount(SupplyAccountRecord) (domain.ID, error)
	UpsertSupplyPackage(SupplyPackageRecord) error
	UpsertSupplyResource(SupplyResourceRecord) error
	StartCollectorRun(CollectorRunRecord) error
	FinishCollectorRun(namespace, runID domain.ID, finishedAt domain.Millis, outcome, detail string) error
	LatestSupplyReading(namespace, resourceID domain.ID) (SupplyReadingRecord, bool, error)
	// AppendSupplyReadingIfChanged appends only when the value differs from
	// the latest reading; appended=false means "checked, unchanged".
	AppendSupplyReadingIfChanged(SupplyReadingRecord) (appended bool, err error)
	// ReplaceSupplyOffers makes offers the credential's whole offer set.
	ReplaceSupplyOffers(namespace, credentialID domain.ID, offers []SupplyOfferRecord) error
	// SetSupplyPaused pauses or resumes credentials' part in routing; it
	// reports how many credentials it changed.
	SetSupplyPaused(namespace domain.ID, credentialIDs []domain.ID, paused bool) (int, error)
	// ReplaceSupplyRouteOrder saves one whole per-model preference with CAS.
	ReplaceSupplyRouteOrder(namespace domain.ID, modelID string, offerIDs []domain.ID, expectedRevision int64, at domain.Millis) (int64, error)
	// PutSupplyCard keeps a credential's card, replacing the last one;
	// DeleteSupplyCard removes it.
	PutSupplyCard(SupplyCardRecord) error
	DeleteSupplyCard(namespace, credentialID domain.ID) error
}
