package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// ChatGPT's plan is read through the sign-in Codex keeps in
// $CODEX_HOME/auth.json (default ~/.codex), the same /wham/usage read
// Codex's own /status makes. There is no key to paste: the sign-in is an
// OAuth session only Codex can start.
//
// Ant never refreshes the token itself: that would rotate Codex's refresh
// token under it. An expired token is reported and Codex renews it on its
// next run.

func chatGPTProvider() Provider {
	return Provider{ID: "chatgpt", Name: "ChatGPT", Link: "https://chatgpt.com/codex/settings/usage", Modes: []Mode{{
		ID: "plan", Name: "ChatGPT plan", Kind: KindSubscription, Live: true,
		Rule:     "Codex use is limited by a 5-hour window and a weekly window. Extra credits are spent once a window runs out.",
		Fields:   []Field{},
		Connect:  "Sign in to Codex with your ChatGPT account: run codex login in Terminal. Ant reads that sign-in.",
		Discover: discoverChatGPT,
		Read:     readChatGPT,
	}}}
}

func codexAuthPath(l Local) string {
	if v, _ := l.env("CODEX_HOME"); v != "" {
		return strings.TrimRight(v, "/") + "/auth.json"
	}
	return l.path(".codex", "auth.json")
}

type codexAuth struct {
	Tokens struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func discoverChatGPT(l Local) []Connection {
	p := codexAuthPath(l)
	var auth codexAuth
	if !readJSON(p, &auth) || auth.Tokens.AccessToken == "" {
		return nil
	}
	id := jwtClaims(auth.Tokens.IDToken)
	account := auth.Tokens.AccountID
	if account == "" {
		account = claimString(id, "https://api.openai.com/auth", "chatgpt_account_id")
	}
	// identified by the ChatGPT account, which outlives token renewals
	return []Connection{{ID: fingerprint("chatgpt", "plan", account+claimString(id, "email")), Provider: "chatgpt", Mode: "plan",
		Origin: OriginFound, Source: l.display(p), Values: map[string]string{"authPath": p}}}
}

func readChatGPT(ctx context.Context, l Local, c Connection) Account {
	p := chatGPTProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	var auth codexAuth
	if !readJSON(c.Values["authPath"], &auth) || auth.Tokens.AccessToken == "" {
		a.Status, a.Message = StatusSignedOut, "Codex is signed out. Run codex login again."
		return a
	}
	id := jwtClaims(auth.Tokens.IDToken)
	a.User = claimString(id, "email")
	a.Plan = catalog.PlanName(claimString(id, "https://api.openai.com/auth", "chatgpt_plan_type"))
	if t, err := time.Parse(time.RFC3339, claimString(id, "https://api.openai.com/auth", "chatgpt_subscription_active_until")); err == nil && t.After(now) {
		a.RenewsAt = ptr(t.UTC())
	}
	accountID := auth.Tokens.AccountID
	if accountID == "" {
		accountID = claimString(id, "https://api.openai.com/auth", "chatgpt_account_id")
	}
	if exp, ok := jwtClaims(auth.Tokens.AccessToken)["exp"].(float64); ok && time.Unix(int64(exp), 0).Before(now) {
		a.Status, a.Message = StatusSignedOut, "Codex's sign-in has expired. Open Codex once and it renews it."
		return a
	}
	b, err := l.fetch(ctx, http.MethodGet, l.url("chatgpt.usage", "https://chatgpt.com/backend-api/wham/usage"), map[string]string{
		"Authorization":      "Bearer " + auth.Tokens.AccessToken,
		"chatgpt-account-id": accountID,
	}, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read usage: "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "ChatGPT refused Codex's sign-in. Run codex login again."
		}
		return a
	}
	plan, windows, balances, err := parseChatGPTUsage(b, now)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	if plan != "" {
		a.Plan = plan
	}
	if strings.EqualFold(a.Plan, "free") {
		a.Kind = KindFree
	}
	a.Status, a.Windows, a.Balances = StatusOK, windows, balances
	withModels(&a, l, "chatgpt:"+accountID, func() ([]Model, error) {
		return codexModels(ctx, l, auth.Tokens.AccessToken, accountID)
	})
	return a
}

