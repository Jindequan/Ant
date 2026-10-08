package quotasource

import (
	"strings"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/quota"
	"ant/server/internal/store"
	"ant/server/internal/supply"
)

func TestStoreMergesOnlyReportedIdentities(t *testing.T) {
	for provider, want := range map[string]string{"aliyun": "inst-1", "copilot": "copilot:u", "zai": "", "claude": ""} {
		a := quota.Account{Provider: provider, Identity: map[string]string{"aliyun": "inst-1", "copilot": "copilot:u", "zai": "cn:pro:123,456", "claude": "claude-plan"}[provider]}
		if got := reportedIdentity(a); got != want {
			t.Errorf("%s: identity %q, want %q", provider, got, want)
		}
	}
}

func TestInventoryFromAnAccount(t *testing.T) {
	reset := time.Unix(1_800_000_000, 0)
	pct := 37.5
	a := quota.Account{Provider: "zai", Mode: "coding-plan", Kind: quota.KindSubscription, Plan: "Pro", Live: true, Status: quota.StatusOK,
		Windows: []quota.Window{
			{Name: "5 hours", Used: ptr(30.0), Limit: ptr(150.0), Left: ptr(120.0), ResetsAt: &reset},
			{Name: "Week", UsedPercent: &pct, ResetsAt: &reset, ResetEstimated: true},
			{Name: "Free tokens", Model: "kimi-k3", Left: ptr(1000.0), Unit: "token"},
		},
		Groups: []quota.Group{{Title: "MCP tools", Windows: []quota.Window{{Name: "Month", Used: ptr(2.0), Limit: ptr(100.0)}}}},
	}
	inv := inventory(a)
	if inv.Identity != "" || len(inv.Packages) != 2 {
		t.Fatalf("inventory = %+v", inv)
	}
	main := inv.Packages[0]
	if main.TemplateRef != "quota/zai/coding-plan" || main.Kind != "subscription" || main.Basis != "observed" || len(main.Resources) != 3 {
		t.Fatalf("main package = %+v", main)
	}
	five, week, free := main.Resources[0], main.Resources[1], main.Resources[2]
	if five.TemplateRef != "window/5-hours" || *five.Used != 30_000_000 || *five.Limit != 150_000_000 || *five.Remaining != 120_000_000 ||
		five.ResetBasis != "reported" || *five.WindowEnd != domain.Millis(reset.UnixMilli()) || string(five.WindowJSON) != `{"kind":"unmodeled","name":"5 hours"}` {
		t.Fatalf("5h = %+v", five)
	}
	if week.Unit != "percent" || *week.Used != 37_500_000 || *week.Limit != 100_000_000 || *week.Remaining != 62_500_000 || week.ResetBasis != "computed" {
		t.Fatalf("week = %+v", week)
	}
	if string(free.ScopeJSON) != `{"kind":"model","model":"kimi-k3"}` || free.Unit != "token" || free.ResetBasis != "none" || free.Used != nil {
		t.Fatalf("free = %+v", free)
	}
	if g := inv.Packages[1]; g.TemplateRef != "quota/zai/coding-plan/mcp-tools" || g.Plan != "MCP tools" || len(g.Resources) != 1 {
		t.Fatalf("group package = %+v", g)
	}
}

func TestRuleOnlyAccountsAreDeclared(t *testing.T) {
	inv := inventory(quota.Account{Provider: "figma", Mode: "starter", Kind: quota.KindFree, Live: false, Windows: []quota.Window{{Name: "Monthly credits", Limit: ptr(500.0)}}})
	if p := inv.Packages[0]; p.Basis != "declared" || p.Kind != "free_grant" {
		t.Fatalf("package = %+v", p)
	}
}

func TestBalanceMeter(t *testing.T) {
	inv := inventory(quota.Account{Provider: "deepseek", Mode: "api", Kind: quota.KindAPI, Live: true,
		Balances: []quota.Balance{{Currency: "CNY", Amount: "-3.20", Granted: "0.00", ToppedUp: "-3.20"}}})
	p := inv.Packages[0]
	r := p.Resources[0]
	if p.Kind != "prepaid" || r.TemplateRef != "balance/cny" || r.Currency != "CNY" || *r.Remaining != -3_200_000 || r.Limit != nil ||
		!strings.Contains(string(r.Evidence), `"toppedUp":"-3.20"`) {
		t.Fatalf("balance = %+v / %+v", p, r)
	}
}

func TestInsightMatchesTheCardRow(t *testing.T) {
	at := domain.Millis(1_800_000_000_000)
	perDay := domain.Units(2_100_000)
	left := domain.Units(23_400_000)
	m := supply.MeterOutlook{
		Resource: store.SupplyResourceRecord{Unit: "CNY", Currency: "CNY", ScopeJSON: []byte(`{"kind":"all"}`)},
		Latest:   store.SupplyReadingRecord{ObservedAt: 5, Remaining: &left, EvidenceJSON: []byte(`{"label":"Main","amount":"23.40"}`)},
		Points:   []store.SupplyReadingRecord{{ObservedAt: 5, Remaining: &left}},
		Outlook:  supply.Outlook{Kind: supply.OutlookRunsOut, At: &at, PerDay: &perDay},
	}
	in := insight(m)
	if in.Currency != "CNY" || in.Label != "Main" || in.Window != "" || in.Outlook != "runs_out" || *in.PerDay != 2.1 ||
		in.At.UnixMilli() != int64(at) || len(in.Points) != 1 || in.Points[0] != [2]float64{5, 23.4} {
		t.Fatalf("balance insight = %+v", in)
	}
	w := supply.MeterOutlook{
		Resource: store.SupplyResourceRecord{Unit: "count", ScopeJSON: []byte(`{"kind":"model","model":"kimi-k3"}`)},
		Latest:   store.SupplyReadingRecord{EvidenceJSON: []byte(`{"name":"Free tokens"}`)},
		Outlook:  supply.Outlook{Kind: supply.OutlookLasts},
	}
	if in := insight(w); in.Window != "Free tokens" || in.Model != "kimi-k3" || in.Currency != "" || in.Outlook != "lasts" {
		t.Fatalf("window insight = %+v", in)
	}
}
