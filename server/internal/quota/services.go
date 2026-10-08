package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Agents, search and voice services with free or plan allowances. Each
// reads its own official endpoint where there is one (Manus, Poe,
// ElevenLabs keys), else what its own pages read with the sign-in.

// --- Manus: total credits, a daily (or weekly) refresh, monthly plan credits ---

var manusSite = newWebSite("manus", "Manus", "manus.im", "manus.im", "session_id", "https://manus.im/login",
	"in a signed-in manus.im tab, open the developer tools, and copy the Cookie header (it holds session_id=…), or just the session_id value.")

func manusProvider() Provider {
	ways := manusSite.ways("credits", readManus)
	key := Way{ID: "api-key", Name: "API key", Key: "apiKey", Read: readManus,
		Hint:   "Manus's official API (GET /v2/usage.availableCredits). Create a key at manus.im › Settings › Integrations › API.",
		Fields: keyField("", "From manus.im › Settings › Integrations › API."),
		Discover: func(l Local) []Connection {
			return keyConnections(l, "manus", "credits", keyHint{Env: []string{"MANUS_API_KEY"}}, nil)
		}}
	return Provider{ID: "manus", Name: "Manus", Link: "https://manus.im/app?show_settings=usage", Media: []string{"agent"}, Modes: []Mode{{
		ID: "credits", Name: "Credits", Kind: KindFree, Live: true,
		Rule: "Refresh credits top up each day (or week) to their maximum and don't pile up; plan credits come monthly; bought add-on credits are kept.",
		Ways: []Way{key, ways[0], ways[1], ways[2]},
	}}}
}

// manusCredits is the AvailableCredits object, in the official API's
// snake_case or the web app's camelCase.
type manusCredits struct {
	Total, Free, Periodic, Addon, ProMonthly, Event, Refresh, MaxRefresh flexNumber
	NextRefresh, PeriodEnd                                               flexNumber
	Interval                                                             string
}

func (m *manusCredits) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	pick := func(dst *flexNumber, keys ...string) {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				_ = json.Unmarshal(v, dst)
				return
			}
		}
	}
	pick(&m.Total, "total_credits", "totalCredits")
	pick(&m.Free, "free_credits", "freeCredits")
	pick(&m.Periodic, "periodic_credits", "periodicCredits")
	pick(&m.Addon, "addon_credits", "addonCredits")
	pick(&m.ProMonthly, "pro_monthly_credits", "proMonthlyCredits")
	pick(&m.Event, "event_credits", "eventCredits")
	pick(&m.Refresh, "refresh_credits", "refreshCredits")
	pick(&m.MaxRefresh, "max_refresh_credits", "maxRefreshCredits")
	pick(&m.NextRefresh, "next_refresh_time", "nextRefreshTime")
	pick(&m.PeriodEnd, "current_period_end", "currentPeriodEnd")
	for _, k := range []string{"refresh_interval", "refreshInterval"} {
		if v, ok := raw[k]; ok {
			_ = json.Unmarshal(v, &m.Interval)
		}
	}
	return nil
}

func readManus(ctx context.Context, l Local, c Connection) Account {
	p := manusProvider()
	a := base(p, p.Modes[0], c, l.Now())
	var b []byte
	var err error
	if key := c.Values["apiKey"]; key != "" {
		b, err = l.fetch(ctx, http.MethodGet, l.url("manus.api", "https://api.manus.ai")+"/v2/usage.availableCredits",
			map[string]string{"x-manus-api-key": key}, nil)
		if err != nil {
			a.Status, a.Message = StatusError, "Couldn't read Manus: "+short(err.Error())
			if isAuthError(err) {
				a.Status, a.Message = StatusSignedOut, "Manus refused the API key."
			}
			return a
		}
	} else {
		s, serr := manusSite.session(ctx, l, c)
		token := cookieValue(s.cookies, "session_id")
		if serr != nil || token == "" {
			webFailed(manusSite, &a, c, errWebSignedOut)
			return a
		}
		a.Source = firstNonEmpty(a.Source, s.source)
		b, err = l.fetch(ctx, http.MethodPost, l.url("manus.web", "https://api.manus.im")+"/user.v1.UserService/GetAvailableCredits", map[string]string{
			"Authorization": "Bearer " + token, "Content-Type": "application/json", "Connect-Protocol-Version": "1",
			"Origin": "https://manus.im", "Referer": "https://manus.im/", "User-Agent": browserAgent,
		}, bytes.NewReader([]byte("{}")))
		if err != nil {
			webFailed(manusSite, &a, c, err)
			return a
		}
	}
	if err := parseManus(b, &a); err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
	}
	return a
}

