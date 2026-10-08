// Package store defines the persistence port. One implementation exists for
// the first release: internal/store/sqlite over modernc.org/sqlite (single
// host, one write connection, bounded writer queues, independent read pool).
// Every durable change goes through SubmitWrite; no other package runs SQL.
package store

import (
	"context"
	"errors"
	"time"

	"ant/server/internal/domain"
)

var (
	ErrQueueFull          = errors.New("store: writer queue full")   // overload: reject before ownership
	ErrQueueClosed        = errors.New("store: writer queue closed") // shutdown drain
	ErrCommandBudget      = errors.New("store: command exceeded budget")
	ErrUnavailable        = errors.New("store: unavailable") // DB open/lock failure class
	ErrBootstrapConsumed  = errors.New("store: bootstrap credential already consumed")
	ErrProjectionConflict = errors.New("store: active projection changes an immutable resource identity")
)

// WriteCommand is one unit of durable work executed inside a single
// BEGIN IMMEDIATE transaction by the writer goroutine. Control=true commands
// use the reserved control lane (settlement/cancellation priority; the writer
// interleaves at most 8 control items before one normal item to avoid
// starvation, 01-ARCHITECTURE §3).
type WriteCommand struct {
	ID      string // diagnostic identity; idempotency is via command_receipts
	Control bool
	Budget  time.Duration // whole-command budget incl. queue-side retry
	Run     func(tx WriterTx) error
}

// Store is the write/read facade. SubmitWrite blocks until the command is
// committed or failed; after ctx cancellation the caller must check receipts
// instead of assuming rollback (02-STORAGE §3).
type Store interface {
	SubmitWrite(ctx context.Context, cmd WriteCommand) error
	Read(ctx context.Context, fn func(rx ReaderTx) error) error
	Close(ctx context.Context) error
}

// WriterTx: typed transaction surface for T1.3 (T2.2 extends it with
// operation/attempt/reservation/permit commands following the same pattern;
// signatures here must not change).
type WriterTx interface {
	// config revisions / activation pointer (K1 save-read-back)
	InsertConfigRevision(rec ConfigRevisionRecord) error
	GetConfigRevision(revision domain.ID) (ConfigRevisionRecord, bool, error)
	GetActiveConfig() (ActiveConfigRecord, bool, error)
	SetActiveConfig(rec ActiveConfigRecord) error

	// pools / members / K4 full-group reorder primitives
	GetPool(poolID domain.ID) (PoolRecord, bool, error)
	ListPoolMembers(poolID domain.ID) ([]PoolMemberRecord, error)
	GetOffer(offerID domain.ID) (OfferRecord, bool, error)
	ReplacePoolMembers(poolID domain.ID, newVersion int64, members []PoolMemberRecord) error
	// ReplacePoolMembersCAS is the optimistic-version primitive for the K4
	// reorder transaction: applied=false (no write) when the stored version
	// differs from expectedVersion; the caller rolls the whole command back.
	ReplacePoolMembersCAS(poolID domain.ID, expectedVersion, newVersion int64, members []PoolMemberRecord) (applied bool, err error)

	// runtime control / receipts / audit
	GetControlVersion() (controlVersion int64, restoreGuard int, err error)
	BumpControlVersion() (int64, error)
	GetReceipt(scope domain.ID, commandID domain.ID) (ReceiptRecord, bool, error)
	InsertReceipt(rec ReceiptRecord) error
	InsertAudit(rec AuditRecord) error
}

// RestoreGuardWriterTx is an additive runtime-control capability. Updating
// the guard and advancing control_version happen in the caller's write
// transaction so a prepared permit pinned to the previous version cannot be
// consumed after the guard changes.
type RestoreGuardWriterTx interface {
	WriterTx
	SetRestoreGuard(expectedControlVersion int64, enabled bool) (applied bool, err error)
}

// RestoreGuardReaderTx exposes the global send-closure state from the same
// read snapshot used by the caller.
type RestoreGuardReaderTx interface {
	ReaderTx
	GetRestoreGuard() (controlVersion int64, enabled bool, err error)
}

