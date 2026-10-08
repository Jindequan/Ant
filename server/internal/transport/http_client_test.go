package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/ledger"
)

type secretResolverFunc func(context.Context, domain.ID, domain.ID) (map[string]string, error)

func (f secretResolverFunc) Resolve(ctx context.Context, ref, revision domain.ID) (map[string]string, error) {
	return f(ctx, ref, revision)
}

func TestDoOnceDoesNotFollowRedirectAndSendsOneRequest(t *testing.T) {
	var source, destination atomic.Int32
	dst := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destination.Add(1) }))
	defer dst.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source.Add(1)
		http.Redirect(w, r, dst.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	resp, err := newHTTPClient(true).DoOnce(context.Background(), adapters.EncodedRequest{
		Endpoint: srv.URL, Method: http.MethodPost, Body: []byte("payload"), MaxResponseBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirect status was followed or lost: %d", resp.StatusCode)
	}
	if source.Load() != 1 || destination.Load() != 0 {
		t.Fatalf("unexpected sends: source=%d destination=%d", source.Load(), destination.Load())
	}
}

func TestDoOnceBoundsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "12345") }))
	defer srv.Close()
	resp, err := newHTTPClient(true).DoOnce(context.Background(), adapters.EncodedRequest{
		Endpoint: srv.URL, Method: http.MethodGet, MaxResponseBytes: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("expected bounded read error, got data=%q err=%v", data, err)
	}
	if len(data) > 4 {
		t.Fatalf("read beyond response bound: %d bytes", len(data))
	}
}

func TestProductionClientRequiresHTTPS(t *testing.T) {
	_, err := NewHTTPClient().DoOnce(context.Background(), adapters.EncodedRequest{
		Endpoint: "http://127.0.0.1/resource", Method: http.MethodGet, MaxResponseBytes: 10,
	})
	if !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("expected HTTP endpoint rejection, got %v", err)
	}
}

func TestProductionProviderClientAllowsOnlyExactRegisteredURLs(t *testing.T) {
	const allowed = "https://api.deepseek.com/user/balance"
	client, err := NewHTTPClientWithAllowedEndpoints(allowed)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateRequest(adapters.EncodedRequest{Endpoint: allowed, Method: http.MethodGet, MaxResponseBytes: 1024}); err != nil {
		t.Fatalf("registered provider URL rejected: %v", err)
	}
	for _, endpoint := range []string{
		"https://api.deepseek.com/user/other",
		"https://api.deepseek.com/user/balance?redirect=1",
		"https://api.example.com/user/balance",
		"http://api.deepseek.com/user/balance",
	} {
		if err := client.ValidateRequest(adapters.EncodedRequest{Endpoint: endpoint, Method: http.MethodGet, MaxResponseBytes: 1024}); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("unregistered provider URL %q accepted: %v", endpoint, err)
		}
	}
	if _, err := NewHTTPClientWithAllowedEndpoints(allowed, allowed); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate provider URL error = %v", err)
	}
}

func TestLocalTestClientPinsTheSimulatedProviderPathAndServerPort(t *testing.T) {
	client, err := NewLocalTestHTTPClient("8080")
	if err != nil {
		t.Fatal(err)
	}
	allowed := adapters.EncodedRequest{Endpoint: "http://127.0.0.1:8080/v1/localtest/chat/completions", Method: http.MethodPost, MaxResponseBytes: 32}
	if err := client.ValidateRequest(allowed); err != nil {
		t.Fatalf("fixture endpoint rejected: %v", err)
	}
	for _, endpoint := range []string{
		"http://127.0.0.1:8081/v1/localtest/chat/completions",
		"http://127.0.0.1:8080/other",
		"http://192.0.2.1:8080/v1/localtest/chat/completions",
		"https://127.0.0.1:8080/v1/localtest/chat/completions",
	} {
		request := allowed
		request.Endpoint = endpoint
		if err := client.ValidateRequest(request); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("unsafe fixture target %q accepted: %v", endpoint, err)
		}
	}
	if _, err := NewLocalTestHTTPClient("0"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ephemeral port must be rejected: %v", err)
	}
}

