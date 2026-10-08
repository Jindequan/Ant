package deepseek

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ant/server/internal/providers/catalog"
	"ant/server/internal/transport"
)

func TestSupplyInventoryAndModels(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") != "Bearer sk-test":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/user/balance":
			_, _ = w.Write(balanceFixture(t))
		case r.URL.Path == "/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-v4"},{"id":"deepseek-v4-pro"}]}`))
		}
	}))
	defer srv.Close()
	m := SupplyModule(srv.URL)
	fetch, err := transport.NewObservationFetch(m.Supply.Origins, srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	ok := catalog.Session{Values: map[string]string{"apiKey": "sk-test"}}
	inv, err := m.Supply.Collector.Inventory(context.Background(), ok, fetch)
	if err != nil || len(inv.Packages) != 1 || inv.Note != "" {
		t.Fatalf("inventory = %+v %v", inv, err)
	}
	p := inv.Packages[0]
	r := p.Resources[0]
	if p.Kind != "prepaid" || p.Usable == nil || !*p.Usable || r.TemplateRef != "balance/cny" || r.Currency != "CNY" || r.QuantumExp != -2 ||
		*r.Remaining != 2340 || !strings.Contains(string(r.Evidence), `"granted":"10.00"`) || r.Limit != nil {
		t.Fatalf("package %+v resource %+v", p, r)
	}
	if d := m.Supply.Endpoints[0].Draws; d[0] != r.TemplateRef {
		t.Fatalf("the endpoint doesn't draw on the balance: %v", d)
	}
	ms, err := m.Supply.Collector.Models(context.Background(), ok, fetch)
	if err != nil || len(ms) != 2 || ms[1].ID != "deepseek-v4-pro" {
		t.Fatalf("models = %+v %v", ms, err)
	}
	if _, err := m.Supply.Collector.Inventory(context.Background(), catalog.Session{Values: map[string]string{"apiKey": "bad"}}, fetch); !errors.Is(err, catalog.ErrSignedOut) {
		t.Fatalf("refused key: %v", err)
	}
}

func TestSupplyBalanceTooLowToCall(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"is_available":false,"balance_infos":[{"currency":"USD","total_balance":"0.01","granted_balance":"0.00","topped_up_balance":"0.01"}]}`))
	}))
	defer srv.Close()
	m := SupplyModule(srv.URL)
	fetch, _ := transport.NewObservationFetch(m.Supply.Origins, srv.Client().Transport)
	inv, err := m.Supply.Collector.Inventory(context.Background(), catalog.Session{Values: map[string]string{"apiKey": "k"}}, fetch)
	if err != nil || *inv.Packages[0].Usable || inv.Note == "" || inv.Packages[0].Resources[0].TemplateRef != "balance/usd" {
		t.Fatalf("inventory = %+v %v", inv, err)
	}
}

// balanceFixture is the canonical balance reply, shared with quota's and
// the supply adapter's tests.
func balanceFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "balance.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
