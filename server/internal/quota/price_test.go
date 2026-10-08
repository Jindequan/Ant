package quota

import "testing"

func TestPricesAreCheckedAndKept(t *testing.T) {
	l := testLocal(t, nil)
	store := NewStore(l.AccountsFile())
	h := newTestService(l, store).Handler()
	for body, want := range map[string]int{
		`{"amount":"20","currency":"usd","period":"month"}`:     200,
		`{"amount":"19.999","currency":"USD","period":"month"}`: 400,
		`{"amount":"0","currency":"USD","period":"month"}`:      400,
		`{"amount":"20","currency":"dollars","period":"month"}`: 400,
		`{"amount":"20","currency":"USD","period":"week"}`:      400,
	} {
		if code, _ := call(t, h, "PUT", "/api/prices/claude-code", body, nil); code != want {
			t.Errorf("%s: %d, want %d", body, code, want)
		}
	}
	code, out := call(t, h, "GET", "/api/quota", "", nil)
	p, _ := out["prices"].(map[string]any)["claude-code"].(map[string]any)
	if code != 200 || p["amount"] != "20" || p["currency"] != "USD" || p["period"] != "month" {
		t.Fatalf("snapshot prices: %v", out["prices"])
	}
	if code, out := call(t, h, "DELETE", "/api/prices/claude-code", "", nil); code != 200 || len(out["prices"].(map[string]any)) != 0 {
		t.Fatalf("delete: %d %v", code, out["prices"])
	}
}
