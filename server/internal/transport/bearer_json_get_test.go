package transport

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBearerJSONGETMakesOneBoundedRequest(t *testing.T) {
	const token = "sk-secret-token"
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/balance" || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s %s %#v", r.Method, r.URL, r.Header)
		}
		_, _ = io.WriteString(w, `{"is_available":true}`)
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	httpClient, err := NewHTTPClientWithRootCAs(roots)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewBearerJSONGET(httpClient, srv.URL+"/balance", 1024)
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.FetchBalance(context.Background(), token)
	if err != nil || string(data) != `{"is_available":true}` || calls.Load() != 1 {
		t.Fatalf("response=%s err=%v calls=%d", data, err, calls.Load())
	}
}

func TestBearerJSONGETDoesNotExposeErrorBodyOrSendInvalidKey(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "credential rejected: sk-secret-token")
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	httpClient, err := NewHTTPClientWithRootCAs(roots)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewBearerJSONGET(httpClient, srv.URL+"/balance", 1024)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FetchBalance(context.Background(), "sk-secret-token")
	if err == nil || strings.Contains(err.Error(), "sk-secret-token") {
		t.Fatalf("error leaked provider response: %v", err)
	}
	for _, key := range []string{"", "bad\nheader", strings.Repeat("x", 8193)} {
		if _, err := client.FetchBalance(context.Background(), key); err == nil {
			t.Errorf("invalid key accepted: %q", key)
		}
	}
}