func TestMixedLocalTestClientAllowsOnlyLoopbackFixtureAndExactProviderURLs(t *testing.T) {
	const providerEndpoint = "https://api.deepseek.com/chat/completions"
	client, err := NewLocalTestHTTPClientWithAllowedEndpoints("8080", providerEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{
		"http://127.0.0.1:8080/v1/localtest/chat/completions",
		providerEndpoint,
	} {
		if err := client.ValidateRequest(adapters.EncodedRequest{Endpoint: endpoint, Method: http.MethodPost, MaxResponseBytes: 32}); err != nil {
			t.Errorf("registered endpoint %q rejected: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{
		"http://127.0.0.1:8081/v1/localtest/chat/completions",
		"http://127.0.0.1:8080/other",
		"https://api.deepseek.com/other",
		"https://api.example.com/chat/completions",
		"https://user:pass@api.deepseek.com/chat/completions",
		"https://api.deepseek.com/chat/completions?redirect=1",
	} {
		if err := client.ValidateRequest(adapters.EncodedRequest{Endpoint: endpoint, Method: http.MethodPost, MaxResponseBytes: 32}); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("unregistered endpoint %q accepted: %v", endpoint, err)
		}
	}
	if _, err := NewLocalTestHTTPClientWithAllowedEndpoints("8080", "http://127.0.0.1:8080/public"); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("plain HTTP provider allowlist endpoint must be rejected: %v", err)
	}
}

func TestControlledPreparePinsAndInjectsCredentialWithoutDigestLeak(t *testing.T) {
	var resolved atomic.Int32
	const token = "local-only-token-value"
	transport, err := NewControlledHTTPTransport("runtime-1", secretResolverFunc(func(_ context.Context, ref, revision domain.ID) (map[string]string, error) {
		if ref != "secret-1" || revision != "revision-1" {
			t.Fatalf("resolved unexpected credential ref/revision: %q/%q", ref, revision)
		}
		resolved.Add(1)
		return map[string]string{"value": token}, nil
	}), func(context.Context, domain.AuthContext, domain.OwnerToken, ledger.DispatchPermit, string) error {
		t.Fatal("permit consumption must happen only during Dispatch")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	auth := domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermExecute}}
	owner := domain.OwnerToken{Key: domain.OperationKey{Namespace: "ns-1", OperationID: "operation-1"}, RuntimeID: "runtime-1", OwnerID: "owner-1", Generation: 1}
	handle, err := transport.Prepare(context.Background(), auth, owner, "attempt-1", adapters.EncodedRequest{
		Endpoint: "https://api.example.invalid/v1/chat/completions", Method: http.MethodPost,
		Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"prompt":"local"}`),
		CredentialRef: "secret-1", CredentialHeader: "Authorization", CredentialPrefix: "Bearer ", MaxResponseBytes: 1024,
	}, domain.ExecutionPlan{Endpoint: "https://api.example.invalid/v1/chat/completions", CredentialRef: "secret-1", CredentialRevision: "revision-1"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Load() != 1 || strings.Contains(handle.RequestDigest(), token) {
		t.Fatal("credential was not resolved once or leaked into the request digest")
	}
	encoded, err := transport.handles.MarkUsed(handle)
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Headers["Authorization"] != "Bearer "+token || string(encoded.Body) != `{"prompt":"local"}` {
		t.Fatalf("prepared request did not inject the pinned credential: headers=%v body=%s", encoded.Headers, encoded.Body)
	}
	if _, err := transport.handles.MarkUsed(handle); !errors.Is(err, ErrHandleUsed) {
		t.Fatalf("handle reuse error = %v", err)
	}
	transport.Discard(handle)
}

func TestControlledPrepareRejectsInvalidRequestBeforeResolvingSecret(t *testing.T) {
	var resolved atomic.Int32
	transport, err := NewControlledHTTPTransport("runtime-1", secretResolverFunc(func(context.Context, domain.ID, domain.ID) (map[string]string, error) {
		resolved.Add(1)
		return map[string]string{"value": "secret"}, nil
	}), func(context.Context, domain.AuthContext, domain.OwnerToken, ledger.DispatchPermit, string) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.OwnerToken{Key: domain.OperationKey{Namespace: "ns-1", OperationID: "op-1"}, RuntimeID: "runtime-1", OwnerID: "owner-1", Generation: 1}
	_, err = transport.Prepare(context.Background(), domain.AuthContext{PrincipalID: "p", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermExecute}}, owner, "attempt-1", adapters.EncodedRequest{
		Endpoint: "http://outside.invalid", Method: http.MethodPost, CredentialRef: "secret-1", CredentialHeader: "Authorization", MaxResponseBytes: 100,
	}, domain.ExecutionPlan{Endpoint: "http://outside.invalid", CredentialRef: "secret-1", CredentialRevision: "revision-1"})
	if !errors.Is(err, ErrInvalidEndpoint) || resolved.Load() != 0 {
		t.Fatalf("invalid request was not rejected before secret access: err=%v resolves=%d", err, resolved.Load())
	}
}

func TestControlledPrepareRejectsTargetOrCredentialSwapBeforeResolvingSecret(t *testing.T) {
	var resolved atomic.Int32
	transport, err := NewControlledHTTPTransport("runtime-1", secretResolverFunc(func(context.Context, domain.ID, domain.ID) (map[string]string, error) {
		resolved.Add(1)
		return map[string]string{"value": "secret"}, nil
	}), func(context.Context, domain.AuthContext, domain.OwnerToken, ledger.DispatchPermit, string) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.OwnerToken{Key: domain.OperationKey{Namespace: "ns-1", OperationID: "op-1"}, RuntimeID: "runtime-1", OwnerID: "owner-1", Generation: 1}
	auth := domain.AuthContext{PrincipalID: "p", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermExecute}}
	for _, tc := range []struct {
		name string
		plan domain.ExecutionPlan
	}{
		{name: "endpoint swap", plan: domain.ExecutionPlan{Endpoint: "https://other.example/v1", CredentialRef: "secret-1", CredentialRevision: "revision-1"}},
		{name: "credential swap", plan: domain.ExecutionPlan{Endpoint: "https://api.example.invalid/v1/chat/completions", CredentialRef: "other-secret", CredentialRevision: "revision-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transport.Prepare(context.Background(), auth, owner, "attempt-1", adapters.EncodedRequest{
				Endpoint: "https://api.example.invalid/v1/chat/completions", Method: http.MethodPost,
				CredentialRef: "secret-1", CredentialHeader: "Authorization", MaxResponseBytes: 100,
			}, tc.plan)
			if !errors.Is(err, ErrPlanBinding) {
				t.Fatalf("Prepare() error = %v, want ErrPlanBinding", err)
			}
		})
	}
	if resolved.Load() != 0 {
		t.Fatalf("mismatched plans resolved credential %d times", resolved.Load())
	}
}

func TestControlledDispatchConsumesPermitBeforeExactlyOneLoopbackRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer dispatch-token" {
			t.Errorf("authorization header = %q", got)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	registry := ledger.NewPermitRegistry()
	resolver := secretResolverFunc(func(context.Context, domain.ID, domain.ID) (map[string]string, error) {
		return map[string]string{"value": "dispatch-token"}, nil
	})
	var transport *ControlledHTTPTransport
	consume := func(_ context.Context, _ domain.AuthContext, _ domain.OwnerToken, permit ledger.DispatchPermit, digest string) error {
		if requests.Load() != 0 {
			t.Fatal("network request began before permit consumption")
		}
		if err := registry.Validate(permit, "runtime-1", digest, "revision-1"); err != nil {
			return err
		}
		return registry.MarkConsumed(permit, "runtime-1", digest)
	}
	transport = &ControlledHTTPTransport{runtimeID: "runtime-1", handles: NewHandleTable(), client: newHTTPClient(true), secrets: resolver, consume: consume}
	auth := domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermExecute}}
	owner := domain.OwnerToken{Key: domain.OperationKey{Namespace: "ns-1", OperationID: "op-1"}, RuntimeID: "runtime-1", OwnerID: "owner-1", Generation: 1}
	handle, err := transport.Prepare(context.Background(), auth, owner, "attempt-1", adapters.EncodedRequest{
		Endpoint: server.URL, Method: http.MethodPost, Headers: map[string]string{"Content-Type": "application/json"},
		Body: []byte(`{"prompt":"test"}`), CredentialRef: "credential", CredentialHeader: "Authorization", CredentialPrefix: "Bearer ", MaxResponseBytes: 1024,
	}, domain.ExecutionPlan{Endpoint: server.URL, CredentialRef: "credential", CredentialRevision: "revision-1"})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := registry.Issue("attempt-1", "runtime-1", handle.RequestDigest(), handle.CredentialRevision())
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.Dispatch(context.Background(), permit, handle)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil || response.Status != http.StatusOK || string(body) != `{"ok":true}` || requests.Load() != 1 {
		t.Fatalf("response=%+v body=%s requestCount=%d err=%v", response, body, requests.Load(), err)
	}
	if _, err := transport.Dispatch(context.Background(), permit, handle); !errors.Is(err, ErrHandleUsed) || requests.Load() != 1 {
		t.Fatalf("re-dispatch err=%v requestCount=%d", err, requests.Load())
	}
	transport.handles.mu.Lock()
	_, exists := transport.handles.handles[handle.ID()]
	transport.handles.mu.Unlock()
	if !exists {
		t.Fatal("spent handle tombstone should reject reuse")
	}
}

func TestControlledDispatchConsumeFailureNeverSends(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	consumeErr := errors.New("ledger unavailable")
	transport := &ControlledHTTPTransport{runtimeID: "runtime-1", handles: NewHandleTable(), client: newHTTPClient(true), consume: func(context.Context, domain.AuthContext, domain.OwnerToken, ledger.DispatchPermit, string) error {
		return consumeErr
	}}
	auth := domain.AuthContext{PrincipalID: "p", Namespace: "ns", Permissions: []domain.Permission{domain.PermExecute}}
	owner := domain.OwnerToken{Key: domain.OperationKey{Namespace: "ns", OperationID: "op"}, RuntimeID: "runtime-1", OwnerID: "owner", Generation: 1}
	handle, err := transport.handles.prepare(adapters.EncodedRequest{Endpoint: server.URL, Method: http.MethodPost, Body: []byte("x"), MaxResponseBytes: 100}, domain.ExecutionPlan{CredentialRevision: "rev"}, nil, auth, owner, "attempt")
	if err != nil {
		t.Fatal(err)
	}
	registry := ledger.NewPermitRegistry()
	permit, err := registry.Issue("attempt", "runtime-1", handle.RequestDigest(), handle.CredentialRevision())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Dispatch(context.Background(), permit, handle); !errors.Is(err, consumeErr) || requests.Load() != 0 {
		t.Fatalf("dispatch err=%v requestCount=%d", err, requests.Load())
	}
	if _, err := transport.Dispatch(context.Background(), permit, handle); !errors.Is(err, ErrHandleUsed) || requests.Load() != 0 {
		t.Fatalf("retry err=%v requestCount=%d", err, requests.Load())
	}
}
