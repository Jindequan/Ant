package zai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/transport"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func server(t *testing.T) (*httptest.Server, catalog.Module, observed.Fetch, *string) {
	t.Helper()
	var auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/api/coding/paas/v4/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"glm-5.3-air"}]}`))
		case auth == "payg":
			_, _ = w.Write(fixture(t, "no-plan.json"))
		case auth == "expired":
			// what Z.ai really answers a bad key (2026-10-08): HTTP 200
			_, _ = w.Write(fixture(t, "refused.json"))
		case auth == "abc.secret":
			_, _ = w.Write(fixture(t, "plan.json"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(srv.Close)
	m := Module(srv.URL)
	fetch, err := transport.NewObservationFetch(m.Supply.Origins, srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return srv, m, fetch, &auth
}

func TestPlanInventory(t *testing.T) {
	_, m, fetch, auth := server(t)
	inv, err := m.Supply.Collector.Inventory(context.Background(), catalog.Session{Values: map[string]string{"apiKey": "abc.secret"}}, fetch)
	if err != nil || *auth != "abc.secret" {
		t.Fatalf("inventory: %v, Authorization %q (want the bare key)", err, *auth)
	}
	if inv.Identity != "" || inv.DisplayGroup != "global:Pro:1791400000000,1791900000000,1792000000000" || len(inv.Packages) != 1 {
		t.Fatalf("inventory = %+v", inv)
	}
	p := inv.Packages[0]
	if p.Kind != "subscription" || p.Basis != "observed" || p.Plan != "Pro" || len(p.Resources) != 3 {
		t.Fatalf("package = %+v", p)
	}
	short, week, mcp := p.Resources[0], p.Resources[1], p.Resources[2]
	if short.TemplateRef != MeterShort || short.Label != "5 hours" || *short.Used != 1200 || *short.Remaining != 8800 || *short.Limit != 10000 ||
		*short.WindowEnd != 1791400000000 || short.ResetBasis != "reported" || short.Aside {
		t.Fatalf("5 hours = %+v", short)
	}
	if week.TemplateRef != MeterWeek || *week.Remaining != 6000 || mcp.TemplateRef != MeterMCP || !mcp.Aside {
		t.Fatalf("week %+v mcp %+v", week, mcp)
	}
	// the endpoint's calls draw on the two plan windows, not the MCP calls
	if d := m.Supply.Endpoints[0].Draws; len(d) != 2 || d[0] != MeterShort || d[1] != MeterWeek {
		t.Fatalf("draws = %v", d)
	}
}

func TestRefusedKeyAndKeyWithoutPlan(t *testing.T) {
	_, m, fetch, _ := server(t)
	for _, key := range []string{"bad", "expired"} {
		_, err := m.Supply.Collector.Inventory(context.Background(), catalog.Session{Values: map[string]string{"apiKey": key}}, fetch)
		if !errors.Is(err, catalog.ErrSignedOut) {
			t.Fatalf("refused key %q: %v", key, err)
		}
	}
	inv, err := m.Supply.Collector.Inventory(context.Background(), catalog.Session{Values: map[string]string{"apiKey": "payg"}}, fetch)
	if err != nil || inv.Note == "" || inv.Packages[0].Kind != "payg" || len(inv.Packages[0].Resources) != 0 {
		t.Fatalf("no plan: %+v %v", inv, err)
	}
}

func TestModels(t *testing.T) {
	_, m, fetch, auth := server(t)
	ms, err := m.Supply.Collector.Models(context.Background(), catalog.Session{Values: map[string]string{"apiKey": "abc.secret"}}, fetch)
	if err != nil || len(ms) != 2 || ms[0].ID != "glm-5.3" || ms[0].Media != "text" || *auth != "Bearer abc.secret" {
		t.Fatalf("models = %+v %v (auth %q)", ms, err, *auth)
	}
}