// parseManus reads {"ok":true,"data":{…}} (official) or the web app's
// {"totalCredits":…} / {"data":{…}}.
func parseManus(b []byte, a *Account) error {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	raw := json.RawMessage(b)
	if json.Unmarshal(b, &env) == nil && len(env.Data) > 0 && string(env.Data) != "null" {
		raw = env.Data
	}
	var m manusCredits
	if json.Unmarshal(raw, &m) != nil {
		return errors.New("Manus answered something Ant doesn't understand")
	}
	a.Balances = []Balance{{Label: "Credits", Currency: "credits", Amount: trimNumber(float64(m.Total))}}
	if m.MaxRefresh > 0 {
		name := "Refresh · " + firstNonEmpty(map[string]string{"daily": "day", "weekly": "week"}[m.Interval], "period")
		w := Window{Name: name, Left: ptr(float64(m.Refresh)), Limit: ptr(float64(m.MaxRefresh)), Unit: "credits"}
		if m.NextRefresh > 0 {
			w.ResetsAt = ptr(time.Unix(int64(m.NextRefresh), 0).UTC())
		}
		a.Windows = append(a.Windows, w)
	}
	if m.ProMonthly > 0 {
		a.Kind = KindSubscription
		w := Window{Name: "Plan · month", Left: ptr(float64(m.Periodic)), Limit: ptr(float64(m.ProMonthly)), Unit: "credits"}
		if m.PeriodEnd > 0 {
			w.ResetsAt = ptr(time.Unix(int64(m.PeriodEnd), 0).UTC())
			a.RenewsAt = w.ResetsAt
		}
		a.Windows = append(a.Windows, w)
	}
	var kept []Balance
	for _, x := range []struct {
		label string
		n     flexNumber
	}{{"Free", m.Free}, {"Add-on", m.Addon}, {"Event", m.Event}} {
		if x.n > 0 {
			kept = append(kept, Balance{Label: x.label, Currency: "credits", Amount: trimNumber(float64(x.n))})
		}
	}
	if len(kept) > 0 {
		a.Groups = []Group{{Title: "Kept credits", Note: "They don't renew; spent after the refresh and plan credits.", Balances: kept}}
	}
	a.Status = StatusOK
	return nil
}

// --- Perplexity: recurring, bonus and bought credits ---

var perplexitySite = newWebSite("perplexity", "Perplexity", "perplexity.ai", "www.perplexity.ai", "__Secure-next-auth.session-token", "https://www.perplexity.ai/",
	"in a signed-in perplexity.ai tab, open the developer tools, and copy the Cookie header (it holds __Secure-next-auth.session-token=…).")

func perplexityProvider() Provider {
	return Provider{ID: "perplexity", Name: "Perplexity", Link: "https://www.perplexity.ai/account/usage", Media: []string{"search", "text"}, Modes: []Mode{{
		ID: "credits", Name: "Credits", Kind: KindSubscription, Live: true,
		Rule: "Recurring credits renew with the plan; bonus credits end on their own dates; bought credits are kept. Recurring are spent first, then bought, then bonus.",
		Ways: perplexitySite.ways("credits", readPerplexity),
	}}}
}

func readPerplexity(ctx context.Context, l Local, c Connection) Account {
	p := perplexityProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	s, err := perplexitySite.session(ctx, l, c)
	if err != nil {
		webFailed(perplexitySite, &a, c, err)
		return a
	}
	a.Source = firstNonEmpty(a.Source, s.source)
	b, err := webGet(ctx, l, s, http.MethodGet, l.url("perplexity", "https://www.perplexity.ai")+"/rest/billing/credits?version=2.18&source=default",
		"www.perplexity.ai", "https://www.perplexity.ai/account/usage", nil, nil)
	if err != nil {
		webFailed(perplexitySite, &a, c, err)
		return a
	}
	if err := parsePerplexityCredits(b, now, &a); err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
	}
	return a
}

