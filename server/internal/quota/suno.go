package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Suno has no public API; Ant makes the read Suno's own web app makes
// (shape as used by gcui-art/suno-api, March 2026): the Clerk __client
// cookie → the active session → a short-lived token → /api/billing/info/.
// The cookie comes from the browser's cookie store, or is pasted on the
// page. A private API can change; when it does, the card says so.

const (
	sunoClerkQuery = "__clerk_api_version=2025-11-10&_clerk_js_version=5.117.0"
	browserAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
)

var errSunoChanged = errors.New("Suno's billing reply changed; Ant needs an update to read it")

var sunoSignIn = &SignIn{URL: "https://suno.com/sign-in", Domain: "suno.com", Done: func(cs []siteCookie) bool {
	// Clerk: __client is the session, __client_uat is "0" until signed in
	uat := cookieValue(cs, "__client_uat")
	return cookieValue(cs, "__client") != "" && uat != "" && uat != "0"
}}

func sunoProvider() Provider {
	return Provider{ID: "suno", Name: "Suno", Link: "https://suno.com/create", Media: []string{"music"}, Modes: []Mode{{
		ID: "account", Name: "Credits", Kind: KindFree, Live: true,
		Rule: "Free: 50 credits a day (a song costs 10), reset at UTC midnight and lost if unused. Paid plans get monthly credits instead.",
		Ways: []Way{
			{ID: "browser", Name: "Your browser", Hint: "Ant reads the Suno sign-in of Chrome, Edge, Brave, Arc or Firefox on this Mac. Sign in to suno.com there.",
				Fields: []Field{}, Discover: discoverSuno, Read: readSuno, BrowserDomain: "suno.com"},
			{ID: "sign-in", Name: "Sign in to Suno", Hint: "Sign in to Suno in the window Ant opens. Ant keeps the session; it never sees your password.",
				Fields: []Field{}, SignIn: sunoSignIn, Read: readSuno},
			{ID: "cookie", Name: "Paste the cookie", Key: "cookie", Read: readSuno,
				Hint:   "Only if signing in doesn't work: in a signed-in suno.com tab, open the developer tools, copy the request's Cookie header (or the __client cookie) and paste it.",
				Fields: []Field{{ID: "cookie", Label: "Cookie", Type: "secret", Placeholder: "__client=…", Help: "The whole Cookie header or just the __client value."}}},
		},
	}}}
}

// discoverSuno looks for the browser's sign-in. It does no Keychain or
// network work beyond reading the cookie stores.
func discoverSuno(l Local) []Connection {
	c, err := l.findBrowserCookie(context.Background(), "suno.com", "__client")
	if err != nil {
		if errors.Is(err, errNoCookie) {
			return nil
		}
		return []Connection{{ID: "suno-browser", Provider: "suno", Mode: "account", Origin: OriginFound,
			Source: "browser", Values: map[string]string{"error": short(err.Error())}}}
	}
	return []Connection{{ID: "suno-browser", Provider: "suno", Mode: "account", Origin: OriginFound,
		Source: c.source, Values: map[string]string{"cookie": c.value}}}
}

// clientCookie takes either the bare __client value or a whole Cookie header.
func clientCookie(s string) string {
	if !strings.Contains(s, "=") {
		return strings.TrimSpace(s)
	}
	for _, part := range strings.Split(s, ";") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok && k == "__client" {
			return v
		}
	}
	return strings.TrimSpace(s)
}

func readSuno(ctx context.Context, l Local, c Connection) Account {
	p := sunoProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	if e := c.Values["error"]; e != "" {
		a.Status, a.Message = StatusError, e
		return a
	}
	cookie := clientCookie(c.Values["cookie"])
	if s, ok := signedInSession(ctx, l, *sunoSignIn, c.Values["session"], c.Values["profile"]); ok {
		cookie = cookieValue(s.cookies, "__client")
	}
	authBase := l.url("suno.auth", "https://auth.suno.com")
	studioBase := l.url("suno.studio", "https://studio-api.prod.suno.com")
	clerk := map[string]string{"Authorization": cookie, "Cookie": "__client=" + cookie, "User-Agent": browserAgent, "Origin": "https://suno.com"}

	b, err := l.fetch(ctx, http.MethodGet, authBase+"/v1/client?"+sunoClerkQuery, clerk, nil)
	if err != nil {
		return sunoFailed(a, err)
	}
	sid, user := parseClerkClient(b)
	if sid == "" {
		a.Status, a.Message = StatusSignedOut, "Suno's sign-in has ended. Sign in again."
		return a
	}
	a.User = user
	b, err = l.fetch(ctx, http.MethodPost, authBase+"/v1/client/sessions/"+url.PathEscape(sid)+"/tokens?"+sunoClerkQuery, clerk, nil)
	if err != nil {
		return sunoFailed(a, err)
	}
	var tok struct {
		JWT string `json:"jwt"`
	}
	if json.Unmarshal(b, &tok) != nil || tok.JWT == "" {
		a.Status, a.Message = StatusError, "Suno's sign-in changed; Ant needs an update to read it."
		return a
	}
	b, err = l.fetch(ctx, http.MethodGet, studioBase+"/api/billing/info/", map[string]string{
		"Authorization": "Bearer " + tok.JWT, "User-Agent": browserAgent, "Origin": "https://suno.com",
	}, nil)
	if err != nil {
		return sunoFailed(a, err)
	}
	if err := parseSunoBilling(b, now, &a); err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status = StatusOK
	return a
}

