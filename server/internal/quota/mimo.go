package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// Xiaomi MiMo's open platform (platform.xiaomimimo.com). Its Token Plan
// and pay-as-you-go balance are read the way the platform's own console
// reads them — /api/v1/tokenPlan/usage, /tokenPlan/detail and /balance —
// with the console sign-in (cookies serviceToken and userId). The Token
// Plan's own tp- keys only serve models; they can't read usage. Shapes as
// read by dsh-cost-meter and CodexBar's MiMoUsageFetcher.

const mimoPlatform = "https://platform.xiaomimimo.com"

var mimoSignIn = &SignIn{URL: mimoPlatform + "/token-plan", Domain: "xiaomimimo.com", Done: func(cs []siteCookie) bool {
	return cookieValue(cs, "userId") != "" && (cookieValue(cs, "serviceToken") != "" || cookieValue(cs, "api-platform_serviceToken") != "")
}}

func mimoProvider() Provider {
	ways := func(mode string, read func(context.Context, Local, Connection) Account) []Way {
		return []Way{
			{ID: "browser", Name: "Your browser", Hint: "Ant reads the MiMo sign-in of Chrome, Edge, Brave, Arc or Firefox on this Mac. Sign in to platform.xiaomimimo.com there.",
				Fields: []Field{}, Discover: discoverMiMo(mode), Read: read, BrowserDomain: "xiaomimimo.com"},
			{ID: "sign-in", Name: "Sign in to Xiaomi MiMo", Hint: "Sign in to the Xiaomi MiMo platform in the window Ant opens. One sign-in connects the Token Plan and the balance; Ant never sees your password.",
				Fields: []Field{}, SignIn: mimoSignIn, Read: read},
			{ID: "cookie", Name: "Paste the cookie", Key: "cookie", Read: read,
				Hint:   "Only if signing in doesn't work: in a signed-in platform.xiaomimimo.com tab, open the developer tools and copy a request's Cookie header.",
				Fields: []Field{{ID: "cookie", Label: "Cookie", Type: "secret", Placeholder: "userId=…; serviceToken=…", Help: "It must hold userId and serviceToken."}}},
		}
	}
	return Provider{ID: "xiaomi", Name: "Xiaomi MiMo", Link: mimoPlatform + "/token-plan", Modes: []Mode{
		{
			ID: "token-plan", Name: "Token Plan", Kind: KindSubscription, Live: true,
			Rule: "Credits for the subscription period (models spend them at different rates), plus any compensation credits. They renew at the end of the period.",
			Ways: ways("token-plan", readMiMoPlan),
		},
		{
			ID: "api", Name: "API balance", Kind: KindAPI, Live: true,
			Rule: "Prepaid balance for pay-as-you-go calls; gift balance counts toward it.",
			Ways: ways("api", readMiMoBalance),
		},
	}}
}

func discoverMiMo(mode string) func(Local) []Connection {
	return func(l Local) []Connection {
		s, err := l.findBrowserSession(context.Background(), "xiaomimimo.com", "userId")
		if err != nil || !mimoSignIn.Done(s.cookies) {
			return nil
		}
		return []Connection{{ID: "xiaomi-browser-" + mode, Provider: "xiaomi", Mode: mode, Origin: OriginFound, Source: s.source}}
	}
}

var errMiMoSignedOut = errors.New("signed out")

// mimoGet reads one console API with the connection's sign-in.
func mimoGet(ctx context.Context, l Local, c Connection, path string, dst any) (string, error) {
	sess, ok := browserSession{}, false
	if ck := c.Values["cookie"]; ck != "" {
		sess, ok = browserSession{cookies: parseCookieHeader(ck, "xiaomimimo.com"), source: "Pasted cookie"}, true
	} else if c.Values["session"] != "" {
		sess, ok = signedInSession(ctx, l, *mimoSignIn, c.Values["session"], c.Values["profile"])
	}
	if !ok && c.Way == "cookie" {
		return "", errMiMoSignedOut
	}
	if !ok {
		var err error
		sess, err = l.findBrowserSession(ctx, "xiaomimimo.com", "userId")
		if err != nil {
			return "", errMiMoSignedOut
		}
	}
	b, err := l.fetch(ctx, http.MethodGet, l.url("mimo", mimoPlatform)+"/api/v1"+path, map[string]string{
		"Cookie": sess.header("platform.xiaomimimo.com"), "User-Agent": browserAgent, "Referer": mimoPlatform + "/token-plan",
	}, nil)
	if err != nil {
		if isAuthError(err) {
			return sess.source, errMiMoSignedOut
		}
		return sess.source, err
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &env) != nil {
		return sess.source, errMiMoSignedOut // a sign-in page instead of JSON
	}
	if env.Code == 401 || env.Code == 10001 {
		return sess.source, errMiMoSignedOut
	}
	if env.Code != 0 {
		return sess.source, fmt.Errorf("Xiaomi MiMo: %s", env.Msg)
	}
	if json.Unmarshal(env.Data, dst) != nil {
		return sess.source, errors.New("Xiaomi MiMo's console changed; Ant needs an update to read it")
	}
	return sess.source, nil
}

