// Package transport owns the single controlled egress path: permit handles
// issued by the ledger, request preparation, credential injection, one-shot
// local use, durable permit consumption, and bounded HTTP sends. Hidden
// retries, redirects, and credential swapping are rejected (DESIGN §2.1/§7.3).
package transport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/ledger"
)

var (
	ErrHandleUnknown  = errors.New("transport: prepared request handle unknown")
	ErrHandleUsed     = errors.New("transport: prepared request already used")
	ErrNotThisRuntime = errors.New("transport: permit not issued by this runtime")
	ErrDigestMismatch = errors.New("transport: request digest mismatch")
)

// PreparedHTTPRequest carries only opaque identity. The encoded request bytes
// and resolved credential stay inside the handle table, never on the wire
// value (K2).
type PreparedHTTPRequest struct {
	handleID           domain.ID
	requestDigest      string
	credentialRevision domain.ID
}

// Accessors are the controlled read path shared with the ledger assembly:
// non-zero handle creation, same attempt/digest read-back, forgery rejection.
func (h PreparedHTTPRequest) ID() domain.ID                 { return h.handleID }
func (h PreparedHTTPRequest) RequestDigest() string         { return h.requestDigest }
func (h PreparedHTTPRequest) CredentialRevision() domain.ID { return h.credentialRevision }

type handleRecord struct {
	digest        string
	credentialRev domain.ID
	credential    []byte
	auth          domain.AuthContext
	owner         domain.OwnerToken
	attemptID     domain.ID
	encoded       adapters.EncodedRequest
	used          bool
}

// HandleTable is the runtime-private registry for prepared requests. Prepare
// registers a copy; MarkUsed flips the local one-shot marker exactly once,
// after which the ledger writer performs the durable permit CAS (03-EXECUTION
// §4). A reply lost after marking converges conservatively — never a second
// send from this handle. All access is mutex-guarded: Router/ExecutionHost
// call these concurrently across operations.
type HandleTable struct {
	mu      sync.Mutex
	handles map[domain.ID]*handleRecord
}

func NewHandleTable() *HandleTable {
	return &HandleTable{handles: map[domain.ID]*handleRecord{}}
}

func randomToken() domain.ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("transport: entropy unavailable: " + err.Error())
	}
	return domain.ID(hex.EncodeToString(b[:]))
}

// Prepare copies the encoded request (defensive copy of body and headers) and
// registers an opaque handle bound to the plan's credential revision.
func (t *HandleTable) Prepare(encoded adapters.EncodedRequest, plan domain.ExecutionPlan) (PreparedHTTPRequest, error) {
	return t.prepare(encoded, plan, nil, domain.AuthContext{}, domain.OwnerToken{}, "")
}

func (t *HandleTable) prepare(encoded adapters.EncodedRequest, plan domain.ExecutionPlan, credential []byte, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID) (PreparedHTTPRequest, error) {
	if encoded.Endpoint == "" || encoded.Method == "" {
		return PreparedHTTPRequest{}, errors.New("transport: endpoint and method required")
	}
	if encoded.CredentialHeader != "" {
		if encoded.CredentialRef == "" || len(credential) == 0 || len(credential) > 8192 || strings.ContainsAny(string(credential), "\r\n") ||
			len(encoded.CredentialPrefix) > 128 || strings.ContainsAny(encoded.CredentialPrefix, "\r\n") || http.CanonicalHeaderKey(encoded.CredentialHeader) == "" {
			return PreparedHTTPRequest{}, ErrInvalidRequest
		}
		for key := range encoded.Headers {
			if strings.EqualFold(key, encoded.CredentialHeader) {
				return PreparedHTTPRequest{}, ErrInvalidRequest
			}
		}
	} else if len(credential) > 0 || encoded.CredentialField != "" || encoded.CredentialPrefix != "" {
		return PreparedHTTPRequest{}, ErrInvalidRequest
	}
	id := randomToken()
	bodyCopy := append([]byte(nil), encoded.Body...)
	rec := &handleRecord{
		digest:        DigestRequest(encoded),
		credentialRev: plan.CredentialRevision,
		credential:    append([]byte(nil), credential...),
		auth:          cloneAuth(auth),
		owner:         owner,
		attemptID:     attemptID,
		encoded: adapters.EncodedRequest{
			Endpoint:         encoded.Endpoint,
			Method:           encoded.Method,
			Headers:          copyHeaders(encoded.Headers),
			Body:             bodyCopy,
			CredentialRef:    encoded.CredentialRef,
			CredentialHeader: encoded.CredentialHeader,
			CredentialField:  encoded.CredentialField,
			CredentialPrefix: encoded.CredentialPrefix,
			MaxResponseBytes: encoded.MaxResponseBytes,
		},
	}
	t.mu.Lock()
	t.handles[id] = rec
	t.mu.Unlock()
	return PreparedHTTPRequest{
		handleID:           id,
		requestDigest:      rec.digest,
		credentialRevision: plan.CredentialRevision,
	}, nil
}

