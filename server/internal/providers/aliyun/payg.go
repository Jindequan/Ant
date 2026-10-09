package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
)

// Pay-as-you-go is read through Alibaba Cloud's official billing API
// (BssOpenApi, RPC signature v1) with an AccessKey: the account balance
// Bailian charges, and what Bailian (product sfm) charged this month and
// last. A RAM user with only AliyunBSSReadOnlyAccess is enough.

// SignRPC signs an Alibaba Cloud RPC request (signature version 1.0,
// HMAC-SHA1) at time now and returns its query string.
func SignRPC(id, secret string, params map[string]string, now time.Time) (string, error) {
	if id == "" || secret == "" {
		return "", errors.New("set the AccessKey ID and secret")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	all := map[string]string{
		"Format": "JSON", "AccessKeyId": id, "SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0",
		"SignatureNonce": hex.EncodeToString(nonce), "Timestamp": now.UTC().Format("2006-01-02T15:04:05Z"),
	}
	for k, v := range params {
		all[k] = v
	}
	return RPCQuery(secret, all), nil
}

// RPCQuery is the canonical query of params followed by its signature.
func RPCQuery(secret string, all map[string]string) string {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = rpcEscape(k) + "=" + rpcEscape(all[k])
	}
	canonical := strings.Join(pairs, "&")
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte("GET&%2F&" + rpcEscape(canonical)))
	return canonical + "&Signature=" + rpcEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

// rpcEscape is RFC 3986 percent-encoding, as Alibaba Cloud signs it.
func rpcEscape(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	return strings.ReplaceAll(e, "%7E", "~")
}

// ErrKeyRefused: Alibaba Cloud refused the AccessKey (unknown, disabled,
// or a signature that doesn't match its secret).
var ErrKeyRefused = errors.New("Alibaba Cloud refused the AccessKey")

// ParseBSSReply reads a billing API reply into dst: {"Success":true,…}, or
// an error with Alibaba Cloud's {"Code","Message"}. A refused key is
// ErrKeyRefused.
func ParseBSSReply(b []byte, dst any) error {
	var r struct {
		Success bool   `json:"Success"`
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ErrChanged
	}
	if !r.Success {
		why := strings.TrimSpace(strings.TrimSuffix(r.Code+": "+r.Message, ": "))
		if strings.Contains(r.Code, "AccessKey") || strings.Contains(r.Code, "Signature") || strings.HasPrefix(r.Code, "Forbidden") {
			return fmt.Errorf("%w: %s", ErrKeyRefused, why)
		}
		return fmt.Errorf("Alibaba Cloud answered: %s", why)
	}
	if dst == nil {
		return nil
	}
	return decode(b, dst)
}

// Amount is a billing amount as Alibaba Cloud sends it: a JSON number or a
// string, maybe with thousands separators ("1,234.50"); kept as text.
type Amount string

func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.ReplaceAll(strings.Trim(string(b), `"`), ",", "")
	if s == "null" {
		s = ""
	}
	*a = Amount(s)
	return nil
}

// Units is the amount at 10^-6; ok is false when it isn't a number.
func (a Amount) Units() (domain.Units, bool) {
	if a == "" {
		return 0, true
	}
	return catalog.ParseDecimal(string(a))
}

// Int is the amount as a whole number (a timestamp in ms, a count); ok is
// false for anything else. "" is 0.
func (a Amount) Int() (int64, bool) {
	if a == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(string(a), 10, 64)
	return v, err == nil
}

// BillItem is one line of QueryBillOverview.
type BillItem struct {
	ProductCode       string `json:"ProductCode"`
	SubscriptionType  string `json:"SubscriptionType"`
	PretaxAmount      Amount `json:"PretaxAmount"`
	PretaxGrossAmount Amount `json:"PretaxGrossAmount"`
	Currency          string `json:"Currency"`
	OwnerID           string `json:"OwnerID"`
}

// BillOverview is the QueryBillOverview reply.
type BillOverview struct {
	Data struct {
		Items struct {
			Item []BillItem `json:"Item"`
		} `json:"Items"`
	} `json:"Data"`
}

// Spend is what Bailian charged pay-as-you-go in a billing cycle, and what
// that usage was worth before free quota, plans, coupons and discounts.
type Spend struct {
	Charged, Gross domain.Units // 10^-6
	Currency       string
	Owner          string
}

// SpendOf sums a cycle's Bailian pay-as-you-go lines exactly.
func SpendOf(items []BillItem) (Spend, error) {
	s := Spend{Currency: "CNY"}
	for _, it := range items {
		if it.OwnerID != "" {
			s.Owner = it.OwnerID
		}
		if it.ProductCode != "sfm" || it.SubscriptionType != "PayAsYouGo" {
			continue
		}
		charged, ok1 := it.PretaxAmount.Units()
		gross, ok2 := it.PretaxGrossAmount.Units()
		if !ok1 || !ok2 {
			return Spend{}, ErrChanged
		}
		s.Charged, s.Gross = s.Charged+charged, s.Gross+gross
		if it.Currency != "" {
			s.Currency = it.Currency
		}
	}
	return s, nil
}

