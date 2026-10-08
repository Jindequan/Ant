// Package security defines the credential-resolution and identity ports.
// Concrete session handling, AES-GCM secret records and the bootstrap CLI are
// delivered with T2.1; this file freezes the seam other packages compile
// against. Secret values never appear in logs, configs or projections.
package security

import (
	"context"

	"ant/server/internal/domain"
)

// SecretResolver resolves a credential reference + revision to its key/value
// material. Implemented over secret_records in T2.1; the Transport uses it
// only inside Dispatch preparation and never persists the result.
type SecretResolver interface {
	Resolve(ctx context.Context, ref domain.ID, revision domain.ID) (map[string]string, error)
}

// Principal is the authenticated identity produced by the auth middleware.
type Principal struct {
	PrincipalID domain.ID
	Namespace   domain.ID
	Permissions []domain.Permission
	Kind        TokenKind
	ExpiresAt   domain.Millis
	CSRFHash    string
}
