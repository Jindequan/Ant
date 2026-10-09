package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/providers/aliyun"
	"ant/server/internal/providers/catalog"
)

// Alibaba Cloud Bailian (百炼). Everything Bailian shows on its Token Plan
// page is read the way that page reads it: with the user's aliyun.com
// sign-in from their browser, through the console's own read-only calls
// (docs/research/ALIYUN-BAILIAN.md). What the console guards with its
// anti-bot layer — the new-user free quota list — Ant does not read.
//
// The account balance can also be read without a browser, through the
// official BssOpenApi QueryAccountBalance with an AccessKey (the aliyun
// CLI's, or ALIBABA_CLOUD_ACCESS_KEY_*).

const (
	bailianConsole    = "https://bailian.console.aliyun.com"
	bailianGateway    = "https://bailian-cs.console.aliyun.com"
	bailianPlanPage   = "/cn-beijing/subscription/token-plan/personal"
	bailianFreeQuota  = "https://bailian.console.aliyun.com/cn-beijing/costing-balance/free-quota"
	aliyunLoginMarker = "login_aliyunid_pk"
)

// aliyunSignIn is the Alibaba Cloud sign-in every console instance reads with.
// It is Bailian's own console-login for programs, in the user's browser;
// Domain and Done still recognise an everyday browser's aliyun.com session.
var aliyunSignIn = &SignIn{URL: bailianLoginPage, Browser: true, Login: aliyunLogin,
	Domain: "aliyun.com", Session: []string{"login_aliyunid_ticket"}, Done: func(cs []siteCookie) bool { return cookieValue(cs, "login_aliyunid_ticket") != "" }}

const aliyunConnect = "Sign in with Alibaba Cloud in your browser, the way Bailian's own CLI signs in. One sign-in connects the Token Plan and the free quota; Ant never sees your password."

// Each Bailian instance is its own account with its own reader: the Token
// Plan subscription, pay-as-you-go spending, and the new-user free quota.
// One Alibaba Cloud sign-in authorizes them all.
func aliyunProvider() Provider {
	signIn := func(mode string, read func(context.Context, Local, Connection) Account) Way {
		w := Way{ID: "sign-in", Name: "Sign in with Alibaba Cloud", Hint: aliyunConnect, SignIn: aliyunSignIn, Fields: []Field{}, Read: read, Resolve: resolveSignIn}
		if mode != "free-quota" {
			w.Discover = discoverBailian(mode)
		}
		if mode == "payg" {
			// the console token Ant's sign-in yields doesn't open bills: a
			// sign-in never connects pay-as-you-go; the browser's own
			// aliyun.com sign-in (found) and an AccessKey do
			w.SignIn, w.Name = nil, "Your browser's Alibaba Cloud sign-in"
			w.Hint = "Ant reads the aliyun.com sign-in of your browser to open your bill. Or add a read-only AccessKey."
		}
		return w
	}
	bl := func(mode string, read func(context.Context, Local, Connection) Account) Way {
		return Way{ID: "bl", Name: "Bailian CLI", Hint: "Ant reads the Bailian CLI's sign-in (~/.bailian). Run bl login.",
			Fields: []Field{}, Discover: discoverBLConfig(mode), Read: withBLConfig(read), Resolve: resolveBL}
	}
	return Provider{ID: "aliyun", Name: "Alibaba Cloud Bailian", Link: bailianConsole + bailianPlanPage, Media: []string{"text", "image", "video", "speech"}, Modes: []Mode{
		{
			ID: "token-plan", Name: "Token Plan", Kind: KindSubscription, Live: true, Media: []string{"text"},
			Rule: "Credits for each window the plan has (Bailian reports a month, and a week or 5 hours where the tier has them), add-on credits, and the plan's tool benefits. It ends on its end date unless auto-renew is on.",
			Ways: []Way{bl("token-plan", readTokenPlan), signIn("token-plan", readTokenPlan)},
		},
		{
			ID: "payg", Name: "Pay-as-you-go", Kind: KindAPI, Live: true,
			Rule: "What Bailian charged your Alibaba Cloud balance this month and last, from the official bill, and the balance it draws on. It is charged only after free quota, resource packs and savings plans.",
			Ways: []Way{
				{ID: "aliyun-cli", Name: "Alibaba Cloud CLI", Hint: "Ant reads the AccessKey the aliyun CLI holds (~/.aliyun).", Fields: []Field{}, Discover: discoverAliyunAccessKey, Read: readBailianPayg},
				bl("payg", readBailianPayg),
				{ID: "accesskey", Name: "AccessKey", Key: "accessKeyId", Read: readBailianPayg,
					Hint: "Bills are read through Alibaba Cloud's billing API. Use a RAM user that has only AliyunBSSReadOnlyAccess.",
					Fields: []Field{
						{ID: "accessKeyId", Label: "AccessKey ID", Type: "text", Placeholder: "LTAI…"},
						{ID: "accessKeySecret", Label: "AccessKey secret", Type: "secret"},
						{ID: "site", Label: "Site", Type: "select", Optional: true, Options: []Option{{"china", "aliyun.com (China)"}, {"intl", "alibabacloud.com (International)"}}},
					}},
				signIn("payg", readBailianPayg),
			},
		},
		{
			ID: "free-quota", Name: "New-user free quota", Kind: KindFree, Live: true,
			Rule: "Each model has its own grant (tokens for text models; images, seconds or calls for others), valid 90 days from activation, Beijing region only. It is spent before anything is charged.",
			Ways: []Way{bl("free-quota", readBailianFreeQuota), signIn("free-quota", readBailianFreeQuota)},
		},
	}}
}

