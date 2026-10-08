package transport

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/ledger"
)

var (
	ErrCredentialUnavailable = errors.New("transport: credential unavailable")
	ErrPermitBinding         = errors.New("transport: permit does not match prepared request")
	ErrPlanBinding           = errors.New("transport: request target or credential does not match execution plan")
)

// PermitConsumer performs the final durable granted→consumed transition. It
// must return only after the ledger commit is known; any ambiguous result
// spends this local request handle and prevents a send or a second attempt.
type PermitConsumer func(context.Context, domain.AuthContext, domain.OwnerToken, ledger.DispatchPermit, string) error

// CredentialResolver is implemented by the secret service. The Transport
// owns this narrow port so provider egress does not depend on secret storage
// or cryptography packages.
type CredentialResolver interface {
	Resolve(context.Context, domain.ID, domain.ID) (map[string]string, error)
}

// ControlledHTTPTransport is the concrete egress implementation. It pins the
// authenticated owner, attempt, credential revision and request digest into a
// runtime-private handle, consumes the durable permit, then performs one
// bounded HTTP/1.1 request with redirects and connection replay disabled.
type ControlledHTTPTransport struct {
	runtimeID domain.ID
	handles   *HandleTable
	client    *HTTPClient
	secrets   CredentialResolver
	consume   PermitConsumer
}

func NewControlledHTTPTransport(runtimeID domain.ID, resolver CredentialResolver, consume PermitConsumer) (*ControlledHTTPTransport, error) {
	return NewControlledHTTPTransportWithClient(runtimeID, resolver, consume, NewHTTPClient())
}

// NewControlledHTTPTransportWithClient composes a runtime over a previously
// validated bounded HTTPClient, for example when a private provider CA is
// explicitly configured. HTTPClient cannot be constructed outside this
// package except through the secure constructors above.
func NewControlledHTTPTransportWithClient(runtimeID domain.ID, resolver CredentialResolver, consume PermitConsumer, client *HTTPClient) (*ControlledHTTPTransport, error) {
	if runtimeID == "" || resolver == nil || consume == nil || client == nil {
		return nil, ErrInvalidRequest
	}
	return &ControlledHTTPTransport{runtimeID: runtimeID, handles: NewHandleTable(), client: client, secrets: resolver, consume: consume}, nil
}

func (t *ControlledHTTPTransport) Prepare(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, encoded adapters.EncodedRequest, plan domain.ExecutionPlan) (PreparedHTTPRequest, error) {
	if t == nil || t.handles == nil || t.client == nil || owner.RuntimeID != t.runtimeID || owner.Key.Namespace == "" || owner.Key.Namespace != auth.Namespace ||
		owner.Key.OperationID == "" || owner.OwnerID == "" || owner.Generation < 1 || attemptID == "" || plan.CredentialRevision == "" || !hasExecute(auth) {
		return PreparedHTTPRequest{}, ErrNotThisRuntime
	}
	if err := t.client.ValidateRequest(encoded); err != nil {
		return PreparedHTTPRequest{}, err
	}
	if encoded.Endpoint != plan.Endpoint || encoded.CredentialRef != plan.CredentialRef {
		return PreparedHTTPRequest{}, ErrPlanBinding
	}
	if encoded.CredentialRef == "" || encoded.CredentialHeader == "" {
		if encoded.CredentialRef != "" || encoded.CredentialHeader != "" || encoded.CredentialField != "" || encoded.CredentialPrefix != "" {
			return PreparedHTTPRequest{}, ErrCredentialUnavailable
		}
		return t.handles.prepare(encoded, plan, nil, auth, owner, attemptID)
	}
	field := encoded.CredentialField
	if field == "" {
		field = "value"
		encoded.CredentialField = field
	}
	values, err := t.secrets.Resolve(ctx, encoded.CredentialRef, plan.CredentialRevision)
	if err != nil {
		return PreparedHTTPRequest{}, ErrCredentialUnavailable
	}
	defer clear(values)
	secret, ok := values[field]
	if !ok || secret == "" || len(secret) > 8192 || strings.ContainsAny(secret, "\r\n") {
		return PreparedHTTPRequest{}, ErrCredentialUnavailable
	}
	credential := []byte(secret)
	return t.handles.prepare(encoded, plan, credential, auth, owner, attemptID)
}

func (t *ControlledHTTPTransport) Discard(handle PreparedHTTPRequest) {
	if t != nil && t.handles != nil {
		t.handles.Discard(handle)
	}
}

func (t *ControlledHTTPTransport) Dispatch(ctx context.Context, permit ledger.DispatchPermit, handle PreparedHTTPRequest) (adapters.RawResponse, error) {
	if t == nil || t.handles == nil || t.client == nil || t.consume == nil || permit.RuntimeID() != t.runtimeID || permit.AttemptID() == "" {
		return adapters.RawResponse{}, ErrNotThisRuntime
	}
	auth, owner, attemptID, revision, ok := t.handles.binding(handle)
	if !ok || attemptID != permit.AttemptID() || owner.RuntimeID != t.runtimeID || revision != handle.CredentialRevision() {
		return adapters.RawResponse{}, ErrPermitBinding
	}
	encoded, err := t.handles.MarkUsed(handle)
	if err != nil {
		return adapters.RawResponse{}, err
	}
	defer eraseEncoded(&encoded)
	if err := t.client.ValidateRequest(encoded); err != nil {
		return adapters.RawResponse{}, err
	}
	if err := t.consume(ctx, auth, owner, permit, handle.RequestDigest()); err != nil {
		return adapters.RawResponse{}, err
	}
	response, err := t.client.DoOnce(ctx, encoded)
	if err != nil {
		return adapters.RawResponse{}, err
	}
	headers := safeResponseHeaders(response.Header)
	return adapters.RawResponse{Status: response.StatusCode, Headers: headers, Body: response.Body}, nil
}

func (t *HandleTable) binding(handle PreparedHTTPRequest) (domain.AuthContext, domain.OwnerToken, domain.ID, domain.ID, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.handles[handle.handleID]
	if !ok || handle.handleID == "" || handle.requestDigest != rec.digest || handle.credentialRevision != rec.credentialRev || rec.attemptID == "" {
		return domain.AuthContext{}, domain.OwnerToken{}, "", "", false
	}
	return cloneAuth(rec.auth), rec.owner, rec.attemptID, rec.credentialRev, true
}

func hasExecute(auth domain.AuthContext) bool {
	for _, permission := range auth.Permissions {
		if permission == domain.PermExecute {
			return auth.PrincipalID != ""
		}
	}
	return false
}

func eraseEncoded(encoded *adapters.EncodedRequest) {
	clear(encoded.Body)
	for key := range encoded.Headers {
		encoded.Headers[key] = ""
	}
	encoded.Headers = nil
}

func safeResponseHeaders(headers http.Header) map[string]string {
	const maxBytes = 16 << 10
	allowed := map[string]bool{
		"Content-Type": true, "Retry-After": true, "X-Request-Id": true,
		"Request-Id": true, "X-Deepseek-Request-Id": true, "Openai-Organization": true,
	}
	out := make(map[string]string)
	used := 0
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if !allowed[canonical] || strings.EqualFold(canonical, "Set-Cookie") || len(values) == 0 {
			continue
		}
		value := strings.Join(values, ",")
		if strings.ContainsAny(value, "\r\n") || used+len(canonical)+len(value) > maxBytes {
			continue
		}
		out[canonical] = value
		used += len(canonical) + len(value)
	}
	return out
}

var _ Port = (*ControlledHTTPTransport)(nil)