// codexModels is what Codex can use with this ChatGPT sign-in: the list
// Codex itself reads (codex-rs models endpoint, GET {base}/models?client_version=),
// models shown in its picker.
func codexModels(ctx context.Context, l Local, token, accountID string) ([]Model, error) {
	version := "0.130.0"
	var v struct {
		Latest string `json:"latest_version"`
	}
	if readJSON(l.path(".codex", "version.json"), &v) && v.Latest != "" {
		version = v.Latest
	}
	b, err := l.fetch(ctx, http.MethodGet, l.url("chatgpt.models", "https://chatgpt.com/backend-api/codex/models")+"?client_version="+version,
		map[string]string{"Authorization": "Bearer " + token, "chatgpt-account-id": accountID}, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Models []struct {
			Slug       string `json:"slug"`
			Name       string `json:"display_name"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("not a model list")
	}
	var out []Model
	for _, m := range r.Models {
		if m.Slug != "" && (m.Visibility == "" || m.Visibility == "list") {
			out = append(out, Model{ID: m.Slug, Name: m.Name, Media: "text"})
		}
	}
	return out, nil
}

// parseChatGPTUsage reads /wham/usage:
//
//	{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":12,
//	 "limit_window_seconds":18000,"reset_at":1759000000,"reset_after_seconds":3600},
//	 "secondary_window":{…}},"credits":{"has_credits":true,"unlimited":false,"balance":"12.5"}}
func parseChatGPTUsage(b []byte, now time.Time) (string, []Window, []Balance, error) {
	type window struct {
		UsedPercent     *float64 `json:"used_percent"`
		LimitWindowSecs int64    `json:"limit_window_seconds"`
		ResetAt         int64    `json:"reset_at"`
		ResetAfterSecs  int64    `json:"reset_after_seconds"`
	}
	var u struct {
		PlanType  string `json:"plan_type"`
		RateLimit *struct {
			Primary   *window `json:"primary_window"`
			Secondary *window `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits *struct {
			Has       bool   `json:"has_credits"`
			Unlimited bool   `json:"unlimited"`
			Balance   string `json:"balance"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return "", nil, nil, errors.New("ChatGPT answered something Ant doesn't understand")
	}
	var windows []Window
	if u.RateLimit != nil {
		for _, w := range []*window{u.RateLimit.Primary, u.RateLimit.Secondary} {
			if w == nil {
				continue
			}
			out := Window{Name: spanName(w.LimitWindowSecs), UsedPercent: w.UsedPercent}
			switch {
			case w.ResetAt > 0:
				out.ResetsAt = ptr(time.Unix(w.ResetAt, 0).UTC())
			case w.ResetAfterSecs > 0:
				out.ResetsAt = ptr(now.Add(time.Duration(w.ResetAfterSecs) * time.Second).UTC())
			}
			windows = append(windows, out)
		}
	}
	var balances []Balance
	if c := u.Credits; c != nil && c.Has && !c.Unlimited {
		if n, err := strconv.ParseFloat(strings.TrimSpace(c.Balance), 64); err == nil && n > 0 {
			balances = append(balances, Balance{Currency: "credits", Amount: strings.TrimSpace(c.Balance)})
		}
	}
	return catalog.PlanName(u.PlanType), windows, balances, nil
}

func spanName(secs int64) string {
	switch {
	case secs == 7*24*3600:
		return "Week"
	case secs > 0 && secs%(24*3600) == 0:
		return fmt.Sprintf("%d days", secs/(24*3600))
	case secs > 0 && secs%3600 == 0:
		return fmt.Sprintf("%d hours", secs/3600)
	}
	return "Allowance"
}