// resolveSignIn gives a sign-in's values with the site as bl names it:
// unset for the China site (the console-login page says "domestic"),
// "international" otherwise.
func resolveSignIn(_ Local, c Connection) (map[string]string, error) {
	v := map[string]string{}
	for k, x := range c.Values {
		v[k] = x
	}
	switch v["site"] {
	case "domestic", "china":
		delete(v, "site")
	case "intl":
		v["site"] = "international"
	}
	return v, nil
}

// --- the console, through the browser's sign-in ---

// discoverBailian finds an everyday browser's Alibaba Cloud sign-in, for the
// instance mode.
func discoverBailian(mode string) func(Local) []Connection {
	return func(l Local) []Connection {
		s, err := l.findBrowserSession(context.Background(), "aliyun.com", aliyunLoginMarker)
		if errors.Is(err, errNoCookie) {
			return nil
		}
		c := Connection{ID: "aliyun-browser-" + mode, Provider: "aliyun", Mode: mode, Origin: OriginFound, Source: s.source}
		if err != nil {
			c.Source, c.Values = "browser", map[string]string{"error": short(err.Error())}
		}
		return []Connection{c}
	}
}

// bailianSession is a signed-in console session: the browser's cookies and
// the console's CSRF token.
type bailianSession struct {
	l         Local
	cookies   browserSession
	secToken  string
	console   string
	gatewayAt string
}

var errBailianSignedOut = errors.New("signed out")

var secTokenRE = regexp.MustCompile(`SEC_TOKEN:\s*"([^"]+)"`)

// openBailian opens the console with the session signed in through Ant's
// window, or else the one found in the browser.
func openBailian(ctx context.Context, l Local, c Connection) (*bailianSession, error) {
	cookies, ok := browserSession{}, false
	if c.Values["session"] != "" {
		cookies, ok = signedInSession(ctx, l, *aliyunSignIn, c.Values["session"], c.Values["profile"])
	}
	if !ok {
		var err error
		cookies, err = l.findBrowserSession(ctx, "aliyun.com", aliyunLoginMarker)
		if errors.Is(err, errNoCookie) {
			return nil, errBailianSignedOut
		}
		if err != nil {
			return nil, err
		}
	}
	s := &bailianSession{l: l, cookies: cookies, console: l.url("aliyun.console", bailianConsole), gatewayAt: l.url("aliyun.gateway", bailianGateway)}
	page := s.console + bailianPlanPage
	b, err := l.fetch(ctx, http.MethodGet, page, map[string]string{
		"Cookie": cookies.header(hostOf(bailianConsole)), "Accept": "text/html", "User-Agent": browserAgent,
	}, nil)
	if err != nil {
		if isAuthError(err) {
			return nil, errBailianSignedOut
		}
		return nil, err
	}
	// a page without the token is the sign-in page: the session has ended
	m := secTokenRE.FindSubmatch(b)
	if m == nil {
		return nil, errBailianSignedOut
	}
	s.secToken = string(m[1])
	return s, nil
}

