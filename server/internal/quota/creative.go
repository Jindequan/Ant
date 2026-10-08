package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// Image and video sites with free allowances. Each reads its own account
// endpoint, the one its own pages read, with the user's sign-in (webSite),
// or with the provider's official API key where one exists. Endpoints were
// read from each site's own JavaScript.

// webGet reads a site's JSON with the session's cookies.
func webGet(ctx context.Context, l Local, s browserSession, method, u, host, referer string, h map[string]string, body []byte) ([]byte, error) {
	headers := map[string]string{"Cookie": s.header(host), "User-Agent": browserAgent, "Accept": "application/json", "Referer": referer}
	if o, err := url.Parse(referer); err == nil {
		headers["Origin"] = o.Scheme + "://" + o.Host
	}
	for k, v := range h {
		headers[k] = v
	}
	var r *bytes.Reader
	if body != nil {
		headers["Content-Type"] = "application/json"
		r = bytes.NewReader(body)
	}
	if r == nil {
		return l.fetch(ctx, method, u, headers, nil)
	}
	return l.fetch(ctx, method, u, headers, r)
}

// webFailed fills a for a failed read: a refusal is a sign-in that ended.
func webFailed(site *webSite, a *Account, c Connection, err error) {
	if errors.Is(err, errWebSignedOut) || isAuthError(err) {
		site.signedOut(a, c)
		return
	}
	a.Status, a.Message = StatusError, "Couldn't read "+site.name+": "+short(err.Error())
}

// utcMidnight is the next 00:00 UTC, when many daily allowances renew.
func utcMidnight(now time.Time) time.Time {
	y, m, d := now.UTC().Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
}

// --- Kling AI: credits in lots, each with its own end ---

var klingSite = newWebSite("kling", "Kling AI", "klingai.com", "klingai.com", "", "https://klingai.com/app/",
	"in a signed-in klingai.com tab, open the developer tools, and copy the Cookie header of a request to klingai.com/api/.")

func klingProvider() Provider {
	return Provider{ID: "kling", Name: "Kling AI", Link: "https://klingai.com/app/membership/membership-plan", Media: []string{"video", "image"}, Modes: []Mode{{
		ID: "credits", Name: "Credits", Kind: KindFree, Live: true,
		Rule: "Credits in lots, each ending on its own date: the daily free credits for signing in, membership credits for the month, bought packs. Video and images spend them by model and length.",
		Ways: klingSite.ways("credits", readKling),
	}}}
}

func readKling(ctx context.Context, l Local, c Connection) Account {
	p := klingProvider()
	a := base(p, p.Modes[0], c, l.Now())
	s, err := klingSite.session(ctx, l, c)
	if err != nil {
		webFailed(klingSite, &a, c, err)
		return a
	}
	a.Source = firstNonEmpty(a.Source, s.source)
	b, err := webGet(ctx, l, s, http.MethodGet, l.url("kling", "https://klingai.com")+"/api/account/pointAndTicket?scope=PERSONAL",
		"klingai.com", "https://klingai.com/app/", nil, nil)
	if err != nil {
		webFailed(klingSite, &a, c, err)
		return a
	}
	if err := parseKlingPoints(b, &a); err != nil {
		webFailed(klingSite, &a, c, err)
	}
	return a
}

