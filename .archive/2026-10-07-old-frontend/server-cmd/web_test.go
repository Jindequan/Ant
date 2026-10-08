package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebHandlerServesAssetsAndSPAWithoutCapturingAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>ANT app</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("window.ant = true"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := newWebHandler(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	for _, tc := range []struct {
		name       string
		path       string
		accept     string
		wantStatus int
		wantBody   string
	}{
		{name: "root without accept header", path: "/", wantStatus: http.StatusOK, wantBody: "ANT app"},
		{name: "root", path: "/", accept: "text/html", wantStatus: http.StatusOK, wantBody: "ANT app"},
		{name: "client route", path: "/operations/operation-1", accept: "text/html,application/xhtml+xml", wantStatus: http.StatusOK, wantBody: "ANT app"},
		{name: "built asset", path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "window.ant"},
		{name: "missing asset", path: "/assets/missing.js", accept: "text/html", wantStatus: http.StatusNotFound},
		{name: "api stays api", path: "/v1/operations", accept: "text/html", wantStatus: http.StatusNotFound},
		{name: "health stays api", path: "/healthz", accept: "text/html", wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept", tc.accept)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.wantStatus || !strings.Contains(res.Body.String(), tc.wantBody) {
				t.Fatalf("status=%d body=%q; want status=%d containing %q", res.Code, res.Body.String(), tc.wantStatus, tc.wantBody)
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/unknown", nil)
	req.Header.Set("Accept", "text/html")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("non-read method status=%d allow=%q", res.Code, res.Header().Get("Allow"))
	}
}

func TestNewWebHandlerRequiresBuiltIndex(t *testing.T) {
	if _, err := newWebHandler(t.TempDir()); err == nil {
		t.Fatal("accepted a web directory without index.html")
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newWebHandler(file); err == nil {
		t.Fatal("accepted a file instead of a web directory")
	}
}

func TestWebHandlerCannotServeSymlinkOutsideWebRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>ANT app</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(secret, []byte("outside file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "leak.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	handler, err := newWebHandler(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	req := httptest.NewRequest(http.MethodGet, "/leak.txt", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "outside file") {
		t.Fatalf("outside symlink response status=%d body=%q", res.Code, res.Body.String())
	}
}
