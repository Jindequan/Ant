package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"ant/server/internal/domain/observed"
)

func TestObservationFetchReadsOnlyDeclaredOrigins(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("xi-api-key") != "" {
			leaked.Store(true)
		}
	}))
	defer other.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/balance":
			if r.Header.Get("Authorization") != "Bearer k" || r.URL.RawQuery != "x=1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/away":
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", observationMaxBytes+1)))
		}
	}))
	defer srv.Close()
	// both test servers share one CA, so srv's client trusts other too
	fetch, err := NewObservationFetch([]string{srv.URL}, srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	res, err := fetch(ctx, observed.Request{URL: srv.URL + "/balance?x=1", Header: map[string]string{"Authorization": "Bearer k"}})
	if err != nil || res.Status != 200 || string(res.Body) != `{"ok":true}` {
		t.Fatalf("declared origin: %d %q %v", res.Status, res.Body, err)
	}
	if _, err := fetch(ctx, observed.Request{URL: other.URL + "/balance"}); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("undeclared origin: %v", err)
	}
	if _, err := fetch(ctx, observed.Request{URL: strings.Replace(srv.URL, "https://", "http://", 1) + "/balance"}); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("plain http: %v", err)
	}
	res, err = fetch(ctx, observed.Request{URL: srv.URL + "/away", Header: map[string]string{"Authorization": "Bearer k", "xi-api-key": "k"}})
	if err != nil || res.Status != http.StatusFound || leaked.Load() {
		t.Fatalf("redirect away: %d %v leaked=%v", res.Status, err, leaked.Load())
	}
	if _, err := fetch(ctx, observed.Request{URL: srv.URL + "/big"}); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("oversized reply: %v", err)
	}
}

func TestObservationFetchRejectsBadOrigins(t *testing.T) {
	for _, bad := range [][]string{nil, {"http://api.z.ai"}, {"https://api.z.ai/path"}, {"https://u:p@api.z.ai"}, {"https://api.z.ai?x=1"}} {
		if _, err := NewObservationFetch(bad, nil); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