// parseKlingPoints reads /api/account/pointAndTicket: points are hundredths
// of a credit.
//
//	{"status":200,"result":1,"data":{"total":16600,"points":[{"balance":6600,"endTime":1791504000000},…],"tickets":…}}
func parseKlingPoints(b []byte, a *Account) error {
	var r struct {
		Status int `json:"status"`
		Result int `json:"result"`
		Data   *struct {
			Total  flexNumber `json:"total"`
			Points []struct {
				Balance flexNumber `json:"balance"`
				EndTime flexNumber `json:"endTime"`
				Name    string     `json:"name"`
				Type    string     `json:"type"`
			} `json:"points"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return errors.New("Kling answered something Ant doesn't understand")
	}
	if r.Data == nil || r.Result != 1 {
		return errWebSignedOut
	}
	lots := r.Data.Points
	sort.SliceStable(lots, func(i, j int) bool { return lots[i].EndTime < lots[j].EndTime })
	for _, lot := range lots {
		if lot.Balance <= 0 {
			continue
		}
		name := firstNonEmpty(lot.Name, "Credits")
		w := Window{Name: name, Left: ptr(float64(lot.Balance) / 100), Unit: "credits", Expires: true}
		if lot.EndTime > 0 {
			w.ResetsAt = ptr(time.UnixMilli(int64(lot.EndTime)).UTC())
		}
		a.Windows = append(a.Windows, w)
	}
	a.Balances = []Balance{{Label: "Credits", Currency: "credits", Amount: trimNumber(float64(r.Data.Total) / 100)}}
	if len(a.Windows) == 0 && r.Data.Total <= 0 {
		a.Message = "No credits left. Signing in on Kling each day adds the free daily credits."
	}
	a.Status = StatusOK
	return nil
}

// --- Leonardo.Ai: tokens, renewing daily on Free and monthly on paid plans ---

var leonardoSite = newWebSite("leonardo", "Leonardo.Ai", "app.leonardo.ai", "app.leonardo.ai", "", "https://app.leonardo.ai/",
	"in a signed-in app.leonardo.ai tab, open the developer tools, and copy the Cookie header of a request to app.leonardo.ai.")

func leonardoProvider() Provider {
	ways := leonardoSite.ways("tokens", readLeonardoWeb)
	key := Way{ID: "api-key", Name: "API key", Key: "apiKey", Read: readLeonardoAPI,
		Hint:   "Leonardo's official API (GET /me). API keys come with a Leonardo API plan, from app.leonardo.ai › API Access.",
		Fields: keyField("", "From app.leonardo.ai › API Access."),
		Discover: func(l Local) []Connection {
			return keyConnections(l, "leonardo", "tokens", keyHint{Env: []string{"LEONARDO_API_KEY"}}, nil)
		}}
	return Provider{ID: "leonardo", Name: "Leonardo.Ai", Link: "https://app.leonardo.ai/buy", Media: []string{"image", "video"}, Modes: []Mode{{
		ID: "tokens", Name: "Tokens", Kind: KindFree, Live: true,
		Rule: "Free: 150 fast tokens a day, renewing daily. Paid plans get monthly tokens (with rollover on some), plus any bought tokens, which don't expire.",
		Ways: []Way{ways[0], key, ways[1], ways[2]},
	}}}
}

type leonardoDetails struct {
	Plan                    string     `json:"plan"`
	SubscriptionTokens      flexNumber `json:"subscriptionTokens"`
	PaidTokens              flexNumber `json:"paidTokens"`
	RolloverTokens          flexNumber `json:"rolloverTokens"`
	TokenRenewalDate        string     `json:"tokenRenewalDate"`
	APIPaidTokens           flexNumber `json:"apiPaidTokens"`
	APISubscriptionTokens   flexNumber `json:"apiSubscriptionTokens"`
	APIPlanTokenRenewalDate string     `json:"apiPlanTokenRenewalDate"`
	User                    *struct {
		Username string `json:"username"`
	} `json:"user"`
}

func leonardoTime(s string) *time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return ptr(t.UTC())
		}
	}
	return nil
}

// fillLeonardo shows a user_details row: the plan's tokens first.
func fillLeonardo(d leonardoDetails, a *Account, api bool) {
	a.Plan = catalog.PlanName(strings.ToLower(d.Plan))
	if d.Plan != "" && !strings.EqualFold(d.Plan, "FREE") {
		a.Kind = KindSubscription
	}
	if d.User != nil {
		a.User = d.User.Username
	}
	plan := Window{Name: "Plan tokens", Left: ptr(float64(d.SubscriptionTokens)), Unit: "tokens", ResetsAt: leonardoTime(d.TokenRenewalDate)}
	if strings.EqualFold(d.Plan, "FREE") {
		plan.Name, plan.Limit = "Today", ptr(150.0)
	}
	a.Windows = []Window{plan}
	var extra []Window
	if d.RolloverTokens > 0 {
		extra = append(extra, Window{Name: "Rollover", Left: ptr(float64(d.RolloverTokens)), Unit: "tokens"})
	}
	if d.PaidTokens > 0 {
		extra = append(extra, Window{Name: "Bought", Left: ptr(float64(d.PaidTokens)), Unit: "tokens"})
	}
	if len(extra) > 0 {
		a.Groups = append(a.Groups, Group{Title: "Other tokens", Note: "Spent after the plan's tokens; they don't renew.", Windows: extra})
	}
	if api || d.APISubscriptionTokens > 0 || d.APIPaidTokens > 0 {
		a.Groups = append(a.Groups, Group{Title: "API", Windows: []Window{
			{Name: "API plan tokens", Left: ptr(float64(d.APISubscriptionTokens)), Unit: "tokens", ResetsAt: leonardoTime(d.APIPlanTokenRenewalDate)},
			{Name: "API bought tokens", Left: ptr(float64(d.APIPaidTokens)), Unit: "tokens"},
		}})
	}
	a.Status = StatusOK
}

func readLeonardoWeb(ctx context.Context, l Local, c Connection) Account {
	p := leonardoProvider()
	a := base(p, p.Modes[0], c, l.Now())
	s, err := leonardoSite.session(ctx, l, c)
	if err != nil {
		webFailed(leonardoSite, &a, c, err)
		return a
	}
	a.Source = firstNonEmpty(a.Source, s.source)
	app := l.url("leonardo.app", "https://app.leonardo.ai")
	b, err := webGet(ctx, l, s, http.MethodGet, app+"/api/auth/get-session", "app.leonardo.ai", "https://app.leonardo.ai/", nil, nil)
	if err != nil {
		webFailed(leonardoSite, &a, c, err)
		return a
	}
	var sess struct {
		Session *struct {
			AccessToken string `json:"accessToken"`
		} `json:"session"`
		User *struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	if json.Unmarshal(b, &sess) != nil || sess.Session == nil || sess.Session.AccessToken == "" {
		leonardoSite.signedOut(&a, c)
		return a
	}
	if sess.User != nil {
		a.User = sess.User.Email
	}
	q, _ := json.Marshal(map[string]string{"operationName": "AntTokens",
		"query": "query AntTokens { users { username user_details { plan subscriptionTokens paidTokens rolloverTokens tokenRenewalDate } } }"})
	b, err = l.fetch(ctx, http.MethodPost, l.url("leonardo.graphql", "https://api.leonardo.ai/v1/graphql"), map[string]string{
		"Authorization": "Bearer " + sess.Session.AccessToken, "Content-Type": "application/json", "Origin": "https://app.leonardo.ai",
	}, bytes.NewReader(q))
	if err != nil {
		webFailed(leonardoSite, &a, c, err)
		return a
	}
	var r struct {
		Data struct {
			Users []struct {
				Username string            `json:"username"`
				Details  []leonardoDetails `json:"user_details"`
			} `json:"users"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if json.Unmarshal(b, &r) != nil || len(r.Errors) > 0 || len(r.Data.Users) == 0 || len(r.Data.Users[0].Details) == 0 {
		a.Status, a.Message = StatusError, "Leonardo's app changed; Ant needs an update to read it."
		return a
	}
	fillLeonardo(r.Data.Users[0].Details[0], &a, false)
	if a.User == "" {
		a.User = r.Data.Users[0].Username
	}
	return a
}

func readLeonardoAPI(ctx context.Context, l Local, c Connection) Account {
	p := leonardoProvider()
	a := base(p, p.Modes[0], c, l.Now())
	var r struct {
		Details []leonardoDetails `json:"user_details"`
	}
	if !getJSON(ctx, l, l.url("leonardo.api", "https://cloud.leonardo.ai/api/rest/v1")+"/me", c.Values["apiKey"], "Leonardo", &a, &r) {
		return a
	}
	if len(r.Details) == 0 {
		a.Status, a.Message = StatusError, "Leonardo answered without the account's details."
		return a
	}
	fillLeonardo(r.Details[0], &a, true)
	return a
}

// --- SeaArt: daily stamina, renewing at 00:00 UTC, and credits ---

var seaArtSite = newWebSite("seaart", "SeaArt", "seaart.ai", "www.seaart.ai", "", "https://www.seaart.ai/",
	"in a signed-in seaart.ai tab, open the developer tools, and copy the Cookie header of a request to www.seaart.ai/api/.")

func seaArtProvider() Provider {
	return Provider{ID: "seaart", Name: "SeaArt", Link: "https://www.seaart.ai/subscribe", Media: []string{"image", "video"}, Modes: []Mode{{
		ID: "stamina", Name: "Stamina and credits", Kind: KindFree, Live: true,
		Rule: "Stamina is a daily allowance that ends at 00:00 UTC and doesn't roll over; it is spent first. Credits (free or bought) are kept, and spent after it.",
		Ways: seaArtSite.ways("stamina", readSeaArt),
	}}}
}

func readSeaArt(ctx context.Context, l Local, c Connection) Account {
	p := seaArtProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	s, err := seaArtSite.session(ctx, l, c)
	if err != nil {
		webFailed(seaArtSite, &a, c, err)
		return a
	}
	a.Source = firstNonEmpty(a.Source, s.source)
	b, err := webGet(ctx, l, s, http.MethodPost, l.url("seaart", "https://www.seaart.ai")+"/api/v1/payment/assets/get",
		"www.seaart.ai", "https://www.seaart.ai/", map[string]string{"X-Platform": "web", "X-Project-Id": "seaart"}, []byte("{}"))
	if err != nil {
		webFailed(seaArtSite, &a, c, err)
		return a
	}
	if err := parseSeaArtAssets(b, now, &a); err != nil {
		webFailed(seaArtSite, &a, c, err)
	}
	return a
}

// parseSeaArtAssets reads /api/v1/payment/assets/get:
//
//	{"status":{"code":10000},"data":{"account_id":"…","temp_coins":150,"free_coins":20,"paid_coins":0,
//	 "is_active":false,"expiry_time":0,"product_level":0}}
//
// code 20000 is "account not logged in".
func parseSeaArtAssets(b []byte, now time.Time, a *Account) error {
	var r struct {
		Status struct {
			Code flexNumber `json:"code"`
			Msg  string     `json:"msg"`
		} `json:"status"`
		Data *struct {
			AccountID  string     `json:"account_id"`
			TempCoins  flexNumber `json:"temp_coins"`
			FreeCoins  flexNumber `json:"free_coins"`
			PaidCoins  flexNumber `json:"paid_coins"`
			IsActive   bool       `json:"is_active"`
			ExpiryTime flexNumber `json:"expiry_time"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return errors.New("SeaArt answered something Ant doesn't understand")
	}
	if r.Status.Code == 20000 || r.Data == nil || r.Data.AccountID == "" {
		return errWebSignedOut
	}
	if r.Status.Code != 10000 {
		return errors.New("SeaArt: " + r.Status.Msg)
	}
	d := r.Data
	a.Windows = []Window{{Name: "Stamina · today", Left: ptr(float64(d.TempCoins)), Unit: "stamina", ResetsAt: ptr(utcMidnight(now)), Expires: false}}
	a.Balances = []Balance{{Label: "Credits", Currency: "credits", Amount: trimNumber(float64(d.FreeCoins + d.PaidCoins))}}
	if d.IsActive {
		a.Kind, a.Plan = KindSubscription, "Subscribed"
		if d.ExpiryTime > 0 {
			t := time.Unix(int64(d.ExpiryTime), 0)
			if d.ExpiryTime > 1e12 {
				t = time.UnixMilli(int64(d.ExpiryTime))
			}
			a.EndsAt = ptr(t.UTC())
		}
	}
	a.Status = StatusOK
	return nil
}

// --- Stable Diffusion Online (stablediffusionweb.com): credits ---

var sdWebSite = newWebSite("sdweb", "Stable Diffusion Online", "stablediffusionweb.com", "stablediffusionweb.com", "", "https://stablediffusionweb.com/app/image-generator",
	"in a signed-in stablediffusionweb.com tab, open the developer tools, and copy the Cookie header of a request to stablediffusionweb.com/api/.")

func sdWebProvider() Provider {
	return Provider{ID: "sdweb", Name: "Stable Diffusion Online", Link: "https://stablediffusionweb.com/pricing", Media: []string{"image", "video"}, Modes: []Mode{{
		ID: "credits", Name: "Credits", Kind: KindFree, Live: true,
		Rule: "Credits spent per image or video by model. Free accounts get a small grant; Pro and Max renew monthly.",
		Ways: sdWebSite.ways("credits", readSDWeb),
	}}}
}

func readSDWeb(ctx context.Context, l Local, c Connection) Account {
	p := sdWebProvider()
	a := base(p, p.Modes[0], c, l.Now())
	s, err := sdWebSite.session(ctx, l, c)
	if err != nil {
		webFailed(sdWebSite, &a, c, err)
		return a
	}
	a.Source = firstNonEmpty(a.Source, s.source)
	input := url.QueryEscape(`{"0":{"json":null,"meta":{"values":["undefined"]}}}`)
	b, err := webGet(ctx, l, s, http.MethodGet, l.url("sdweb", "https://stablediffusionweb.com")+"/api/auth.user?batch=1&input="+input,
		"stablediffusionweb.com", "https://stablediffusionweb.com/app/image-generator", nil, nil)
	if err != nil {
		webFailed(sdWebSite, &a, c, err)
		return a
	}
	if err := parseSDWebUser(b, &a); err != nil {
		webFailed(sdWebSite, &a, c, err)
	}
	return a
}

// parseSDWebUser reads the tRPC batch answer of auth.user:
// [{"result":{"data":{"json":{"email":"…","plan":"FREE","credits":40}}}}];
// json is null when signed out.
func parseSDWebUser(b []byte, a *Account) error {
	var r []struct {
		Result struct {
			Data struct {
				JSON *struct {
					Email   string     `json:"email"`
					Plan    string     `json:"plan"`
					Credits flexNumber `json:"credits"`
				} `json:"json"`
			} `json:"data"`
		} `json:"result"`
	}
	if json.Unmarshal(b, &r) != nil || len(r) == 0 {
		return errors.New("Stable Diffusion Online answered something Ant doesn't understand")
	}
	u := r[0].Result.Data.JSON
	if u == nil {
		return errWebSignedOut
	}
	a.User, a.Plan = u.Email, catalog.PlanName(strings.ToLower(u.Plan))
	if u.Plan != "" && !strings.EqualFold(u.Plan, "FREE") {
		a.Kind = KindSubscription
	}
	a.Windows = []Window{{Name: "Credits", Left: ptr(float64(u.Credits)), Unit: "credits"}}
	a.Status = StatusOK
	return nil
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// trimNumber writes a count without needless decimals.
func trimNumber(f float64) string {
	s := money2(f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
