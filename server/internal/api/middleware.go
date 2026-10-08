package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/security"
)

// AUTH-01 (T2.1) HTTP boundary, gated G1-DEV. Design 03 §2: the
// AuthProvider-produced context is the only source of identity; request
// bodies never self-authorize a namespace or permission. Design 05 §5:
// logs never contain credentials or secret material. This middleware is
// handler-independent wiring; the SQLite-backed token store is wired by
// later T2.1 leaves.

type principalKey struct{}

// APIError is the uniform JSON error envelope. Codes are stable strings so
// clients can branch; messages never echo credentials or secret material.
type APIError struct {
	Status int
	Group  string
	Code   string
}

func (e APIError) write(w http.ResponseWriter) {
	messageKey, action := "ant.error.forbidden", "check_permissions"
	if e.Status == http.StatusUnauthorized {
		messageKey, action = "ant.error.unauthenticated", "login"
	} else if e.Code == "csrf_failed" {
		messageKey, action = "ant.error.csrfFailed", "refresh_session"
	}
	writeConfigError(w, e.Status, e.Code, messageKey, false, action)
}

var (
	errUnauthenticated = APIError{Status: http.StatusUnauthorized, Group: "auth", Code: "unauthenticated"}
	errForbidden       = APIError{Status: http.StatusForbidden, Group: "auth", Code: "forbidden"}
	errCSRF            = APIError{Status: http.StatusForbidden, Group: "auth", Code: "csrf_failed"}
	// errSelfAuthorize marks a body/query that declares a different
	// namespace than the server-derived principal namespace. The distinct
	// code exists for diagnosis only; the outcome is the same 403.
	errSelfAuthorize = APIError{Status: http.StatusForbidden, Group: "auth", Code: "namespace_mismatch"}
)

const (
	sessionCookieName = "ant_session"
	csrfCookieName    = "ant_csrf"
)

// AuthMiddleware authenticates a bearer token or an HttpOnly session cookie
// and injects the server-derived Principal. Cookie-authenticated writes require
// a matching CSRF header/cookie/hash and a same-origin Origin header.
func AuthMiddleware(auth *security.AuthProvider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		bearer, cookieAuth := "", false
		if header != "" {
			var ok bool
			bearer, ok = bearerToken(r)
			if !ok {
				errUnauthenticated.write(w)
				return
			}
		} else if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			bearer, cookieAuth = cookie.Value, true
		} else {
			errUnauthenticated.write(w)
			return
		}
		principal, err := auth.Authenticate(bearer)
		if err != nil || principal.Kind == security.TokenKindBootstrap || (cookieAuth && principal.Kind != security.TokenKindSession) {
			// Uniform 401: unknown, revoked and malformed are
			// indistinguishable from the outside.
			errUnauthenticated.write(w)
			return
		}
		if cookieAuth && !safeMethod(r.Method) && !validCSRFAttempt(r, principal) {
			errCSRF.write(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func validCSRFAttempt(r *http.Request, principal security.Principal) bool {
	header := r.Header.Get("X-CSRF-Token")
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || header == "" || cookie.Value == "" || principal.CSRFHash == "" {
		return false
	}
	if !security.ConstantTimeEqual([]byte(header), []byte(cookie.Value)) || !sameOrigin(r) {
		return false
	}
	hash := sha256.Sum256([]byte(header))
	actual := []byte(hex.EncodeToString(hash[:]))
	return security.ConstantTimeEqual(actual, []byte(principal.CSRFHash))
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme) && strings.EqualFold(parsed.Host, r.Host)
}

// bearerToken extracts "Authorization: Bearer <token>". Anything else
// (missing header, wrong scheme, empty value) is unauthenticated.
func bearerToken(r *http.Request) (string, bool) {
	const scheme = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	return strings.TrimSpace(h[len(scheme):]), true
}

// PrincipalFrom returns the authenticated principal of the request context.
// Handlers must derive every authorization decision from this value.
func PrincipalFrom(ctx context.Context) (security.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(security.Principal)
	return p, ok
}

// RequirePermission admits only principals holding perm on their own
// (server-derived) namespace. Cross-namespace targets are denied by
// AssertNamespace before handlers reach any store.
func RequirePermission(perm domain.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			errUnauthenticated.write(w)
			return
		}
		if !security.Can(p, perm, p.Namespace) {
			errForbidden.write(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AssertNamespace rejects a namespace field that arrived from the request
// (body/query) when it differs from the principal's server-derived
// namespace. A body can echo its own namespace; it can never claim another
// one (design 03 §2 "不接受请求体自授 namespace").
func AssertNamespace(p security.Principal, requested domain.ID) APIError {
	if requested != p.Namespace {
		return errSelfAuthorize
	}
	return APIError{}
}

// GuardHandler adapts AssertNamespace's verdict for tests and handlers
// that take the requested namespace from a decoded body.
func GuardHandler(p security.Principal, requested domain.ID, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiErr := AssertNamespace(p, requested); apiErr.Status != 0 {
			apiErr.write(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LogRequest emits one structured line per request. Only the principal ID
// and namespace identify the caller; bearer tokens, Authorization headers
// and any secret material must never be passed to the logger.
func LogRequest(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attrs := []any{"method", r.Method, "path", r.URL.Path}
		if p, ok := PrincipalFrom(r.Context()); ok {
			attrs = append(attrs, "principalId", string(p.PrincipalID), "namespace", string(p.Namespace))
		}
		logger.Info("request", attrs...)
		next.ServeHTTP(w, r)
	})
}
