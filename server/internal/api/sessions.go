package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/security"
)

const sessionLifetimeMillis = int64(12 * time.Hour / time.Millisecond)

type SessionClock interface{ Now() domain.Millis }

// RegisterSessionRoutes installs bootstrap exchange and browser-session
// lifecycle routes. The bootstrap token is accepted only by POST /sessions;
// all subsequent browser requests use an HttpOnly cookie.
func RegisterSessionRoutes(mux *http.ServeMux, auth *security.AuthProvider, tokens security.TokenStore, clock SessionClock) {
	mux.HandleFunc("POST /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in LoginInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		if in.Token == "" {
			writeConfigError(w, http.StatusUnauthorized, "Unauthenticated", "ant.error.unauthenticated", false, "login")
			return
		}
		administrator, err := auth.Authenticate(in.Token)
		if err != nil || administrator.Kind != security.TokenKindBootstrap {
			writeConfigError(w, http.StatusUnauthorized, "Unauthenticated", "ant.error.unauthenticated", false, "login")
			return
		}
		now := clock.Now()
		if int64(now) > math.MaxInt64-sessionLifetimeMillis {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		sessionToken, err := newOpaqueToken()
		if err != nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		csrfToken, err := newOpaqueToken()
		if err != nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		csrfHash := sha256.Sum256([]byte(csrfToken))
		expiresAt := domain.Millis(int64(now) + sessionLifetimeMillis)
		record := security.TokenRecord{
			PrincipalID: administrator.PrincipalID,
			Namespace:   administrator.Namespace,
			Permissions: administrator.Permissions,
			Kind:        security.TokenKindSession,
			ExpiresAt:   expiresAt,
			CSRFHash:    hex.EncodeToString(csrfHash[:]),
		}
		if err := tokens.ExchangeBootstrap(security.HashToken(in.Token), security.HashToken(sessionToken), record); errors.Is(err, security.ErrUnauthenticated) {
			writeConfigError(w, http.StatusUnauthorized, "unauthenticated", "ant.error.unauthenticated", false, "login")
			return
		} else if err != nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		setSessionCookies(w, r, sessionToken, csrfToken, expiresAt)
		writeConfigJSON(w, http.StatusOK, SessionView{
			PrincipalID: administrator.PrincipalID,
			Namespace:   administrator.Namespace,
			Permissions: administrator.Permissions,
			CSRFToken:   csrfToken,
			ExpiresAt:   expiresAt,
		})
	})

	mux.Handle("GET /v1/session", AuthMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFrom(r.Context())
		if !ok || principal.Kind != security.TokenKindSession {
			writeConfigError(w, http.StatusUnauthorized, "Unauthenticated", "ant.error.unauthenticated", false, "login")
			return
		}
		csrf, err := r.Cookie(csrfCookieName)
		if err != nil || !matchesCSRFHash(csrf.Value, principal.CSRFHash) {
			writeConfigError(w, http.StatusUnauthorized, "Unauthenticated", "ant.error.unauthenticated", false, "login")
			return
		}
		writeConfigJSON(w, http.StatusOK, SessionView{
			PrincipalID: principal.PrincipalID,
			Namespace:   principal.Namespace,
			Permissions: principal.Permissions,
			CSRFToken:   csrf.Value,
			ExpiresAt:   principal.ExpiresAt,
		})
	})))

	mux.Handle("DELETE /v1/session", AuthMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFrom(r.Context())
		if !ok || principal.Kind != security.TokenKindSession {
			writeConfigError(w, http.StatusUnauthorized, "Unauthenticated", "ant.error.unauthenticated", false, "login")
			return
		}
		session, err := r.Cookie(sessionCookieName)
		if err != nil || session.Value == "" {
			writeConfigError(w, http.StatusUnauthorized, "Unauthenticated", "ant.error.unauthenticated", false, "login")
			return
		}
		if auth.RevokeToken(session.Value) != nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		clearSessionCookies(w, r)
		w.WriteHeader(http.StatusNoContent)
	})))
}

func newOpaqueToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("session entropy unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func matchesCSRFHash(token, expected string) bool {
	if token == "" || expected == "" {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	actual := []byte(hex.EncodeToString(hash[:]))
	return security.ConstantTimeEqual(actual, []byte(expected))
}

func setSessionCookies(w http.ResponseWriter, r *http.Request, session, csrf string, expiresAt domain.Millis) {
	expires := time.UnixMilli(int64(expiresAt))
	maxAge := int(sessionLifetimeMillis / 1000)
	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: session, Path: "/v1", Expires: expires,
		MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: csrf, Path: "/v1", Expires: expires,
		MaxAge: maxAge, HttpOnly: false, Secure: secure, SameSite: http.SameSiteStrictMode})
	w.Header().Set("Cache-Control", "no-store")
}

func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/v1", MaxAge: -1,
			HttpOnly: name == sessionCookieName, Secure: secure, SameSite: http.SameSiteStrictMode})
	}
	w.Header().Set("Cache-Control", "no-store")
}