// parsePerplexityCredits reads /rest/billing/credits (cents of a credit):
// grants by type, total used this period, and the renewal date. Usage is
// taken from recurring first, then bought, then bonus — Perplexity's order.
func parsePerplexityCredits(b []byte, now time.Time, a *Account) error {
	var r struct {
		Renewal flexNumber `json:"renewal_date_ts"`
		Used    flexNumber `json:"total_usage_cents"`
		Bought  flexNumber `json:"current_period_purchased_cents"`
		Grants  []struct {
			Type    string      `json:"type"`
			Amount  flexNumber  `json:"amount_cents"`
			Expires *flexNumber `json:"expires_at_ts"`
		} `json:"credit_grants"`
	}
	if json.Unmarshal(b, &r) != nil || r.Grants == nil {
		return errors.New("Perplexity answered something Ant doesn't understand")
	}
	var recurring, bought, bonus float64
	var bonusEnds *time.Time
	for _, g := range r.Grants {
		switch g.Type {
		case "recurring":
			recurring += float64(g.Amount)
		case "purchased":
			bought += float64(g.Amount)
		case "promotional":
			if g.Expires != nil && time.Unix(int64(*g.Expires), 0).Before(now) {
				continue
			}
			bonus += float64(g.Amount)
			if g.Expires != nil {
				t := time.Unix(int64(*g.Expires), 0).UTC()
				if bonusEnds == nil || t.Before(*bonusEnds) {
					bonusEnds = &t
				}
			}
		}
	}
	bought = max(bought, float64(r.Bought))
	used := float64(r.Used)
	take := func(pool float64) float64 { u := min(used, pool); used -= u; return pool - u }
	renews := ptr(time.Unix(int64(r.Renewal), 0).UTC())
	if recurring > 0 {
		a.Windows = append(a.Windows, Window{Name: "Credits · plan", Left: ptr(take(recurring) / 100), Limit: ptr(recurring / 100), Unit: "credits", ResetsAt: renews})
		a.RenewsAt = renews
		a.Plan = "Pro"
		if recurring >= 5000 {
			a.Plan = "Max"
		}
	} else {
		a.Kind = KindFree
	}
	if bought > 0 {
		a.Windows = append(a.Windows, Window{Name: "Bought", Left: ptr(take(bought) / 100), Limit: ptr(bought / 100), Unit: "credits"})
	}
	if bonus > 0 {
		a.Windows = append(a.Windows, Window{Name: "Bonus", Left: ptr(take(bonus) / 100), Limit: ptr(bonus / 100), Unit: "credits", ResetsAt: bonusEnds, Expires: bonusEnds != nil})
	}
	if len(a.Windows) == 0 {
		a.Message = "No credits on this account."
	}
	a.Status = StatusOK
	return nil
}

// --- Poe: compute points, monthly on subscriptions, daily on Free ---

func poeProvider() Provider {
	return Provider{ID: "poe", Name: "Poe", Link: "https://poe.com/settings", Media: []string{"text", "image", "video"}, Modes: []Mode{{
		ID: "points", Name: "Compute points", Kind: KindFree, Live: true, Key: "apiKey",
		Rule:   "Free: a daily allowance of points. Subscriptions get monthly points; bots spend them at their own rates.",
		Fields: keyField("", "From poe.com/api/keys."),
		Discover: func(l Local) []Connection {
			return keyConnections(l, "poe", "points", keyHint{Hosts: []string{"api.poe.com"}, OpenCode: []string{"poe"}, Env: []string{"POE_API_KEY"}}, nil)
		},
		Read: readPoe,
	}}}
}

func readPoe(ctx context.Context, l Local, c Connection) Account {
	p := poeProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	poe := l.url("poe", "https://api.poe.com")
	var bal struct {
		Points *flexNumber `json:"current_point_balance"`
	}
	if !getJSON(ctx, l, poe+"/usage/current_balance", c.Values["apiKey"], "Poe", &a, &bal) {
		return a
	}
	if bal.Points == nil {
		a.Status, a.Message = StatusError, "Poe answered without a point balance."
		return a
	}
	a.Balances = []Balance{{Label: "Points", Currency: "credits", Amount: trimNumber(float64(*bal.Points))}}
	a.Status = StatusOK
	// what each bot spent in the last 7 days, best effort
	var hist struct {
		Data []struct {
			Bot     string     `json:"bot_name"`
			Points  flexNumber `json:"cost_points"`
			Created flexNumber `json:"creation_time"`
		} `json:"data"`
	}
	var dummy Account
	if getJSON(ctx, l, poe+"/usage/points_history?limit=100", c.Values["apiKey"], "Poe", &dummy, &hist) {
		spent := map[string]float64{}
		for _, h := range hist.Data {
			t := int64(h.Created)
			for t > 1e11 { // µs or ms to s
				t /= 1000
			}
			if h.Bot != "" && time.Unix(t, 0).After(now.AddDate(0, 0, -7)) {
				spent[h.Bot] += float64(h.Points)
			}
		}
		var bots []string
		for k := range spent {
			bots = append(bots, k)
		}
		sort.Slice(bots, func(i, j int) bool { return spent[bots[i]] > spent[bots[j]] })
		var bs []Balance
		for _, k := range bots {
			bs = append(bs, Balance{Label: k, Currency: "credits", Amount: trimNumber(spent[k])})
		}
		if len(bs) > 0 {
			a.Groups = []Group{{Title: "Spent in the last 7 days", Balances: bs}}
		}
	}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("poe", "https://api.poe.com")+"/v1/models")
	return a
}

