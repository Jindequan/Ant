package quotasource

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/providers/zai"
	"ant/server/internal/quota"
	"ant/server/internal/runtime"
	"ant/server/internal/store/sqlite"
	"ant/server/internal/supply"
	"ant/server/internal/transport"
)

func TestDecimalIsExact(t *testing.T) {
	for in, want := range map[[2]int64]string{{2340, -2}: "23.40", {5, -2}: "0.05", {-1500, -2}: "-15.00", {7, 0}: "7", {3, 2}: "300", {0, -2}: "0.00"} {
		if got := decimal(domain.Units(in[0]), int(in[1])); got != want {
			t.Errorf("decimal(%d, %d) = %s, want %s", in[0], in[1], got, want)
		}
	}
}

// Z.ai and DeepSeek are read through their modules: the cards, the stored
// facts, and Z.ai's display-only grouping of two keys of one plan.
func TestNativeModulesReadZaiAndDeepSeek(t *testing.T) {
	zaiSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/coding/paas/v4/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"level":"pro","limits":[
			{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":12,"nextResetTime":1791400000000},
			{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":40,"nextResetTime":1791900000000}]}}`))
	}))
	defer zaiSrv.Close()
	dsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-v4"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"23.40","granted_balance":"10.00","topped_up_balance":"13.40"}]}`))
	}))
	defer dsSrv.Close()

	zm, dm := zai.Module(zaiSrv.URL), deepseek.SupplyModule(dsSrv.URL)
	zf, _ := transport.NewObservationFetch(zm.Supply.Origins, zaiSrv.Client().Transport)
	df, _ := transport.NewObservationFetch(dm.Supply.Origins, dsSrv.Client().Transport)

	home := t.TempDir()
	env := map[string]string{"ZAI_API_KEY": "key-one", "Z_AI_API_KEY": "key-two", "DEEPSEEK_API_KEY": "sk-test"}
	// the quota readers' own client reaches nothing: a card can only be OK through its module
	local := quota.Local{Home: home, Getenv: func(k string) string { return env[k] }, HTTP: &http.Client{Transport: loopbackOnly{}}, Now: time.Now, URLs: map[string]string{}}
	var providers []quota.Provider
	for _, p := range quota.Providers() {
		if p.ID == "zai" || p.ID == "deepseek" {
			providers = append(providers, p)
		}
	}
	accounts := quota.NewStore("")
	src := New(local, accounts, providers, Native{Module: zm, Fetch: zf}, Native{Module: dm, Fetch: df})
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	engine := supply.NewEngine("local", st, src, runtime.NewClock(), supply.DefaultPolicy(), nil)
	page := quota.NewService(local, accounts, quota.Options{Reader: Reader{Source: src, Engine: engine}})

	snap := page.Refresh(context.Background())
	var z, d *quota.Account
	for i, a := range snap.Accounts {
		switch a.Provider {
		case "zai":
			z = &snap.Accounts[i]
		case "deepseek":
			d = &snap.Accounts[i]
		}
	}
	if len(snap.Accounts) != 2 || z == nil || d == nil {
		t.Fatalf("cards = %+v", snap.Accounts)
	}
	if z.Status != quota.StatusOK || z.Plan != "Pro" || len(z.Windows) != 2 || *z.Windows[0].UsedPercent != 12 || z.Windows[0].Name != "5 hours" ||
		len(z.Keys) != 2 || len(z.Models) != 1 || z.Models[0].ID != "glm-5.3" {
		t.Fatalf("Z.ai card = %+v", z)
	}
	if d.Status != quota.StatusOK || len(d.Balances) != 1 || d.Balances[0] != (quota.Balance{Currency: "CNY", Amount: "23.40", Granted: "10.00", ToppedUp: "13.40"}) || d.Models[0].ID != "deepseek-v4" {
		t.Fatalf("DeepSeek card = %+v", d)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count := func(q string) int {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// two keys, two stored accounts: grouping on the page is display only
	if n := count(`SELECT COUNT(*) FROM supply_accounts WHERE provider_id='zai'`); n != 2 {
		t.Fatalf("Z.ai stored accounts = %d, want 2", n)
	}
	if n := count(`SELECT COUNT(*) FROM supply_readings r JOIN supply_resources s USING(namespace,resource_id) WHERE s.template_ref='window/short' AND s.unit='percent' AND r.remaining_units=8800`); n != 2 {
		t.Fatalf("Z.ai 5-hour readings = %d, want 2 (one per key)", n)
	}
	if n := count(`SELECT COUNT(*) FROM supply_readings r JOIN supply_resources s USING(namespace,resource_id) WHERE s.template_ref='balance/cny' AND s.quantum_exp=-2 AND r.remaining_units=2340`); n != 1 {
		t.Fatalf("DeepSeek balance readings = %d, want 1", n)
	}
}