// ReaderTx: consistent read snapshot from the read-only connection pool.
type ReaderTx interface {
	GetConfigRevision(revision domain.ID) (ConfigRevisionRecord, bool, error)
	GetActiveConfig() (ActiveConfigRecord, bool, error)
	GetPool(poolID domain.ID) (PoolRecord, bool, error)
	ListPoolMembers(poolID domain.ID) ([]PoolMemberRecord, error)
	ListPoolsByNamespace(namespace domain.ID) ([]PoolRecord, error)
}

// ReceiptReaderTx is an additive read capability for caller-scoped command
// status queries. The receipt scope must be derived from the authenticated
// principal by the application service.
type ReceiptReaderTx interface {
	ReaderTx
	GetReceipt(scope, commandID domain.ID) (ReceiptRecord, bool, error)
}

// OfferReaderTx is an additive, snapshot-consistent read capability used by
// execution planning to join live pool members to the active config revision.
type OfferReaderTx interface {
	ReaderTx
	GetOffer(offerID domain.ID) (OfferRecord, bool, error)
}

// ConfigDraftWriterTx is the T2 configuration-service extension to the
// frozen T1 WriterTx surface. Draft CAS operations execute inside the same
// BEGIN IMMEDIATE transaction as activation pointer and receipt writes.
type ConfigDraftWriterTx interface {
	GetConfigDraft(draftID domain.ID) (ConfigDraftRecord, bool, error)
	InsertConfigDraft(rec ConfigDraftRecord) error
	UpdateConfigDraftCAS(principalID domain.ID, expectedVersion int64, rec ConfigDraftRecord) (applied bool, err error)
	HasSecretRevision(namespace, ref, revision domain.ID) (bool, error)
}

// SecretWriterTx keeps ownership, sealed bytes, current revision and command
// receipt changes inside the existing serialized SQLite write transaction.
type SecretWriterTx interface {
	GetSecretReference(ref domain.ID) (SecretReferenceRecord, bool, error)
	InsertSecret(reference SecretReferenceRecord, revision SecretRevisionRecord) error
	InsertSecretRevision(revision SecretRevisionRecord) error
	AdvanceSecretRevision(principal, namespace, ref, expectedRevision domain.ID, revision SecretRevisionRecord, updatedAt domain.Millis) (bool, error)
}

type SecretReaderTx interface {
	GetSecretReference(ref domain.ID) (SecretReferenceRecord, bool, error)
	GetSecretRevision(ref, revision domain.ID) (SecretRevisionRecord, bool, error)
}

// ConfigProjectionWriterTx materializes an already validated immutable
// configuration into the live tables used by planning and pool queries. It
// preserves resource ledgers and historical rows; it never deletes them.
type ConfigProjectionWriterTx interface {
	ApplyConfigProjection(ConfigProjection) error
}

type AccountProjectionRecord struct {
	AccountID        domain.ID
	ProviderOwnerRef domain.ID
	Enabled          bool
	DefinitionJSON   []byte
}

type BindingProjectionRecord struct {
	BindingID          domain.ID
	AccountID          domain.ID
	AdapterRef         domain.ID
	Domain             string
	Endpoint           string
	CredentialRef      domain.ID
	CredentialRevision domain.ID
	Enabled            bool
	DefinitionJSON     []byte
	ResourceRefs       []domain.ID
}

type ResourceProjectionRecord struct {
	Definition     domain.ResourceDefinition
	DefinitionJSON []byte
	InitialState   *domain.ResourceSnapshot
	StartMS        *int64
	EndMS          *int64
}

type OfferProjectionRecord struct {
	Offer          OfferRecord
	DefinitionJSON []byte
	ResourceRefs   []domain.ID
}

type PoolProjectionRecord struct {
	Pool           PoolRecord
	DefinitionJSON []byte
	Members        []PoolMemberRecord
}