func mimoFailed(a *Account, err error) {
	a.Status, a.Message = StatusError, short(err.Error())
	if errors.Is(err, errMiMoSignedOut) {
		a.Status, a.Message = StatusSignedOut, "Your Xiaomi MiMo sign-in has ended. Sign in again."
	}
}

var mimoZone = time.FixedZone("UTC+8", 8*3600) // the console's times are Beijing time

func readMiMoPlan(ctx context.Context, l Local, c Connection) Account {
	p := mimoProvider()
	a := base(p, p.Modes[0], c, l.Now())
	var usage struct {
		MonthUsage *struct {
			Items []mimoItem `json:"items"`
		} `json:"monthUsage"`
		Usage *struct {
			Items []mimoItem `json:"items"`
		} `json:"usage"`
		Items []mimoItem `json:"items"`
	}
	src, err := mimoGet(ctx, l, c, "/tokenPlan/usage", &usage)
	if src != "" {
		a.Source = src
	}
	if err != nil {
		mimoFailed(&a, err)
		return a
	}
	items := usage.Items
	switch {
	case usage.MonthUsage != nil:
		items = usage.MonthUsage.Items
	case usage.Usage != nil:
		items = usage.Usage.Items
	}
	var detail struct {
		PlanCode         string `json:"planCode"`
		CurrentPeriodEnd string `json:"currentPeriodEnd"`
		Expired          bool   `json:"expired"`
	}
	_, _ = mimoGet(ctx, l, c, "/tokenPlan/detail", &detail)
	var ends *time.Time
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(detail.CurrentPeriodEnd), mimoZone); err == nil {
		ends = ptr(t.UTC())
	}
	a.Status, a.Plan, a.EndsAt = StatusOK, catalog.PlanName(detail.PlanCode), ends
	for _, it := range items {
		name := map[string]string{"month_total_token": "Plan credits", "plan_total_token": "Plan credits", "compensation_total_token": "Compensation credits"}[it.Name]
		if name == "" {
			name = it.Name
		}
		w := Window{Name: name, Unit: "credits", ResetsAt: ends}
		if it.Limit > 0 {
			w.Limit, w.Used = ptr(float64(it.Limit)), ptr(float64(it.Used))
			w.Left = ptr(max(0, float64(it.Limit-it.Used)))
		} else {
			w.UsedPercent = ptr(max(0, min(100, float64(it.Percent)*100)))
		}
		a.Windows = append(a.Windows, w)
	}
	if detail.Expired {
		a.Message = "The Token Plan has expired."
	} else if len(a.Windows) == 0 {
		a.Message = "This Xiaomi MiMo account has no Token Plan."
	}
	return a
}

type mimoItem struct {
	Name    string     `json:"name"`
	Used    flexNumber `json:"used"`
	Limit   flexNumber `json:"limit"`
	Percent flexNumber `json:"percent"`
}

func readMiMoBalance(ctx context.Context, l Local, c Connection) Account {
	p := mimoProvider()
	a := base(p, p.Modes[1], c, l.Now())
	var bal struct {
		Balance  flexNumber `json:"balance"`
		Currency string     `json:"currency"`
		Cash     flexNumber `json:"cashBalance"`
		Gift     flexNumber `json:"giftBalance"`
	}
	src, err := mimoGet(ctx, l, c, "/balance", &bal)
	if src != "" {
		a.Source = src
	}
	if err != nil {
		mimoFailed(&a, err)
		return a
	}
	cur := strings.ToUpper(strings.TrimSpace(bal.Currency))
	if cur == "" || cur == "RMB" {
		cur = "CNY"
	}
	a.Status = StatusOK
	a.Balances = []Balance{{Currency: cur, Amount: money2(float64(bal.Balance)), Granted: money2(float64(bal.Gift)), ToppedUp: money2(float64(bal.Cash))}}
	return a
}