// post sends a console call to endpoint, with the cookies the browser would
// send to host.
func (s *bailianSession) post(ctx context.Context, endpoint, host string, form url.Values) ([]byte, error) {
	form.Set("sec_token", s.secToken)
	form.Set("region", "cn-beijing")
	return s.l.fetch(ctx, http.MethodPost, endpoint, map[string]string{
		"Cookie":       s.cookies.header(host),
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       bailianConsole,
		"Referer":      bailianConsole + bailianPlanPage,
		"User-Agent":   browserAgent,
	}, strings.NewReader(form.Encode()))
}

// consoleEnvelope is what every console call answers: code "200" and the
// call's own data, or a code naming why not.
type consoleEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e consoleEnvelope) err() error {
	switch {
	case e.Code == "200":
		return nil
	case strings.Contains(strings.ToLower(e.Code), "login"):
		return errBailianSignedOut
	}
	return fmt.Errorf("Bailian answered %s %s", e.Code, e.Message)
}

// gateway calls one of the console's gateway APIs and returns its data.
func (s *bailianSession) gateway(ctx context.Context, api string, data map[string]any, dst any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["cornerstoneParam"] = map[string]any{}
	params, _ := json.Marshal(map[string]any{"Api": api, "V": "1.0", "Data": data})
	endpoint := s.gatewayAt + "/data/api.json?action=BroadScopeAspnGateway&product=sfm_bailian&api=" + url.QueryEscape(api) + "&_v="
	b, err := s.post(ctx, endpoint, hostOf(bailianGateway), url.Values{"params": {string(params)}})
	if err != nil {
		return err
	}
	var env consoleEnvelope
	if json.Unmarshal(b, &env) != nil {
		return errBailianChanged
	}
	if err := env.err(); err != nil {
		return err
	}
	var gw struct {
		Success  *bool  `json:"success"`
		ErrorMsg string `json:"errorMsg"`
		DataV2   *struct {
			Data struct {
				Success bool            `json:"success"`
				Data    json.RawMessage `json:"data"`
			} `json:"data"`
		} `json:"DataV2"`
	}
	if json.Unmarshal(env.Data, &gw) != nil {
		return errBailianChanged
	}
	if gw.DataV2 == nil || !gw.DataV2.Data.Success {
		if gw.ErrorMsg != "" {
			return fmt.Errorf("Bailian: %s", gw.ErrorMsg)
		}
		return errBailianChanged
	}
	if dst == nil || len(gw.DataV2.Data.Data) == 0 || string(gw.DataV2.Data.Data) == "null" {
		return nil
	}
	if json.Unmarshal(gw.DataV2.Data.Data, dst) != nil {
		return errBailianChanged
	}
	return nil
}

// openAPI calls an Alibaba Cloud OpenAPI action through the console.
func (s *bailianSession) openAPI(ctx context.Context, product, action string, params map[string]any, dst any) error {
	if params == nil {
		params = map[string]any{}
	}
	p, _ := json.Marshal(params)
	endpoint := s.console + "/data/api.json?action=" + url.QueryEscape(action) + "&product=" + url.QueryEscape(product) + "&_tag="
	b, err := s.post(ctx, endpoint, hostOf(bailianConsole), url.Values{"product": {product}, "action": {action}, "params": {string(p)}})
	if err != nil {
		return err
	}
	var env consoleEnvelope
	if json.Unmarshal(b, &env) != nil {
		return errBailianChanged
	}
	if err := env.err(); err != nil {
		return err
	}
	if json.Unmarshal(env.Data, dst) != nil {
		return errBailianChanged
	}
	return nil
}

// errBailianChanged: a reply Ant doesn't understand (the module's sentinel).
var errBailianChanged = aliyun.ErrChanged

// openFor opens the console for connection c, or fills a with why not.
func openFor(ctx context.Context, l Local, c Connection, a *Account) *bailianSession {
	if e := c.Values["error"]; e != "" {
		a.Status, a.Message = StatusError, e
		return nil
	}
	s, err := openBailian(ctx, l, c)
	if err != nil {
		bailianFailed(a, err)
		return nil
	}
	a.Source = s.cookies.source
	return s
}

