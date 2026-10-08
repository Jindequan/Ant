package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/security"
)

type fixedSessionClock struct{ now domain.Millis }

func (c fixedSessionClock) Now() domain.Millis { return c.now }

func TestBootstrapExchangeSessionCSRFAndLogout(t *testing.T) {
	tokens := security.NewMemoryTokenStore()
	bootstrap, err := security.BootstrapAdmin(tokens, "admin", "tenant-a", []domain.Permission{domain.PermRead, domain.PermConfigure})
	if err != nil {
		t.Fatal(err)
	}
	auth := security.NewAuthProvider(tokens)
	mux := http.NewServeMux()
	RegisterSessionRoutes(mux, auth, tokens, fixedSessionClock{now: 1_800_000_000_000})
	loginReq := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"token":"`+bootstrap+`"}`))
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	var view SessionView
	if err := json.Unmarshal(loginRec.Body.Bytes(), &view); err != nil || view.PrincipalID != "admin" || view.Namespace != "tenant-a" || view.CSRFToken == "" {
		t.Fatalf("invalid session projection: %+v err=%v", view, err)
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		switch cookie.Name {
		case sessionCookieName:
			sessionCookie = cookie
		case csrfCookieName:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode || csrfCookie == nil || csrfCookie.HttpOnly {
		t.Fatalf("wrong session cookie policy: session=%+v csrf=%+v", sessionCookie, csrfCookie)
	}
	if _, err := auth.Authenticate(bootstrap); err == nil {
		t.Fatal("bootstrap token remained usable after session exchange")
	}

	currentReq := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
	currentReq.AddCookie(sessionCookie)
	currentReq.AddCookie(csrfCookie)
	currentRec := httptest.NewRecorder()
	mux.ServeHTTP(currentRec, currentReq)
	if currentRec.Code != http.StatusOK {
		t.Fatalf("current-session status=%d body=%s", currentRec.Code, currentRec.Body.String())
	}

	mutate := AuthMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name, origin, csrf string
		want               int
	}{
		{name: "missing csrf", origin: "http://example.test", want: http.StatusForbidden},
		{name: "cross origin", origin: "https://evil.test", csrf: view.CSRFToken, want: http.StatusForbidden},
		{name: "matching csrf", origin: "http://example.test", csrf: view.CSRFToken, want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://example.test/write", nil)
			req.Host = "example.test"
			req.AddCookie(sessionCookie)
			req.AddCookie(csrfCookie)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.csrf != "" {
				req.Header.Set("X-CSRF-Token", tc.csrf)
			}
			rec := httptest.NewRecorder()
			mutate.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	logoutReq := httptest.NewRequest(http.MethodDelete, "/v1/session", nil)
	logoutReq.Host = "example.test"
	logoutReq.Header.Set("Origin", "http://example.test")
	logoutReq.Header.Set("X-CSRF-Token", view.CSRFToken)
	logoutReq.AddCookie(sessionCookie)
	logoutReq.AddCookie(csrfCookie)
	logoutRec := httptest.NewRecorder()
	mux.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logoutRec.Code, logoutRec.Body.String())
	}
	if _, err := auth.Authenticate(sessionCookie.Value); err == nil {
		t.Fatal("session token remained usable after logout")
	}
}

func TestBootstrapTokenCannotCallProtectedRoutesWithoutExchange(t *testing.T) {
	tokens := security.NewMemoryTokenStore()
	bootstrap, err := security.BootstrapAdmin(tokens, "admin", "tenant-a", []domain.Permission{domain.PermRead})
	if err != nil {
		t.Fatal(err)
	}
	h := AuthMiddleware(security.NewAuthProvider(tokens), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	req.Header.Set("Authorization", "Bearer "+bootstrap)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap token directly authorized route: status=%d", rec.Code)
	}
}