func sunoFailed(a Account, err error) Account {
	a.Status, a.Message = StatusError, "Couldn't read Suno: "+short(err.Error())
	if isAuthError(err) {
		a.Status, a.Message = StatusSignedOut, "Suno refused the sign-in. Sign in again."
	}
	return a
}

// parseClerkClient reads the active session id and the user's email from
// Clerk's GET /v1/client: {"response":{"last_active_session_id":"sess_…",
// "sessions":[{"id":"sess_…","user":{"email_addresses":[{"email_address":"…"}]}}]}}.
func parseClerkClient(b []byte) (sid, email string) {
	var c struct {
		Response *struct {
			LastActive string `json:"last_active_session_id"`
			Sessions   []struct {
				ID   string `json:"id"`
				User struct {
					Emails []struct {
						Address string `json:"email_address"`
					} `json:"email_addresses"`
				} `json:"user"`
			} `json:"sessions"`
		} `json:"response"`
	}
	if json.Unmarshal(b, &c) != nil || c.Response == nil {
		return "", ""
	}
	sid = c.Response.LastActive
	for _, s := range c.Response.Sessions {
		if s.ID == sid && len(s.User.Emails) > 0 {
			email = s.User.Emails[0].Address
		}
	}
	return sid, email
}

// parseSunoBilling reads /api/billing/info/. Known fields:
// total_credits_left, period ("day" on the free plan, whose credits reset
// at UTC midnight and don't carry over), monthly_limit, monthly_usage;
// is_active and a plan name when subscribed. Anything else is ignored.
func parseSunoBilling(b []byte, now time.Time, a *Account) error {
	var info struct {
		CreditsLeft  *float64 `json:"total_credits_left"`
		Period       *string  `json:"period"`
		MonthlyLimit *float64 `json:"monthly_limit"`
		MonthlyUsage *float64 `json:"monthly_usage"`
		IsActive     *bool    `json:"is_active"`
		Plan         *struct {
			Name string `json:"name"`
		} `json:"plan"`
		RenewsOn *string `json:"renews_on"`
	}
	if err := json.Unmarshal(b, &info); err != nil || info.CreditsLeft == nil {
		return errSunoChanged
	}
	period := ""
	if info.Period != nil {
		period = strings.ToLower(*info.Period)
	}
	w := Window{Left: info.CreditsLeft, Unit: "credits"}
	if info.MonthlyLimit != nil && *info.MonthlyLimit > 0 {
		w.Limit = info.MonthlyLimit
		if info.MonthlyUsage != nil {
			w.Used = info.MonthlyUsage
		}
	}
	paid := info.IsActive != nil && *info.IsActive && period != "day"
	if paid {
		a.Kind = KindSubscription
		if info.Plan != nil && info.Plan.Name != "" {
			a.Plan = info.Plan.Name
		}
		w.Name = "Month"
		if info.RenewsOn != nil {
			if t, err := time.Parse(time.RFC3339, *info.RenewsOn); err == nil {
				a.RenewsAt, w.ResetsAt = ptr(t.UTC()), ptr(t.UTC())
			} else if t, err := time.Parse("2006-01-02", *info.RenewsOn); err == nil {
				a.RenewsAt, w.ResetsAt = ptr(t.UTC()), ptr(t.UTC())
			}
		}
	} else {
		a.Kind, a.Plan, w.Name = KindFree, "Free", "Today"
		y, m, d := now.UTC().Date()
		w.ResetsAt, w.ResetEstimated = ptr(time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)), true
		// the "monthly" figures don't describe a daily allowance
		w.Used = nil
		if w.Limit != nil && *w.Limit < *w.Left {
			w.Limit = nil
		}
		if w.Limit == nil && *w.Left <= 50 {
			w.Limit = ptr(50.0) // Suno's published free allowance
		}
	}
	a.Windows = []Window{w}
	return nil
}