// --- ElevenLabs: characters per billing period ---

func elevenLabsProvider() Provider {
	return Provider{ID: "elevenlabs", Name: "ElevenLabs", Link: "https://elevenlabs.io/app/subscription", Media: []string{"speech", "music"}, Modes: []Mode{{
		ID: "characters", Name: "Credits", Kind: KindFree, Live: true, Key: "apiKey",
		Rule:   "Credits (characters) per month, renewing on the plan's date: 10,000 on Free. Voice slots are a separate limit.",
		Fields: keyField("sk_…", "From elevenlabs.io › Developers › API keys; it needs the user_read permission."),
		Discover: func(l Local) []Connection {
			return keyConnections(l, "elevenlabs", "characters", keyHint{Env: []string{"ELEVENLABS_API_KEY", "XI_API_KEY"}}, nil)
		},
		Read: readElevenLabs,
	}}}
}

func readElevenLabs(ctx context.Context, l Local, c Connection) Account {
	p := elevenLabsProvider()
	a := base(p, p.Modes[0], c, l.Now())
	b, err := l.fetch(ctx, http.MethodGet, l.url("elevenlabs", "https://api.elevenlabs.io")+"/v1/user/subscription",
		map[string]string{"xi-api-key": c.Values["apiKey"]}, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read ElevenLabs: "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "ElevenLabs refused the key, or it lacks the user_read permission."
		}
		return a
	}
	var r struct {
		Tier       string     `json:"tier"`
		Status     string     `json:"status"`
		Used       flexNumber `json:"character_count"`
		Limit      flexNumber `json:"character_limit"`
		Reset      flexNumber `json:"next_character_count_reset_unix"`
		Voices     flexNumber `json:"voice_slots_used"`
		VoiceLimit flexNumber `json:"voice_limit"`
	}
	if json.Unmarshal(b, &r) != nil {
		a.Status, a.Message = StatusError, "ElevenLabs answered something Ant doesn't understand."
		return a
	}
	a.Plan = planName(strings.ReplaceAll(r.Tier, "_", " "))
	if r.Tier != "" && r.Tier != "free" {
		a.Kind = KindSubscription
	}
	w := Window{Name: "Credits · month", Used: ptr(float64(r.Used)), Limit: ptr(float64(r.Limit)), Left: ptr(max(0, float64(r.Limit-r.Used))), Unit: "credits"}
	if r.Reset > 0 {
		w.ResetsAt = ptr(time.Unix(int64(r.Reset), 0).UTC())
		a.RenewsAt = w.ResetsAt
	}
	a.Windows = []Window{w}
	if r.VoiceLimit > 0 {
		a.Windows = append(a.Windows, Window{Name: "Voice slots", Used: ptr(float64(r.Voices)), Limit: ptr(float64(r.VoiceLimit)),
			Left: ptr(max(0, float64(r.VoiceLimit-r.Voices))), Unit: "voices", Aside: true})
	}
	a.Status = StatusOK
	withModels(&a, l, fingerprint("elevenlabs", "models", c.Values["apiKey"]), func() ([]Model, error) {
		b, err := l.fetch(ctx, http.MethodGet, l.url("elevenlabs", "https://api.elevenlabs.io")+"/v1/models",
			map[string]string{"xi-api-key": c.Values["apiKey"]}, nil)
		if err != nil {
			return nil, err
		}
		var ms []struct {
			ID   string `json:"model_id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &ms) != nil {
			return nil, errors.New("not a model list")
		}
		var out []Model
		for _, m := range ms {
			out = append(out, Model{ID: m.ID, Name: m.Name, Media: "speech"})
		}
		return out, nil
	})
	return a
}
