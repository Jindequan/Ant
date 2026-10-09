package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/transport"
)

func TestRPCSignatureMatchesTheDocumentedExample(t *testing.T) {
	// Alibaba Cloud's signature v1 example (DescribeRegions, secret "testsecret").
	q := RPCQuery("testsecret", map[string]string{
		"AccessKeyId": "testid", "Action": "DescribeRegions", "Format": "XML", "SignatureMethod": "HMAC-SHA1",
		"SignatureNonce": "3ee8c1b8-83d3-44af-a94f-4e0ad82fd6cf", "SignatureVersion": "1.0",
		"Timestamp": "2016-02-23T12:46:24Z", "Version": "2014-05-26",
	})
	v, _ := url.ParseQuery(q)
	if v.Get("Signature") != "OLeaidS1JvxuMvnyHOwuJ+uX5qY=" {
		t.Fatalf("signature = %q", v.Get("Signature"))
	}
}

// fakeBilling answers BssOpenApi for AccessKey "LTAIx" / secret "s",
// checking each request's signature and timestamp.
func fakeBilling(t *testing.T, at time.Time) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var actions []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		all := map[string]string{}
		for k := range q {
			if k != "Signature" {
				all[k] = q.Get(k)
			}
		}
		signed, _ := url.ParseQuery(RPCQuery("s", all))
		if q.Get("AccessKeyId") != "LTAIx" || signed.Get("Signature") != q.Get("Signature") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(fixture(t, "bss-refused.json"))
			return
		}
		if q.Get("Timestamp") != at.UTC().Format("2006-01-02T15:04:05Z") || q.Get("Version") != "2017-12-14" {
			t.Errorf("timestamp %q version %q", q.Get("Timestamp"), q.Get("Version"))
		}
		mu.Lock()
		actions = append(actions, q.Get("Action")+" "+q.Get("BillingCycle"))
		mu.Unlock()
		switch q.Get("Action") {
		case "QueryAccountBalance":
			_, _ = w.Write(fixture(t, "bss-balance.json"))
		case "QueryBillOverview":
			if q.Get("BillingCycle") == "2026-09" {
				_, _ = w.Write(fixture(t, "bss-bill-2026-09.json"))
				return
			}
			_, _ = w.Write(fixture(t, "bss-bill-empty.json"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &actions
}

func TestPaygInventory(t *testing.T) {
	// 01:30 on Oct 1 in Beijing is still Sep 30 in UTC: the cycles follow Beijing
	at := time.Date(2026, 9, 30, 17, 30, 0, 0, time.UTC)
	srv, actions := fakeBilling(t, at)
	m := Module(Bases{Gateway: "https://gw.example", GatewayIntl: "https://gw-intl.example", TokenPlan: "https://tp.example", Business: srv.URL, BusinessIntl: "https://intl.example"})
	fetch, err := transport.NewObservationFetch(m.Supply.Origins, srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	s := catalog.Session{Product: ProductPayg, Values: map[string]string{"accessKeyId": "LTAIx", "accessKeySecret": "s", "site": "china"}, Now: at}
	inv, err := m.Supply.Collector.Inventory(context.Background(), s, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*actions, ",") != "QueryAccountBalance ,QueryBillOverview 2026-10,QueryBillOverview 2026-09" {
		t.Fatalf("actions = %v", *actions)
	}
	if inv.Identity != "payg:1400" || len(inv.Packages) != 2 || inv.Note != "Usage worth last month ¥12.304321 before free quota, plans and discounts." {
		t.Fatalf("inventory = %+v", inv)
	}
	bal := inv.Packages[0]
	if bal.Kind != "payg" || len(bal.Resources) != 1 || bal.Resources[0].TemplateRef != "balance/cny" || *bal.Resources[0].Remaining != 1_234_500_000 || bal.Resources[0].Aside {
		t.Fatalf("balance = %+v", bal)
	}
	ch := inv.Packages[1]
	// 1.5 + 0.004321, exact; the subscription and the domain aren't Bailian pay-as-you-go
	if ch.Plan != "Charged by Bailian" || len(ch.Resources) != 2 || ch.Resources[0].Label != "This month" || *ch.Resources[0].Remaining != 0 ||
		ch.Resources[1].Label != "Last month" || *ch.Resources[1].Remaining != 1_504_321 || !ch.Resources[1].Aside {
		t.Fatalf("charged = %+v", ch)
	}
	if got := catalog.Offers(m.Supply, s, nil); len(got) != 0 {
		t.Fatalf("pay-as-you-go offers = %+v (an AccessKey lists no models)", got)
	}
}

func TestPaygRefusedKey(t *testing.T) {
	at := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	srv, _ := fakeBilling(t, at)
	m := Module(Bases{Gateway: "https://gw.example", GatewayIntl: "https://gw-intl.example", TokenPlan: "https://tp.example", Business: srv.URL, BusinessIntl: "https://intl.example"})
	fetch, _ := transport.NewObservationFetch(m.Supply.Origins, srv.Client().Transport)
	for _, v := range []map[string]string{
		{"accessKeyId": "LTAIother", "accessKeySecret": "s"},
		{"accessKeyId": "LTAIx", "accessKeySecret": "wrong"}, // a signature that doesn't match
	} {
		_, err := m.Supply.Collector.Inventory(context.Background(), catalog.Session{Product: ProductPayg, Values: v, Now: at}, fetch)
		if !errors.Is(err, catalog.ErrSignedOut) || !strings.Contains(err.Error(), "InvalidAccessKeyId.NotFound") {
			t.Fatalf("%v: %v", v, err)
		}
	}
}

func TestSpendAndMoney(t *testing.T) {
	var items []BillItem
	if err := json.Unmarshal([]byte(`[{"ProductCode":"sfm","SubscriptionType":"PayAsYouGo","PretaxAmount":"1,000.10","PretaxGrossAmount":2000,"OwnerID":"9"}]`), &items); err != nil {
		t.Fatal(err)
	}
	sp, err := SpendOf(items)
	if err != nil || sp.Charged != 1_000_100_000 || sp.Gross != 2_000_000_000 || sp.Owner != "9" || sp.Currency != "CNY" {
		t.Fatalf("spend = %+v %v", sp, err)
	}
	if _, err := SpendOf([]BillItem{{ProductCode: "sfm", SubscriptionType: "PayAsYouGo", PretaxAmount: "1e3"}}); !errors.Is(err, ErrChanged) {
		t.Fatalf("exponent: %v", err)
	}
	for in, want := range map[domain.Units]string{0: "0.00", 1_500_000: "1.50", 1_504_321: "1.504321", 1_200: "0.0012", -2_500_000: "-2.50", 1_234_500_000: "1234.50"} {
		if got := Money(in); got != want {
			t.Errorf("Money(%d) = %q, want %q", in, got, want)
		}
	}
	if c := Cycles(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)); c != [2]string{"2026-01", "2025-12"} {
		t.Errorf("cycles = %v", c)
	}
}