type ConfigProjection struct {
	Accounts  []AccountProjectionRecord
	Bindings  []BindingProjectionRecord
	Resources []ResourceProjectionRecord
	Offers    []OfferProjectionRecord
	Pools     []PoolProjectionRecord
}

// ConfigDraftReaderTx exposes an owner-checked draft from a consistent read
// snapshot without expanding the frozen T1 ReaderTx surface.
type ConfigDraftReaderTx interface {
	GetConfigDraft(draftID domain.ID) (ConfigDraftRecord, bool, error)
}

// AuthSessionRecord stores only a one-way token hash. Namespace permissions
// are returned from the separately scoped grants table at read time.
type AuthSessionRecord struct {
	TokenHash   string
	PrincipalID domain.ID
	Kind        string
	Namespace   domain.ID
	ExpiresAt   domain.Millis
	CSRFHash    string
	CreatedAt   domain.Millis
	Permissions []domain.Permission
}

// AuthWriterTx is the narrow persistent identity extension used by bootstrap
// and token issuance. It remains separate from the frozen core transaction
// surface so auth schema operations are explicit and reviewable.
type AuthWriterTx interface {
	HasBootstrapAuthSession() (bool, error)
	ListNamespaceGrants(principalID, namespace domain.ID) ([]domain.Permission, error)
	InsertPrincipal(principalID domain.ID, displayName string, createdAt domain.Millis) error
	InsertNamespaceGrant(principalID, namespace domain.ID, permission domain.Permission) error
	InsertAuthSession(rec AuthSessionRecord) error
	ExchangeBootstrap(bootstrapHash string, session AuthSessionRecord, at domain.Millis) error
	RevokeAuthSession(tokenHash string, at domain.Millis) error
}

type AuthReaderTx interface {
	LookupAuthSession(tokenHash string, now domain.Millis) (AuthSessionRecord, bool, error)
}

// Records are persistence models; conversion to/from domain types happens at
// the boundary (ledger / config service), never inside SQL.
type ConfigRevisionRecord struct {
	Revision       domain.ID
	DefinitionJSON []byte
	CreatedAt      domain.Millis
}

type ActiveConfigRecord struct {
	Revision domain.ID
	Version  int64
}

// ConfigDraftRecord is the persistence shape of config_drafts. Definition and
// issues remain JSON text; credentials are represented only by secret refs.
type ConfigDraftRecord struct {
	DraftID        domain.ID
	PrincipalID    domain.ID
	BaseRevision   domain.ID
	Version        int64
	Status         string
	DefinitionJSON []byte
	IssuesJSON     []byte
	CreatedAt      domain.Millis
	UpdatedAt      domain.Millis
}

type SecretReferenceRecord struct {
	Ref             domain.ID
	PrincipalID     domain.ID
	Namespace       domain.ID
	CurrentRevision domain.ID
	Mask            string
	CreatedAt       domain.Millis
	UpdatedAt       domain.Millis
}

type SecretRevisionRecord struct {
	Ref          domain.ID
	Revision     domain.ID
	Nonce        []byte
	Ciphertext   []byte
	MasterKeyRef string
	Mask         string
	CreatedAt    domain.Millis
}

type PoolRecord struct {
	PoolID    domain.ID
	Namespace domain.ID
	Name      string
	Enabled   bool
	Version   int64
}

type PoolMemberRecord struct {
	PoolID   domain.ID
	OfferID  domain.ID
	Priority int64
	Enabled  bool
	Version  int64
}

type OfferRecord struct {
	OfferID           domain.ID
	BindingID         domain.ID
	Domain            string
	CapabilityProfile domain.ID
	Enabled           bool
	Version           int64
}

type ReceiptRecord struct {
	Scope      domain.ID
	CommandID  domain.ID
	Kind       string
	InputHash  string
	ResultJSON []byte
	CreatedAt  domain.Millis
}

type AuditRecord struct {
	AuditID     domain.ID
	PrincipalID domain.ID
	Namespace   domain.ID
	Action      string
	TargetRef   domain.ID
	EvidenceRef domain.ID
	CreatedAt   domain.Millis
}