// Discard drops a prepared request that will not be dispatched.
func (t *HandleTable) Discard(h PreparedHTTPRequest) {
	t.mu.Lock()
	if rec := t.handles[h.handleID]; rec != nil {
		clear(rec.credential)
	}
	delete(t.handles, h.handleID)
	t.mu.Unlock()
}

// MarkUsed atomically flips the local one-shot marker and returns the stored
// encoded request exactly once; second use and forged handles are rejected.
// The whole check-and-flip runs under one critical section so two concurrent
// dispatches of the same handle cannot both win. Byte ownership (R07): the
// returned value is a defensive copy — callers cannot mutate the bytes other
// modules validate against, and the one-shot marker prevents a second read.
func (t *HandleTable) MarkUsed(h PreparedHTTPRequest) (adapters.EncodedRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.handles[h.handleID]
	if !ok || h.handleID == "" || h.requestDigest == "" {
		return adapters.EncodedRequest{}, ErrHandleUnknown
	}
	if h.requestDigest != rec.digest || h.credentialRevision != rec.credentialRev {
		return adapters.EncodedRequest{}, ErrDigestMismatch
	}
	if rec.used {
		return adapters.EncodedRequest{}, ErrHandleUsed
	}
	rec.used = true
	headers := copyHeaders(rec.encoded.Headers)
	if rec.encoded.CredentialHeader != "" {
		if len(rec.credential) == 0 {
			return adapters.EncodedRequest{}, ErrHandleUnknown
		}
		headers[rec.encoded.CredentialHeader] = rec.encoded.CredentialPrefix + string(rec.credential)
		clear(rec.credential)
	}
	encoded := adapters.EncodedRequest{
		Endpoint:         rec.encoded.Endpoint,
		Method:           rec.encoded.Method,
		Headers:          headers,
		Body:             append([]byte(nil), rec.encoded.Body...),
		CredentialRef:    rec.encoded.CredentialRef,
		CredentialHeader: rec.encoded.CredentialHeader,
		CredentialField:  rec.encoded.CredentialField,
		CredentialPrefix: rec.encoded.CredentialPrefix,
		MaxResponseBytes: rec.encoded.MaxResponseBytes,
	}
	clear(rec.encoded.Body)
	rec.encoded.Body = nil
	clear(rec.encoded.Headers)
	rec.encoded.Headers = nil
	return encoded, nil
}

// DigestRequest covers method, endpoint, sendable headers and body — the
// value the permit binds to (05-RUNTIME §2; secrets are never part of it).
func DigestRequest(encoded adapters.EncodedRequest) string {
	h := sha256.New()
	h.Write([]byte("ant:req:v1\n"))
	h.Write([]byte(encoded.Method))
	h.Write([]byte{0})
	h.Write([]byte(encoded.Endpoint))
	h.Write([]byte{0})
	h.Write([]byte(encoded.CredentialRef))
	h.Write([]byte{0})
	h.Write([]byte(encoded.CredentialHeader))
	h.Write([]byte{0})
	h.Write([]byte(encoded.CredentialField))
	h.Write([]byte{0})
	h.Write([]byte(encoded.CredentialPrefix))
	h.Write([]byte{0})
	for _, k := range sortedHeaderKeys(encoded.Headers) {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(encoded.Headers[k]))
		h.Write([]byte{0})
	}
	h.Write([]byte{0})
	h.Write(encoded.Body)
	return hex.EncodeToString(h.Sum(nil))
}

func sortedHeaderKeys(in map[string]string) []string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Port is the consumer-side interface (Router, T2.3); it matches the shape
// frozen in docs/design/contracts.go. Dispatch consumes the permit and
// performs the single send; the signature is fixed so T2.3 cannot widen it
// into a retrying primitive.
type Port interface {
	Prepare(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID, adapters.EncodedRequest, domain.ExecutionPlan) (PreparedHTTPRequest, error)
	Discard(PreparedHTTPRequest)
	Dispatch(context.Context, ledger.DispatchPermit, PreparedHTTPRequest) (adapters.RawResponse, error)
}

var _ = PreparedHTTPRequest{}

func copyHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAuth(in domain.AuthContext) domain.AuthContext {
	in.Permissions = append([]domain.Permission(nil), in.Permissions...)
	return in
}
