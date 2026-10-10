package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestGeminiClientReadFromInstalledModule(t *testing.T) {
	l := testLocal(t, nil)
	bin := l.path("tools", "bin")
	l.Getenv = func(key string) string {
		if key == "PATH" {
			return bin
		}
		return ""
	}
	writeFile(t, filepath.Join(bin, "gemini"), "// CLI entry without embedded client")
	writeFile(t, l.path("tools", "node_modules", "@google", "gemini-cli-core", "dist", "src", "code_assist", "oauth2.js"), "const OAUTH_CLIENT_ID = 'fixture-client';\nconst OAUTH_CLIENT_SECRET = 'fixture-secret';")
	id, secret, err := geminiOAuthClient(l)
	if err != nil || id != "fixture-client" || secret != "fixture-secret" {
		t.Fatal("installed module client was not resolved")
	}
}

func TestGeminiMissingClientNeverSendsRefreshToken(t *testing.T) {
	for _, env := range []map[string]string{nil, {"GEMINI_OAUTH_CLIENT_ID": "fixture-client"}} {
		l := testLocal(t, env)
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(500) }))
		l.URLs["google.token"] = srv.URL
		_, err := geminiToken(context.Background(), l, geminiCreds{RefreshToken: "fixture-refresh"})
		srv.Close()
		if !errors.Is(err, errGeminiClientUnavailable) || called {
			t.Fatal("missing client must stop before transmitting a refresh token")
		}
		token, err := geminiToken(context.Background(), l, geminiCreds{AccessToken: "fresh", Expiry: l.Now().Add(10 * time.Minute).UnixMilli()})
		if err != nil || token != "fresh" {
			t.Fatal("fresh access token must work without client resolution")
		}
	}
}
