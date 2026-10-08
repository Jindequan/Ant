package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/webui"
)

func TestPageHandlerServesThePageAndNothingElse(t *testing.T) {
	h := PageHandler(webui.Files())
	for path, want := range map[string]string{"/": "<title>Ant</title>", "/app.js": "/api/quota", "/app.css": "--surface"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: %d, missing %q", path, rec.Code, want)
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
			t.Fatalf("%s: no CSP", path)
		}
	}
	for _, path := range []string{"/v1/pools", "/static/app.js", "/missing"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: %d", rec.Code)
	}
}
