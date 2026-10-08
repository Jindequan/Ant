package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kimi (Moonshot AI) is paid for two ways, each its own account:
//   - Kimi Code: a subscription on a key used at api.kimi.com/coding, whose
//     /usages reports the plan's windows (as kimi-cli and yetone/magpie
//     read it).
//   - The Moonshot open platform: a prepaid balance read from the official
//     GET /v1/users/me/balance (api.moonshot.cn in China, .ai elsewhere).

func kimiProvider() Provider {
	return Provider{ID: "kimi", Name: "Kimi", Link: "https://www.kimi.com/code", Modes: []Mode{
		{
			ID: "code", Name: "Kimi Code", Kind: KindSubscription, Live: true, Key: "apiKey",
			Rule:   "A subscription for coding tools: its windows (a rolling few hours and a week) renew on their own clocks.",
			Fields: keyField("sk-kimi-…", "The key from your Kimi Code console, used at api.kimi.com/coding."),
			Discover: func(l Local) []Connection {
				return keyConnections(l, "kimi", "code", keyHint{Hosts: []string{"api.kimi.com"}, Path: "/coding",
					OpenCode: []string{"kimi-for-coding"}, Env: []string{"KIMI_CODE_API_KEY"}}, nil)
			},
			Read: readKimiCode,
		},
		{
			ID: "api", Name: "Moonshot API balance", Kind: KindAPI, Live: true, Key: "apiKey",
			Rule: "Prepaid balance on the Moonshot open platform, spent per token. Vouchers are used before cash.",
			Fields: []Field{
				{ID: "apiKey", Label: "API key", Type: "secret", Placeholder: "sk-…", Help: "From platform.moonshot.cn (or .ai) › API keys."},
				{ID: "region", Label: "Platform", Type: "select", Options: []Option{{"cn", "platform.moonshot.cn (China)"}, {"ai", "platform.moonshot.ai (International)"}}},
			},
			Discover: func(l Local) []Connection {
				cn := keyConnections(l, "kimi", "api", keyHint{Hosts: []string{"api.moonshot.cn"}, OpenCode: []string{"moonshotai-cn"}, Env: []string{"MOONSHOT_API_KEY"}},
					func(foundKey) map[string]string { return map[string]string{"region": "cn"} })
				ai := keyConnections(l, "kimi", "api", keyHint{Hosts: []string{"api.moonshot.ai"}, OpenCode: []string{"moonshotai"}},
					func(foundKey) map[string]string { return map[string]string{"region": "ai"} })
				return append(cn, ai...)
			},
			Read: readMoonshotBalance,
		},
	}}
}

func readKimiCode(ctx context.Context, l Local, c Connection) Account {
	p := kimiProvider()
	a := base(p, p.Modes[0], c, l.Now())
	var raw json.RawMessage
	if !getJSON(ctx, l, l.url("kimi.code", "https://api.kimi.com/coding/v1/usages"), c.Values["apiKey"], "Kimi", &a, &raw) {
		return a
	}
	ws, err := parseKimiUsages(raw)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status, a.Windows = StatusOK, ws
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("kimi.code.models", "https://api.kimi.com/coding/v1/models"))
	return a
}

// parseKimiUsages reads
//
//	{"usage":{"limit":"100","used":"12","resetTime":"…"},
//	 "limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
//	            "detail":{"limit":"20","remaining":"18","resetTime":"…"}}]}
//
// usage is the week; each limit a shorter window.
func parseKimiUsages(b []byte) ([]Window, error) {
	type row map[string]any
	var r struct {
		Usage  row `json:"usage"`
		Limits []struct {
			Window row `json:"window"`
			Detail row `json:"detail"`
		} `json:"limits"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, fmt.Errorf("Kimi answered something Ant doesn't understand")
	}
	num := func(v any) (float64, bool) {
		switch x := v.(type) {
		case float64:
			return x, true
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			return f, err == nil
		}
		return 0, false
	}
	window := func(d row, name string) (Window, bool) {
		limit, ok := num(d["limit"])
		if !ok || limit <= 0 {
			return Window{}, false
		}
		used, ok := num(d["used"])
		if !ok {
			left, ok := num(d["remaining"])
			if !ok {
				return Window{}, false
			}
			used = limit - left
		}
		w := Window{Name: name, UsedPercent: ptr(max(0, min(100, used/limit*100)))}
		for _, k := range []string{"resetTime", "resetAt", "reset_at", "reset_time"} {
			if s, _ := d[k].(string); s != "" {
				if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
					w.ResetsAt = ptr(t.UTC())
					break
				}
			}
		}
		return w, true
	}
	var out []Window
	for _, lim := range r.Limits {
		d := lim.Detail
		if d == nil {
			d = lim.Window
		}
		n, _ := num(lim.Window["duration"])
		unit, _ := lim.Window["timeUnit"].(string)
		var span time.Duration
		switch {
		case strings.Contains(unit, "MINUTE"):
			span = time.Duration(n) * time.Minute
		case strings.Contains(unit, "HOUR"):
			span = time.Duration(n) * time.Hour
		case strings.Contains(unit, "DAY"):
			span = time.Duration(n) * 24 * time.Hour
		}
		if w, ok := window(d, durationName(span)); ok {
			out = append(out, w)
		}
	}
	if w, ok := window(r.Usage, "Week"); ok {
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Kimi reported no usage windows for this key")
	}
	return out, nil
}

// durationName names a window by its length: "5 hours", "7 days".
func durationName(d time.Duration) string {
	switch {
	case d == 7*24*time.Hour:
		return "Week"
	case d > 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%d days", d/(24*time.Hour))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d hours", d/time.Hour)
	case d > 0:
		return fmt.Sprintf("%d minutes", d/time.Minute)
	}
	return "Allowance"
}

func readMoonshotBalance(ctx context.Context, l Local, c Connection) Account {
	p := kimiProvider()
	a := base(p, p.Modes[1], c, l.Now())
	host, currency := "https://api.moonshot.cn", "CNY"
	a.Link = "https://platform.moonshot.cn/console/info"
	if c.Values["region"] == "ai" {
		host, currency, a.Link = "https://api.moonshot.ai", "USD", "https://platform.moonshot.ai/console/info"
	}
	var r struct {
		Data struct {
			Available flexNumber `json:"available_balance"`
			Voucher   flexNumber `json:"voucher_balance"`
			Cash      flexNumber `json:"cash_balance"`
		} `json:"data"`
	}
	if !getJSON(ctx, l, l.url("kimi.api", host)+"/v1/users/me/balance", c.Values["apiKey"], "Moonshot", &a, &r) {
		return a
	}
	a.Status = StatusOK
	a.Balances = []Balance{{Currency: currency, Amount: money2(float64(r.Data.Available)),
		Granted: money2(float64(r.Data.Voucher)), ToppedUp: money2(float64(r.Data.Cash))}}
	if r.Data.Available <= 0 {
		a.Message = "The balance is used up. Top up to keep using the API."
	}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("kimi.api", host)+"/v1/models")
	return a
}
