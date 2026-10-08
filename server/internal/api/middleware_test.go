package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/security"
)

// AUTH-01 acceptance coverage. All tests drive http.Handler.ServeHTTP
// through httptest.NewRecorder — no listener is created and no network
// traffic of any kind occurs (G1-DEV no-egress requirement).

const (
	nsA = domain.ID("ns-alpha")
	nsB = domain.ID("ns-beta")
)

type env struct {
	auth   *security.AuthProvider
	token  string
	logger bytes.Buffer
}

func newEnv(t *testing.T, perms ...domain.Permission) *env {
	t.Helper()
	tokens := security.NewMemoryTokenStore()
	token := "test-api-token"
	err := tokens.Put(security.HashToken(token), security.TokenRecord{
		PrincipalID: "admin-1", Namespace: nsA, Permissions: perms, Kind: security.TokenKindAPIToken,
	})
	if err != nil {
		t.Fatalf("token fixture: %v", err)
	}
	return &env{auth: security.NewAuthProvider(tokens), token: token}
}

func (e *env) do(t *testing.T, handler http.Handler, method, target, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

// echoPrincipal writes the context principal back so tests can assert what
// the server derived (never what a body claimed).
var echoPrincipal = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"principalId": string(p.PrincipalID),
		"namespace":   string(p.Namespace),
	})
})

func TestMissingOrMalformedAuthorizationIs401(t *testing.T) {
	e := newEnv(t, domain.PermRead)
	h := AuthMiddleware(e.auth, echoPrincipal)
	for _, tc := range []struct {
		name   string
		bearer string
		header bool
	}{
		{"no header", "", false},
		{"empty bearer", "", true},
		{"wrong scheme", "Basic dXNlcjpwYXNz", false},
		{"unknown token", "bogus-token", true},
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		if tc.header && tc.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		if tc.header && tc.bearer == "" {
			r.Header.Set("Authorization", "Bearer ")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", tc.name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "unauthenticated") {
			t.Fatalf("%s: body lacks uniform error code: %s", tc.name, rec.Body.String())
		}
	}
}