// Money writes units at 10^-6 as people write money: exact, at least two
// decimals ("1.5" is "1.50", "0.0012" stays).
func Money(u domain.Units) string {
	neg := u < 0
	if neg {
		u = -u
	}
	s := fmt.Sprintf("%d.%06d", u/1_000_000, u%1_000_000)
	s = strings.TrimRight(s, "0")
	if i := strings.IndexByte(s, '.'); len(s)-i-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-i-1))
	}
	if neg {
		s = "-" + s
	}
	return s
}

// Cycles are this month's and last month's billing cycles, in Beijing time
// (Alibaba Cloud bills in it).
func Cycles(now time.Time) [2]string {
	local := now.In(time.FixedZone("CST", 8*3600))
	first := local.AddDate(0, 0, 1-local.Day())
	return [2]string{first.Format("2006-01"), first.AddDate(0, -1, 0).Format("2006-01")}
}

// bss calls one billing API action with the session's AccessKey.
func (c collector) bss(ctx context.Context, s catalog.Session, fetch observed.Fetch, action string, params map[string]string, dst any) error {
	endpoint := c.b.Business
	if s.Values["site"] == "intl" || s.Values["site"] == "international" {
		endpoint = c.b.BusinessIntl
	}
	p := map[string]string{"Action": action, "Version": "2017-12-14"}
	for k, v := range params {
		p[k] = v
	}
	q, err := SignRPC(strings.TrimSpace(s.Values["accessKeyId"]), strings.TrimSpace(s.Values["accessKeySecret"]), p, s.Now)
	if err != nil {
		return fmt.Errorf("%w: Add the AccessKey ID and secret.", catalog.ErrSignedOut)
	}
	res, err := fetch(ctx, observed.Request{URL: endpoint + "/?" + q, Header: map[string]string{"Accept": "application/json"}})
	if err != nil {
		return fmt.Errorf("Couldn't reach Alibaba Cloud's billing: %v", err)
	}
	err = ParseBSSReply(res.Body, dst)
	if errors.Is(err, ErrKeyRefused) {
		return fmt.Errorf("%w: %s. Check the AccessKey (a RAM user with AliyunBSSReadOnlyAccess is enough).", catalog.ErrSignedOut, strings.TrimRight(err.Error(), "."))
	}
	if err == nil && (res.Status < 200 || res.Status > 299) {
		return fmt.Errorf("Alibaba Cloud's billing answered %d", res.Status)
	}
	return err
}

// payg reads the balance and the two months' charges.
func (c collector) payg(ctx context.Context, s catalog.Session, fetch observed.Fetch) (observed.Inventory, error) {
	var bal struct {
		Data struct {
			AvailableAmount Amount `json:"AvailableAmount"`
			Currency        string `json:"Currency"`
		} `json:"Data"`
	}
	if err := c.bss(ctx, s, fetch, "QueryAccountBalance", nil, &bal); err != nil {
		return observed.Inventory{}, err
	}
	avail, ok := bal.Data.AvailableAmount.Units()
	if !ok {
		return observed.Inventory{}, ErrChanged
	}
	cur := strings.ToUpper(firstOf(bal.Data.Currency, "CNY"))
	main := observed.PackageReading{TemplateRef: "aliyun/payg", Kind: "payg", Basis: "observed", Resources: []observed.ResourceReading{
		money("balance/"+strings.ToLower(cur), "Balance", cur, avail, false)}}
	inv := observed.Inventory{Packages: []observed.PackageReading{main}}

	charged := observed.PackageReading{TemplateRef: "aliyun/payg-charged", Kind: "payg", Basis: "observed", Plan: "Charged by Bailian"}
	var worth, notes []string
	for i, cycle := range Cycles(s.Now) {
		label, ref := [2]string{"This month", "Last month"}[i], [2]string{"charged/this-month", "charged/last-month"}[i]
		var bill BillOverview
		if err := c.bss(ctx, s, fetch, "QueryBillOverview", map[string]string{"BillingCycle": cycle}, &bill); err != nil {
			notes = append(notes, "Couldn't read the bill: "+err.Error())
			break
		}
		sp, err := SpendOf(bill.Data.Items.Item)
		if err != nil {
			notes = append(notes, "Couldn't read the bill: "+err.Error())
			break
		}
		if sp.Owner != "" {
			inv.Identity = "payg:" + sp.Owner // the Alibaba Cloud account
		}
		charged.Resources = append(charged.Resources, money(ref, label, sp.Currency, sp.Charged, true))
		if sp.Gross > sp.Charged {
			worth = append(worth, strings.ToLower(label)+" ¥"+Money(sp.Gross))
		}
	}
	if len(charged.Resources) > 0 {
		inv.Packages = append(inv.Packages, charged)
	}
	if len(worth) > 0 {
		notes = append(notes, "Usage worth "+strings.Join(worth, " and ")+" before free quota, plans and discounts.")
	}
	inv.Note = strings.Join(notes, " ")
	return inv, nil
}

// money is an amount at 10^-6; charged months are aside: what was spent,
// never something a call draws on.
func money(ref, label, currency string, amount domain.Units, aside bool) observed.ResourceReading {
	return observed.ResourceReading{TemplateRef: ref, Label: label, Aside: aside, MetricID: "balance", Unit: currency, Currency: currency, QuantumExp: -6,
		SemanticsRef: "sem.entitlement-pool", SemanticsRevision: "semrev-1", WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: []byte(`{"kind":"all"}`),
		ResetBasis: "none", Remaining: &amount}
}

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
