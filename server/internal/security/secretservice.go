package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"

	"ant/server/internal/domain"
)

// AUTH-01 (T2.1): concrete credential resolution per the frozen seam in
// secrets.go. Design 05 §5: AES-GCM with a fresh random nonce per record,
// ref+revision bound as associated data, master key held OUTSIDE the
// database; only mask/ref/revision are ever exposed to callers, projections
// and logs — cleartext exists only between Resolve and the Transport's
// request preparation and is never persisted or logged.

var (
	// ErrSecretUnknown covers missing refs and unknown revisions alike:
	// callers cannot probe which revisions exist.
	ErrSecretUnknown = errors.New("security: secret not available")
	// ErrSecretSeal is returned when sealing/opening fails (wrong master
	// key, corrupted record, AAD mismatch). One error for all shapes.
	ErrSecretSeal = errors.New("security: secret seal check failed")
	// ErrSecretInvalid is a malformed write (empty values, oversized key).
	ErrSecretInvalid = errors.New("security: secret write invalid")
	// ErrSecretRevisionExists prevents changing secret material behind an
	// already-issued ref+revision. Rotation must allocate a fresh revision.
	ErrSecretRevisionExists = errors.New("security: secret revision already exists")
)

// SecretRecordStore persists sealed secret bytes keyed by ref+revision for
// cryptography-focused tests and in-process tooling. The production
// application writes directly through the typed transactional store ports so
// ownership, receipts, and ciphertext commit atomically.
// PutSealed must be an atomic insert-only operation and reject an existing
// pair; persistent implementations must use INSERT/no-upsert and map the
// uniqueness conflict to ErrSecretRevisionExists.
type SecretRecordStore interface {
	PutSealed(ref, revision domain.ID, sealed []byte) error
	GetSealed(ref, revision domain.ID) ([]byte, error)
}

// MemorySecretStore is the in-process SecretRecordStore for local
// development and tests. Sealed bytes are held in memory only.
type MemorySecretStore struct {
	mu      sync.RWMutex
	records map[domain.ID]map[domain.ID][]byte
}

// NewMemorySecretStore returns an empty in-process secret store.
func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{records: make(map[domain.ID]map[domain.ID][]byte)}
}

// PutSealed atomically creates a sealed record for ref+revision. Existing
// revisions are immutable; implementations must enforce this in storage.
func (m *MemorySecretStore) PutSealed(ref, revision domain.ID, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	revs, ok := m.records[ref]
	if !ok {
		revs = make(map[domain.ID][]byte)
		m.records[ref] = revs
	}
	if _, exists := revs[revision]; exists {
		return ErrSecretRevisionExists
	}
	revs[revision] = slices.Clone(sealed)
	return nil
}

// GetSealed returns the sealed record for ref+revision.
func (m *MemorySecretStore) GetSealed(ref, revision domain.ID) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	revs, ok := m.records[ref]
	if !ok {
		return nil, ErrSecretUnknown
	}
	sealed, ok := revs[revision]
	if !ok {
		return nil, ErrSecretUnknown
	}
	return slices.Clone(sealed), nil
}

// SecretView is the only shape credential writes ever return and the only
// shape GET/projections may display: reference, revision and a short mask.
// It carries no cleartext by construction.
type SecretView struct {
	Ref      domain.ID `json:"ref"`
	Revision domain.ID `json:"revision"`
	Mask     string    `json:"mask"`
}

// SecretCleartext wraps resolved credential material between Resolve and
// Transport preparation. Its String and LogValue methods are always
// "[REDACTED]", so accidental formatting or structured logging of the value
// cannot leak contents (AUTH-01 acceptance: logs/GET/frontend never expose
// secret values).
type SecretCleartext struct {
	values map[string]string
}

// NewSecretCleartext copies the map defensively.
func NewSecretCleartext(values map[string]string) SecretCleartext {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	return SecretCleartext{values: out}
}

// Get returns one credential field.
func (s SecretCleartext) Get(key string) (string, bool) {
	v, ok := s.values[key]
	return v, ok
}

// String never reveals contents.
func (s SecretCleartext) String() string { return "[REDACTED]" }

// GoString prevents the %#v formatting verb from exposing the private map.
func (s SecretCleartext) GoString() string { return "security.SecretCleartext{[REDACTED]}" }

// LogValue makes slog drop the contents in any structured log record.
func (s SecretCleartext) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// SecretService seals and resolves credential material with AES-256-GCM.
type SecretService struct {
	aead  cipher.AEAD
	store SecretRecordStore
}

// SealedSecret is a persistence-ready AES-GCM value. The nonce is stored in
// its own database column; cleartext is never part of this type.
type SealedSecret struct {
	Nonce      []byte
	Ciphertext []byte
	Mask       string
}

// NewSecretService builds a service over a 32-byte master key that the
// caller loaded from outside the database (design 05 §5: master key file
// 0600 / system key service; never stored with the records).
func NewSecretService(masterKey []byte, store SecretRecordStore) (*SecretService, error) {
	if len(masterKey) != 32 || store == nil {
		return nil, ErrSecretInvalid
	}
	service, err := NewSecretCipher(masterKey)
	if err != nil {
		return nil, err
	}
	service.store = store
	return service, nil
}

// NewSecretCipher builds the cryptographic half for transactional repository
// adapters. The application owns the storage transaction and never calls the
// convenience Write/Resolve methods that require SecretRecordStore.
func NewSecretCipher(masterKey []byte) (*SecretService, error) {
	if len(masterKey) != 32 {
		return nil, ErrSecretInvalid
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, ErrSecretInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrSecretInvalid
	}
	return &SecretService{aead: aead}, nil
}