func bailianFailed(a *Account, err error) {
	a.Status, a.Message = StatusError, short(err.Error())
	if errors.Is(err, errBailianSignedOut) {
		a.Status, a.Message = StatusSignedOut, "Your Alibaba Cloud sign-in has ended. Sign in again."
	}
}

// readTokenPlan reads the Token Plan through the console (a browser
// session or the CLI token), with the module's parsers.
func readTokenPlan(ctx context.Context, l Local, c Connection) Account {
	p := aliyunProvider()
	a := base(p, p.Modes[0], c, l.Now())
	s := bailianClient(ctx, l, c, &a)
	if s == nil {
		return a
	}
	if c.Values["token"] != "" {
		a.Source = "Signed in with Alibaba Cloud"
	}
	var sub aliyun.Subscription
	if err := s.gateway(ctx, aliyun.APISubscription, nil, &sub); err != nil {
		bailianFailed(&a, err)
		return a
	}
	a.Status = StatusOK
	if sub.InstanceCode == "" {
		a.Message = "This Alibaba Cloud account has no Token Plan."
		return a
	}
	a.Identity = sub.InstanceCode
	a.Plan = catalog.PlanName(sub.SpecCode)
	a.AutoRenew = sub.AutoRenewFlag
	if end, err := sub.EndTime.Int64(); err == nil && end > 0 {
		t := time.UnixMilli(end).UTC()
		a.EndsAt = &t
		if sub.AutoRenewFlag != nil && *sub.AutoRenewFlag {
			a.RenewsAt = &t
		}
	}
	if !strings.EqualFold(sub.Status, "VALID") && sub.Status != "" {
		a.Message = "The Token Plan is " + strings.ToLower(sub.Status) + "."
	}
	ending := sub.AutoRenewFlag != nil && !*sub.AutoRenewFlag

	var usage map[string]json.RawMessage
	if err := s.gateway(ctx, aliyun.APIUsage, nil, &usage); err != nil {
		a.Message = "Couldn't read usage: " + short(err.Error())
	}
	for _, w := range aliyun.ParseUsage(usage) {
		f, err := strconv.ParseFloat(w.Used, 64)
		if err != nil {
			continue
		}
		win := Window{Name: w.Name, UsedPercent: ptr(f * 100)}
		if w.ResetsAt > 0 {
			win.ResetsAt = ptr(time.UnixMilli(w.ResetsAt).UTC())
		}
		a.Windows = append(a.Windows, win)
	}
	var addons aliyun.Addons
	if err := s.gateway(ctx, aliyun.APIAddons, nil, &addons); err == nil {
		total, _ := addons.TotalCredits.Float64()
		left, _ := addons.RemainingCredits.Float64()
		if total > 0 {
			a.Windows = append(a.Windows, Window{Name: "Add-on credits", Left: ptr(left), Limit: ptr(total), Unit: "credits"})
		}
	}

	tools := Group{Title: "Tool benefits", Note: aliyun.ToolsNote}
	var equity aliyun.Equities
	if err := s.gateway(ctx, aliyun.APIEquity,
		map[string]any{"queryTokenPlanEquityInfoRequest": map[string]any{"requestId": aliyun.RequestID()}}, &equity); err != nil {
		tools.Note = "Couldn't read tool benefits: " + short(err.Error())
	}
	for _, e := range equity.Infos {
		name, unit := aliyun.Tool(e.EquityType)
		left, _ := e.AvailableQuota.Float64()
		total, _ := e.TotalQuota.Float64()
		w := Window{Name: name, Left: ptr(left), Limit: ptr(total), Unit: unit, Expires: ending}
		if end, err := e.InstanceEndTime.Int64(); err == nil && end > 0 {
			w.ResetsAt = ptr(time.UnixMilli(end).UTC())
		}
		tools.Windows = append(tools.Windows, w)
	}
	sort.SliceStable(tools.Windows, func(i, j int) bool { return tools.Windows[i].Name < tools.Windows[j].Name })
	a.Groups = []Group{tools}
	return a
}
