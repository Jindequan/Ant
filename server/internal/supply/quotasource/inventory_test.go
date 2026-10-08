package quotasource

import (
	"strings"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/quota"
)

func TestDecimalUnitsIsExact(t *testing.T) {
	cases := map[string]int64{"23.40": 23_400_000, "0": 0, "-1.5": -1_500_000, "0.0000004": 0, "0.0000005": 1, "12": 12_000_000, "999999999999.999999": 999_999_999_999_999_999}
	for in, want := range cases {
		if got := decimalUnits(in); got == nil || int64(*got) != want {
			t.Errorf("%s → %v, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.2.3", "1e5", ".5", "1234567890123.0"} {
		if got := decimalUnits(bad); got != nil {
			t.Errorf("%q → %d, want nil", bad, *got)
		}
	}
}

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
