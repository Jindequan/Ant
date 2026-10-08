package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A reader's key in a custom header must not follow a redirect to another
// host; Go would forward it.
func TestReaderClientDoesNotCarryKeysAcrossHosts(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer origin.Close()

	l := Local{HTTP: readerClient()}
	_, err := l.fetch(context.Background(), http.MethodGet, origin.URL, map[string]string{"xi-api-key": "secret"}, nil)
	var se *statusError
	if !errors.As(err, &se) || se.Code != http.StatusFound {
		t.Fatalf("fetch error = %v; want the redirect answered as a 302 reply", err)
	}
	if leaked.Load() {
		t.Fatal("the key reached another host")
	}
}

func TestReaderClientSameHostRedirectStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	b, err := Local{HTTP: readerClient()}.fetch(context.Background(), http.MethodGet, srv.URL+"/old", nil, nil)
	if err != nil || string(b) != `{"ok":true}` {
		t.Fatalf("fetch = %q, %v", b, err)
	}
}

func TestReaderClientRefusesPlainHTTPOffThisMachine(t *testing.T) {
	_, err := Local{HTTP: readerClient()}.fetch(context.Background(), http.MethodGet, "http://provider.invalid/usage", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("fetch error = %v; want an HTTPS refusal before any connection", err)
	}
}