func TestPrincipalDerivesFromServerGrantsNotRequestBody(t *testing.T) {
	e := newEnv(t, domain.PermRead)
	h := AuthMiddleware(e.auth, echoPrincipal)
	// The body claims the beta namespace; the principal must still be the
	// server-held ns-alpha identity. Body namespace is ignored.
	body := `{"namespace":"` + string(nsB) + `","operationId":"op-1"}`
	rec := e.do(t, h, http.MethodPost, "/v1/operations", e.token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["namespace"] != string(nsA) || got["principalId"] != "admin-1" {
		t.Fatalf("body self-authorization leaked through: %v", got)
	}
}

func TestBodySelfAuthorizationIsRejectedWith403(t *testing.T) {
	e := newEnv(t, domain.PermRead, domain.PermExecute)
	// Handler decodes the body's namespace and passes it through
	// GuardHandler/AssertNamespace — the production pattern for every
	// namespace-carrying endpoint.
	h := AuthMiddleware(e.auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		var body struct {
			Namespace domain.ID `json:"namespace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		GuardHandler(p, body.Namespace, echoPrincipal).ServeHTTP(w, r)
	}))
	// Own namespace: accepted.
	if rec := e.do(t, h, http.MethodPost, "/v1/operations", e.token,
		`{"namespace":"`+string(nsA)+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("own namespace rejected: %d %s", rec.Code, rec.Body.String())
	}
	// Foreign namespace in the body: 403 even though the token is valid
	// and even if it asked for a permission it holds.
	rec := e.do(t, h, http.MethodPost, "/v1/operations", e.token,
		`{"namespace":"`+string(nsB)+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-namespace body: status %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "namespace_mismatch") {
		t.Fatalf("body lacks self-authorization code: %s", rec.Body.String())
	}
}

func TestCrossNamespaceAccessDeniedForReadCancelManage(t *testing.T) {
	e := newEnv(t, domain.PermRead, domain.PermCancel, domain.PermManage)
	// The handler targets whatever namespace the test put in the URL —
	// here the beta namespace while the principal lives in alpha. The
	// security predicate must deny every permission class.
	h := AuthMiddleware(e.auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		if !security.Can(p, domain.PermRead, nsB) &&
			!security.Can(p, domain.PermCancel, nsB) &&
			!security.Can(p, domain.PermManage, nsB) {
			errForbidden.write(w)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := e.do(t, h, http.MethodGet, "/v1/beta/operations/op-1", e.token, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-namespace read/cancel/manage: %d, want 403", rec.Code)
	}
}

func TestRequirePermissionAdmitsOwnAndDeniesMissing(t *testing.T) {
	e := newEnv(t, domain.PermRead) // no manage
	h := AuthMiddleware(e.auth, RequirePermission(domain.PermManage, echoPrincipal))
	rec := e.do(t, h, http.MethodGet, "/v1/admin/metrics", e.token, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing permission: %d, want 403", rec.Code)
	}
	h2 := AuthMiddleware(e.auth, RequirePermission(domain.PermRead, echoPrincipal))
	rec = e.do(t, h2, http.MethodGet, "/v1/operations/op-1", e.token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("own-namespace permitted call: %d %s", rec.Code, rec.Body.String())
	}
	// No principal in context (middleware skipped): handler fails closed.
	rec = httptest.NewRecorder()
	RequirePermission(domain.PermRead, echoPrincipal).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("context without principal: %d, want 401", rec.Code)
	}
}

func TestRevokedTokenFailsAtTheBoundary(t *testing.T) {
	e := newEnv(t, domain.PermRead)
	if err := e.auth.RevokeToken(e.token); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rec := e.do(t, AuthMiddleware(e.auth, echoPrincipal), http.MethodGet, "/v1/whoami", e.token, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d, want 401", rec.Code)
	}
}

func TestLogsAndResponsesNeverCarryCredentials(t *testing.T) {
	e := newEnv(t, domain.PermRead)
	logger := slog.New(slog.NewJSONHandler(&e.logger, nil))
	// Production order: identity is injected first, then the request is
	// logged with its principal context, then the handler runs.
	h := AuthMiddleware(e.auth, LogRequest(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		// Simulate an endpoint that also resolves a secret view: the
		// response and the log must both stay free of cleartext.
		view, err := newLocalSecretRoundTrip(t)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"namespace": string(p.Namespace), "secretMask": view.Mask,
		})
	})))
	rec := e.do(t, h, http.MethodGet, "/v1/bindings/b-1", e.token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), e.token) {
		t.Fatalf("response leaks bearer token: %s", rec.Body.String())
	}
	logged := e.logger.String()
	if strings.Contains(logged, e.token) {
		t.Fatalf("log leaks bearer token: %s", logged)
	}
	if strings.Contains(logged, "sk-fake-local-roundtrip") {
		t.Fatalf("log leaks secret material: %s", logged)
	}
	if !strings.Contains(logged, string(nsA)) {
		t.Fatalf("log lacks principal namespace context: %s", logged)
	}
}

func TestLogOfBadRequestsCarriesNoAuthorizationHeader(t *testing.T) {
	e := newEnv(t, domain.PermRead)
	logger := slog.New(slog.NewJSONHandler(&e.logger, nil))
	h := LogRequest(logger, AuthMiddleware(e.auth, echoPrincipal))
	r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	r.Header.Set("Authorization", "Bearer leaked-value-must-not-appear")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if strings.Contains(e.logger.String(), "leaked-value-must-not-appear") ||
		strings.Contains(e.logger.String(), "Authorization") {
		t.Fatalf("log records credential material: %s", e.logger.String())
	}
}

// newLocalSecretRoundTrip is a tiny in-test secret write/resolve so the
// credential-free-response assertion exercises real SecretService output.
func newLocalSecretRoundTrip(t *testing.T) (security.SecretView, error) {
	t.Helper()
	svc, err := security.NewSecretService(
		bytes.Repeat([]byte{0x24}, 32), security.NewMemorySecretStore())
	if err != nil {
		return security.SecretView{}, err
	}
	return svc.Write("cred-x", "rev-1", map[string]string{"api_key": "sk-fake-local-roundtrip-9876"})
}
