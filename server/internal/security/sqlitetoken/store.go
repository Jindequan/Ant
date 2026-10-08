// Package sqlitetoken adapts the typed SQLite auth-session port to security's
// token interfaces. Cleartext bearer tokens never enter the store.
package sqlitetoken

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"

	"ant/server/internal/domain"
	"ant/server/internal/security"
	"ant/server/internal/store"
)

var (
	ErrInvalidRecord        = errors.New("token store: invalid record")
	ErrBootstrapInitialized = errors.New("token store: bootstrap already initialized")
	ErrGrantMismatch        = errors.New("token store: requested permissions do not match principal grants")
)

type Clock interface{ Now() domain.Millis }

type Store struct {
	store store.Store
	clock Clock
}

func New(st store.Store, clock Clock) (*Store, error) {
	if st == nil || clock == nil {
		return nil, ErrInvalidRecord
	}
	return &Store{store: st, clock: clock}, nil
}

func validRecord(record security.TokenRecord) bool {
	if record.PrincipalID == "" || record.Namespace == "" || len(record.Permissions) == 0 {
		return false
	}
	for _, permission := range record.Permissions {
		switch permission {
		case domain.PermExecute, domain.PermRead, domain.PermCancel, domain.PermManage, domain.PermConfigure:
		default:
			return false
		}
	}
	return true
}

func hashText(hash security.TokenHash) string { return hex.EncodeToString(hash[:]) }

func (s *Store) Put(hash security.TokenHash, record security.TokenRecord) error {
	if s == nil || record.Kind == security.TokenKindBootstrap || !validRecord(record) {
		return ErrInvalidRecord
	}
	expiresAt := record.ExpiresAt
	if expiresAt == 0 {
		expiresAt = domain.Millis(1<<63 - 1)
	}
	if expiresAt <= s.clock.Now() || (record.Kind == security.TokenKindSession && record.CSRFHash == "") ||
		(record.Kind != security.TokenKindSession && record.CSRFHash != "") {
		return ErrInvalidRecord
	}
	return s.store.SubmitWrite(context.Background(), store.WriteCommand{ID: "auth-token-put", Run: func(tx store.WriterTx) error {
		auth, ok := tx.(store.AuthWriterTx)
		if !ok {
			return ErrInvalidRecord
		}
		grants, err := auth.ListNamespaceGrants(record.PrincipalID, record.Namespace)
		if err != nil {
			return err
		}
		if !samePermissions(grants, record.Permissions) {
			return ErrGrantMismatch
		}
		return auth.InsertAuthSession(store.AuthSessionRecord{
			TokenHash: hashText(hash), PrincipalID: record.PrincipalID, Kind: string(record.Kind), Namespace: record.Namespace,
			ExpiresAt: expiresAt, CSRFHash: record.CSRFHash, CreatedAt: s.clock.Now(),
		})
	}})
}

func (s *Store) PutBootstrapIfAbsent(hash security.TokenHash, record security.TokenRecord) error {
	if s == nil || record.Kind != security.TokenKindBootstrap || !validRecord(record) {
		return ErrInvalidRecord
	}
	return s.store.SubmitWrite(context.Background(), store.WriteCommand{ID: "auth-bootstrap-initialize", Run: func(tx store.WriterTx) error {
		auth, ok := tx.(store.AuthWriterTx)
		if !ok {
			return ErrInvalidRecord
		}
		used, err := auth.HasBootstrapAuthSession()
		if err != nil {
			return err
		}
		if used {
			return ErrBootstrapInitialized
		}
		now := s.clock.Now()
		if err := auth.InsertPrincipal(record.PrincipalID, "ANT administrator", now); err != nil {
			return err
		}
		permissions := slices.Clone(record.Permissions)
		slices.Sort(permissions)
		for i, permission := range permissions {
			if i > 0 && permissions[i-1] == permission {
				return ErrInvalidRecord
			}
			if err := auth.InsertNamespaceGrant(record.PrincipalID, record.Namespace, permission); err != nil {
				return err
			}
		}
		return auth.InsertAuthSession(store.AuthSessionRecord{
			TokenHash: hashText(hash), PrincipalID: record.PrincipalID, Kind: string(record.Kind), Namespace: record.Namespace,
			ExpiresAt: domain.Millis(1<<63 - 1), CreatedAt: now,
		})
	}})
}

func (s *Store) ExchangeBootstrap(bootstrapHash, sessionHash security.TokenHash, record security.TokenRecord) error {
	if s == nil || record.Kind != security.TokenKindSession || !validRecord(record) || record.CSRFHash == "" || record.ExpiresAt <= s.clock.Now() {
		return ErrInvalidRecord
	}
	err := s.store.SubmitWrite(context.Background(), store.WriteCommand{ID: "auth-bootstrap-exchange", Run: func(tx store.WriterTx) error {
		auth, ok := tx.(store.AuthWriterTx)
		if !ok {
			return ErrInvalidRecord
		}
		grants, err := auth.ListNamespaceGrants(record.PrincipalID, record.Namespace)
		if err != nil {
			return err
		}
		if !samePermissions(grants, record.Permissions) {
			return ErrGrantMismatch
		}
		now := s.clock.Now()
		return auth.ExchangeBootstrap(hashText(bootstrapHash), store.AuthSessionRecord{
			TokenHash: hashText(sessionHash), PrincipalID: record.PrincipalID, Kind: string(record.Kind),
			Namespace: record.Namespace, ExpiresAt: record.ExpiresAt, CSRFHash: record.CSRFHash, CreatedAt: now,
		}, now)
	}})
	if errors.Is(err, store.ErrBootstrapConsumed) {
		return security.ErrUnauthenticated
	}
	return err
}

func (s *Store) Lookup(hash security.TokenHash) (security.TokenRecord, bool, error) {
	if s == nil {
		return security.TokenRecord{}, false, ErrInvalidRecord
	}
	var record store.AuthSessionRecord
	err := s.store.Read(context.Background(), func(tx store.ReaderTx) error {
		auth, ok := tx.(store.AuthReaderTx)
		if !ok {
			return ErrInvalidRecord
		}
		var found bool
		var err error
		record, found, err = auth.LookupAuthSession(hashText(hash), s.clock.Now())
		if !found && err == nil {
			record = store.AuthSessionRecord{}
		}
		return err
	})
	if err != nil || record.TokenHash == "" {
		return security.TokenRecord{}, false, err
	}
	out := security.TokenRecord{PrincipalID: record.PrincipalID, Namespace: record.Namespace,
		Permissions: slices.Clone(record.Permissions), Kind: security.TokenKind(record.Kind), ExpiresAt: record.ExpiresAt, CSRFHash: record.CSRFHash}
	if !validRecord(out) {
		return security.TokenRecord{}, false, nil
	}
	return out, true, nil
}

func (s *Store) Revoke(hash security.TokenHash) error {
	if s == nil {
		return ErrInvalidRecord
	}
	return s.store.SubmitWrite(context.Background(), store.WriteCommand{ID: "auth-token-revoke", Run: func(tx store.WriterTx) error {
		auth, ok := tx.(store.AuthWriterTx)
		if !ok {
			return ErrInvalidRecord
		}
		return auth.RevokeAuthSession(hashText(hash), s.clock.Now())
	}})
}

func samePermissions(left, right []domain.Permission) bool {
	a, b := slices.Clone(left), slices.Clone(right)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

var _ security.TokenStore = (*Store)(nil)
