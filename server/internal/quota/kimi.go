package quota

import (
	"context"
	"encoding/json"
	"time"

	"ant/server/internal/providers/kimi"
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
			Link: func(v map[string]string) string {
				if v["region"] == "ai" {
					return "https://platform.moonshot.ai/console/info"
				}
				return "https://platform.moonshot.cn/console/info"
			},
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
	ws, err := kimi.ParseUsages(raw)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status = StatusOK
	for _, w := range ws {
		out := Window{Name: w.Name, UsedPercent: ptr(w.UsedPercent)}
		if w.ResetsAt > 0 {
			out.ResetsAt = ptr(time.UnixMilli(w.ResetsAt).UTC())
		}
		a.Windows = append(a.Windows, out)
	}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("kimi.code.models", "https://api.kimi.com/coding/v1/models"))
	return a
}

// readMoonshotBalance reads the balance with the module's exact parser:
// every digit Moonshot reports is kept, and cash below zero (arrears) shows
// as it is.
func readMoonshotBalance(ctx context.Context, l Local, c Connection) Account {
	p := kimiProvider()
	a := base(p, p.Modes[1], c, l.Now())
	host, currency := "https://api.moonshot.cn", "CNY"
	if c.Values["region"] == "ai" {
		host, currency = "https://api.moonshot.ai", "USD"
	}
	var raw json.RawMessage
	if !getJSON(ctx, l, l.url("kimi.api", host)+"/v1/users/me/balance", c.Values["apiKey"], "Moonshot", &a, &raw) {
		return a
	}
	b, err := kimi.ParseBalance(raw)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status, a.Message = StatusOK, kimi.Notes(b, currency)
	a.Balances = []Balance{{Currency: currency, Amount: kimi.Money(b.Available), Granted: kimi.Money(b.Voucher), ToppedUp: kimi.Money(b.Cash)}}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("kimi.api", host)+"/v1/models")
	return a
}
