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
	// a provider's logo is served; anything else under /logos isn't
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logos/deepseek.png", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("logo: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, path := range []string{"/v1/pools", "/static/app.js", "/missing", "/logos/", "/logos/nobody.png", "/logos/../app.js", "/logos/DeepSeek.png", "/logos/deepseek.svg"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", path, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: %d", rec.Code)
	}
}