// additionalData binds every sealed record to its exact ref and revision:
// a record sealed for one revision can never be opened under another, and
// swapping records between refs fails the seal check.
func additionalData(ref, revision domain.ID) []byte {
	buf := make([]byte, 0, len("ant:secret:v1")+len(ref)+len(revision)+16)
	buf = append(buf, "ant:secret:v1"...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(ref)))
	buf = append(buf, ref...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(revision)))
	buf = append(buf, revision...)
	return buf
}

// Write seals credential values under a fresh ref+revision and returns the
// view (mask/ref/revision) that is the ONLY record of the write outside the
// sealed store. A revision cannot be reused for rotation or replacement.
// Values are stored encrypted; the master key and plaintext never reach the
// store or the return value.
func (s *SecretService) Write(ref, revision domain.ID, values map[string]string) (SecretView, error) {
	if s.store == nil {
		return SecretView{}, ErrSecretInvalid
	}
	sealed, err := s.Seal(ref, revision, values)
	if err != nil {
		return SecretView{}, err
	}
	stored := append(append([]byte(nil), sealed.Nonce...), sealed.Ciphertext...)
	if err := s.store.PutSealed(ref, revision, stored); err != nil {
		return SecretView{}, err
	}
	return SecretView{Ref: ref, Revision: revision, Mask: sealed.Mask}, nil
}

// Seal encrypts values for atomic persistence in a caller-managed transaction.
// AAD binds the ciphertext to the exact generated reference and revision.
func (s *SecretService) Seal(ref, revision domain.ID, values map[string]string) (SealedSecret, error) {
	if ref == "" || revision == "" || len(values) == 0 {
		return SealedSecret{}, ErrSecretInvalid
	}
	for k, v := range values {
		if k == "" {
			return SealedSecret{}, ErrSecretInvalid
		}
		if len(v) > 1<<20 { // 1MiB per field ceiling; real keys are tiny.
			return SealedSecret{}, ErrSecretInvalid
		}
	}
	plaintext := encodeValues(values)
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return SealedSecret{}, fmt.Errorf("%w: nonce unavailable", ErrSecretSeal)
	}
	ciphertext := s.aead.Seal(nil, nonce, plaintext, additionalData(ref, revision))
	return SealedSecret{Nonce: nonce, Ciphertext: ciphertext, Mask: MaskValue(values)}, nil
}

// Open decrypts one persistence record only for the execution resolver.
func (s *SecretService) Open(ref, revision domain.ID, nonce, ciphertext []byte) (map[string]string, error) {
	if len(nonce) != s.aead.NonceSize() {
		return nil, ErrSecretSeal
	}
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, additionalData(ref, revision))
	if err != nil {
		return nil, ErrSecretSeal
	}
	return decodeValues(plaintext)
}

// Resolve implements the frozen SecretResolver port: it returns the
// cleartext for Transport preparation only. The result is a fresh copy per
// call; mutating it never affects stored records or other callers.
func (s *SecretService) Resolve(ctx context.Context, ref domain.ID, revision domain.ID) (map[string]string, error) {
	if s.store == nil {
		return nil, ErrSecretUnknown
	}
	sealed, err := s.store.GetSealed(ref, revision)
	if err != nil {
		if errors.Is(err, ErrSecretUnknown) {
			return nil, ErrSecretUnknown
		}
		return nil, err
	}
	if len(sealed) < s.aead.NonceSize() {
		return nil, ErrSecretSeal
	}
	return s.Open(ref, revision, sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():])
}

// MaskValue derives the display mask from the stored values: the last four
// characters of the lexicographically-first field's value, or "****" when
// the value is too short to mask safely. Masks are lossy by design and
// must never be treated as authentication or equality evidence. (Display
// rule decided in this implementation; CFG-08 may adjust presentation but
// not widen it.)
func MaskValue(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	v := values[keys[0]]
	const minMaskable = 8
	if len(v) < minMaskable {
		return "****"
	}
	return "…" + v[len(v)-4:]
}

// encodeValues serialises credential fields: count-prefixed key/value
// pairs, deterministic ordering, no escaping ambiguity.
func encodeValues(values map[string]string) []byte {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := make([]byte, 0, 64)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(values)))
	for _, k := range keys {
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(k)))
		buf = append(buf, k...)
		v := values[k]
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(v)))
		buf = append(buf, v...)
	}
	return buf
}

// decodeValues is the inverse of encodeValues; malformed input is a seal
// failure (only records we sealed ourselves can decode).
func decodeValues(plaintext []byte) (map[string]string, error) {
	if len(plaintext) < 4 {
		return nil, ErrSecretSeal
	}
	n := binary.BigEndian.Uint32(plaintext)
	plaintext = plaintext[4:]
	out := make(map[string]string, n)
	for i := uint32(0); i < n; i++ {
		if len(plaintext) < 4 {
			return nil, ErrSecretSeal
		}
		klen := binary.BigEndian.Uint32(plaintext)
		plaintext = plaintext[4:]
		if uint32(len(plaintext)) < klen {
			return nil, ErrSecretSeal
		}
		k := string(plaintext[:klen])
		plaintext = plaintext[klen:]
		if len(plaintext) < 4 {
			return nil, ErrSecretSeal
		}
		vlen := binary.BigEndian.Uint32(plaintext)
		plaintext = plaintext[4:]
		if uint32(len(plaintext)) < vlen {
			return nil, ErrSecretSeal
		}
		v := string(plaintext[:vlen])
		plaintext = plaintext[vlen:]
		out[k] = v
	}
	if len(plaintext) != 0 {
		return nil, ErrSecretSeal
	}
	return out, nil
}
