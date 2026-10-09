package quota

import (
	"context"
	"strconv"
	"time"
)

// Demo mode shows made-up accounts covering every kind and state, for
// working on the page without reading anything. They behave as found
// accounts; accounts added in demo mode stay in memory, and only modes
// that need no provider read (rule-only ones) are read for them.

func demoConnections() []Connection {
	found := func(id, provider, mode, source string) Connection {
		return Connection{ID: id, Provider: provider, Mode: mode, Origin: OriginFound, Source: source, Values: map[string]string{}}
	}
	way := func(c Connection, w string) Connection { c.Way = w; return c }
	return []Connection{
		way(found("demo-claude", "claude", "plan", "Claude Code"), "claude-code"),
		way(found("demo-copilot", "copilot", "plan", "Copilot CLI"), "cli"),
		way(found("demo-gemini", "gemini", "code-assist", "~/.gemini/oauth_creds.json"), "cli"),
		found("demo-zai", "zai", "coding-plan", "ZCode"),
		found("demo-chatgpt", "chatgpt", "plan", "~/.codex/auth.json"),
		found("demo-deepseek", "deepseek", "api", "ZCode"),
		found("demo-suno", "suno", "account", "Chrome · Default"),
		found("demo-opencode", "opencode", "go", "ZCode"),
		way(found("demo-aliyun-plan", "aliyun", "token-plan", "Bailian CLI"), "bl"),
		way(found("demo-aliyun-payg", "aliyun", "payg", "~/.aliyun/config.json"), "aliyun-cli"),
		way(found("demo-aliyun-free", "aliyun", "free-quota", "Bailian CLI"), "bl"),
		found("demo-kimi", "kimi", "code", "OpenCode"),
		found("demo-minimax", "minimax", "token-plan", "ZCode"),
		way(found("demo-volc", "volcengine", "coding-plan", "Volcengine CLI"), "ve"),
		found("demo-mimo", "xiaomi", "token-plan", "Signed in with Ant"),
		found("demo-openrouter", "openrouter", "credits", "$OPENROUTER_API_KEY"),
		found("demo-silicon", "siliconflow", "api", "OpenCode"),
		way(found("demo-manus", "manus", "credits", "Safari"), "browser"),
		way(found("demo-kling", "kling", "credits", "Chrome · Default"), "browser"),
		way(found("demo-leonardo", "leonardo", "tokens", "Signed in with Ant"), "sign-in"),
		way(found("demo-seaart", "seaart", "stamina", "Safari"), "browser"),
		way(found("demo-eleven", "elevenlabs", "characters", "$ELEVENLABS_API_KEY"), ""),
		way(found("demo-figma", "figma", "ai-credits", ""), ""),
	}
}

// demoModels are what the sample accounts list, overlapping as real ones
// do: one model reachable through several accounts.
var demoModels = map[string][]string{
	"demo-claude":     {"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"},
	"demo-chatgpt":    {"gpt-5.5-codex", "gpt-5.5", "gpt-5.5-mini"},
	"demo-copilot":    {"gpt-5.5", "claude-sonnet-5-5", "gemini-2.5-pro"},
	"demo-zai":        {"glm-5.3", "glm-5.3-air"},
	"demo-kimi":       {"kimi-k3", "kimi-for-coding"},
	"demo-deepseek":   {"deepseek-v4-pro", "deepseek-v4"},
	"demo-openrouter": {"anthropic/claude-sonnet-5-5", "openai/gpt-5.5", "moonshotai/kimi-k3", "deepseek/deepseek-v4-pro", "z-ai/glm-5.3"},
	"demo-silicon":    {"deepseek-ai/deepseek-v4-pro", "moonshotai/kimi-k3", "zai-org/glm-5.3"},
	"demo-opencode":   {"glm-5.3", "kimi-k3", "deepseek-v4-pro"},
	"demo-minimax":    {"minimax-m3"},
}

func demoAccounts(conns []Connection, now time.Time) []Account {
	at := func(d time.Duration) *time.Time { return ptr(now.Add(d).UTC()) }
	y, m, d := now.UTC().Date()
	midnight := ptr(time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC))
	read := now.Add(-2 * time.Minute)
	samples := map[string]func(Account) Account{
		"demo-manus": func(a Account) Account {
			a.SessionEndsAt = at(26 * 24 * time.Hour)
			a.Balances = []Balance{{Label: "Credits", Currency: "credits", Amount: "1300"}}
			a.Windows = []Window{{Name: "Refresh · day", Left: ptr(180.0), Limit: ptr(300.0), Unit: "credits", ResetsAt: midnight}}
			a.Groups = []Group{{Title: "Kept credits", Note: "They don't renew; spent after the refresh and plan credits.", Balances: []Balance{{Label: "Free", Currency: "credits", Amount: "1000"}}}}
			return a
		},
		"demo-kling": func(a Account) Account {
			a.SessionEndsAt = at(2*24*time.Hour + 5*time.Hour) // a browser sign-in about to end
			a.Balances = []Balance{{Label: "Credits", Currency: "credits", Amount: "166"}}
			a.Windows = []Window{
				{Name: "Daily free credits", Left: ptr(66.0), Unit: "credits", ResetsAt: midnight, Expires: true},
				{Name: "Credits", Left: ptr(100.0), Unit: "credits", ResetsAt: at(20 * 24 * time.Hour), Expires: true},
			}
			return a
		},
		"demo-leonardo": func(a Account) Account {
			a.Plan, a.User = "Free", "you@example.com"
			a.Windows = []Window{{Name: "Today", Left: ptr(95.0), Limit: ptr(150.0), Unit: "tokens", ResetsAt: midnight}}
			return a
		},
		"demo-seaart": func(a Account) Account {
			a.Windows = []Window{{Name: "Stamina · today", Left: ptr(130.0), Unit: "stamina", ResetsAt: midnight}}
			a.Balances = []Balance{{Label: "Credits", Currency: "credits", Amount: "25"}}
			return a
		},
		"demo-eleven": func(a Account) Account {
			a.Plan = "Free"
			a.Windows = []Window{{Name: "Credits · month", Used: ptr(2500.0), Limit: ptr(10000.0), Left: ptr(7500.0), Unit: "credits", ResetsAt: at(18 * 24 * time.Hour)}}
			return a
		},
		"demo-claude": func(a Account) Account {
			a.Windows = []Window{
				{Name: "5 hours", UsedPercent: ptr(34.0), ResetsAt: at(3*time.Hour + 20*time.Minute)},
				{Name: "Week", UsedPercent: ptr(58.0), ResetsAt: at(4*24*time.Hour + 6*time.Hour)},
				{Name: "Week · Opus", UsedPercent: ptr(12.0), ResetsAt: at(4*24*time.Hour + 6*time.Hour)},
			}
			return a
		},
		"demo-copilot": func(a Account) Account {
			y, m, _ := now.UTC().Date()
			a.Plan, a.User = "Pro", "octocat"
			a.Windows = []Window{{Name: "Premium requests", Left: ptr(212.0), Limit: ptr(300.0), Unit: "requests", ResetsAt: ptr(time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC))}}
			return a
		},
		"demo-gemini": func(a Account) Account {
			a.Plan, a.User = "Gemini Code Assist for individuals", "you@gmail.com"
			a.Windows = []Window{
				{Name: "gemini-2.5-flash", Model: "gemini-2.5-flash", UsedPercent: ptr(3.0), ResetsAt: midnight},
				{Name: "gemini-2.5-pro", Model: "gemini-2.5-pro", UsedPercent: ptr(41.0), ResetsAt: midnight},
			}
			return a
		},
		"demo-zai": func(a Account) Account {
			a.Plan, a.Windows = "Pro", []Window{
				{Name: "5 hours", UsedPercent: ptr(18.0), ResetsAt: at(2*time.Hour + 14*time.Minute)},
				{Name: "Week", UsedPercent: ptr(46.0), ResetsAt: at(3*24*time.Hour + 5*time.Hour)},
				{Name: "MCP tools · month", UsedPercent: ptr(4.0), ResetsAt: at(19 * 24 * time.Hour), Aside: true},
			}
			return a
		},
		"demo-chatgpt": func(a Account) Account {
			a.Plan, a.User, a.RenewsAt = "Plus", "you@example.com", at(6*24*time.Hour+3*time.Hour)
			a.Windows = []Window{
				{Name: "5 hours", UsedPercent: ptr(72.0), ResetsAt: at(48 * time.Minute)},
				{Name: "Week", UsedPercent: ptr(91.0), ResetsAt: at(1*24*time.Hour + 9*time.Hour)},
			}
			return a
		},
		"demo-deepseek": func(a Account) Account {
			a.Balances = []Balance{{Currency: "CNY", Amount: "23.40", Granted: "10.00", ToppedUp: "13.40"}}
			return a
		},
		"demo-suno": func(a Account) Account {
			a.Plan, a.Windows = "Free", []Window{{Name: "Today", Left: ptr(40.0), Limit: ptr(50.0), Unit: "credits", ResetsAt: midnight, ResetEstimated: true}}
			return a
		},
		"demo-aliyun-plan": func(a Account) Account {
			ends := at(10*24*time.Hour + 2*time.Hour)
			a.Plan, a.EndsAt, a.AutoRenew = "Standard", ends, ptr(false)
			a.Windows = []Window{{Name: "Month", UsedPercent: ptr(100.0), ResetsAt: ends}}
			a.Groups = []Group{{Title: "Tool benefits", Note: "Other AgentStudio tools are 12% off while the plan is active.", Windows: []Window{
				{Name: "Image generation MCP", Left: ptr(20.0), Limit: ptr(20.0), Unit: "images", ResetsAt: ends, Expires: true},
				{Name: "Speech recognition MCP", Left: ptr(18000.0), Limit: ptr(18000.0), Unit: "seconds", ResetsAt: ends, Expires: true},
				{Name: "Web page parsing MCP", Left: ptr(1000.0), Limit: ptr(1000.0), Unit: "calls", ResetsAt: ends, Expires: true},
			}}}
			return a
		},
		"demo-aliyun-payg": func(a Account) Account {
			a.Balances = []Balance{{Label: "Balance", Currency: "CNY", Amount: "0.01"}}
			a.Groups = []Group{{Title: "Charged by Bailian", Note: "Usage worth last month ¥12.30 before free quota, plans and discounts.",
				Balances: []Balance{{Label: "This month", Currency: "CNY", Amount: "0.00"}, {Label: "Last month", Currency: "CNY", Amount: "1.50"}}}}
			return a
		},
		"demo-aliyun-free": func(a Account) Account {
			day := func(n int) *time.Time { return at(time.Duration(n) * 24 * time.Hour) }
			a.Windows = []Window{
				{Name: "qwen-image-3.0", Media: "image", Left: ptr(10.0), Limit: ptr(10.0), Unit: "images", ResetsAt: day(27), Expires: true},
				{Name: "wan3.0-video", Media: "video", Left: ptr(30.0), Limit: ptr(30.0), Unit: "seconds", ResetsAt: day(29), Expires: true},
				{Name: "kimi-k3", Left: ptr(1000000.0), Limit: ptr(1000000.0), Unit: "tokens", ResetsAt: day(41), Expires: true},
				{Name: "glm-5.3", Left: ptr(812345.0), Limit: ptr(1000000.0), Unit: "tokens", ResetsAt: day(52), Expires: true},
				{Name: "qwen3.8-max", Left: ptr(420000.0), Limit: ptr(1000000.0), Unit: "tokens", ResetsAt: day(60), Expires: true},
				{Name: "deepseek-v4-pro", Left: ptr(1000000.0), Limit: ptr(1000000.0), Unit: "tokens", ResetsAt: day(71), Expires: true},
				{Name: "cosyvoice-v4", Media: "speech", Left: ptr(20000.0), Limit: ptr(20000.0), Unit: "characters", ResetsAt: day(80), Expires: true},
				{Name: "qwen-image-2.1-pro", Media: "image", Left: ptr(10.0), Limit: ptr(10.0), Unit: "images", ResetsAt: day(86), Expires: true},
			}
			for i := range a.Windows {
				a.Windows[i].Model = a.Windows[i].Name
				if a.Windows[i].Media == "" {
					a.Windows[i].Media = "text"
				}
			}
			a.Message = "7 models with free quota · 445 more used up or expired."
			return a
		},
		"demo-kimi": func(a Account) Account {
			a.Windows = []Window{
				{Name: "5 hours", UsedPercent: ptr(64.0), ResetsAt: at(3*time.Hour + 20*time.Minute)},
				{Name: "Week", UsedPercent: ptr(38.0), ResetsAt: at(5 * 24 * time.Hour)},
			}
			return a
		},
		"demo-minimax": func(a Account) Account {
			a.Windows = []Window{
				{Name: "5 hours", UsedPercent: ptr(12.0), ResetsAt: at(90 * time.Minute)},
				{Name: "Week", UsedPercent: ptr(27.0), ResetsAt: at(2 * 24 * time.Hour)},
				{Name: "Video · Day", UsedPercent: ptr(100.0), ResetsAt: at(10 * time.Hour), Aside: true},
			}
			return a
		},
		"demo-volc": func(a Account) Account {
			a.Windows = []Window{
				{Name: "5 hours", UsedPercent: ptr(8.0), ResetsAt: at(4*time.Hour + 30*time.Minute)},
				{Name: "Week", UsedPercent: ptr(55.0), ResetsAt: at(5*24*time.Hour + 6*time.Hour)},
				{Name: "Month", UsedPercent: ptr(31.0), ResetsAt: at(23 * 24 * time.Hour)},
			}
			return a
		},
		"demo-mimo": func(a Account) Account {
			ends := at(27 * 24 * time.Hour)
			a.Plan, a.EndsAt = "Standard", ends
			a.Windows = []Window{{Name: "Plan credits", Used: ptr(250000.0), Limit: ptr(1000000.0), Left: ptr(750000.0), Unit: "credits", ResetsAt: ends}}
			return a
		},
		"demo-openrouter": func(a Account) Account {
			a.Balances = []Balance{{Currency: "USD", Amount: "16.50"}}
			return a
		},
		"demo-silicon": func(a Account) Account {
			a.Balances = []Balance{{Currency: "CNY", Amount: "88.88", Granted: "14.00", ToppedUp: "74.88"}}
			return a
		},
		"demo-opencode": func(a Account) Account {
			a.Windows = []Window{
				{Name: "5 hours", UsedPercent: ptr(5.0), ResetsAt: at(4 * time.Hour)},
				{Name: "Week", UsedPercent: ptr(33.0), ResetsAt: at(4 * 24 * time.Hour)},
				{Name: "Month", UsedPercent: ptr(12.0), ResetsAt: at(21 * 24 * time.Hour)},
			}
			return a
		},
	}
	providers := Providers()
	local := Local{Now: func() time.Time { return now }}
	out := make([]Account, 0, len(conns))
	for _, c := range conns {
		p, _ := providerByID(providers, c.Provider)
		m, ok := p.mode(c.Mode)
		if !ok {
			continue
		}
		switch sample, isSample := samples[c.ID]; {
		case isSample && c.Origin == OriginFound:
			a := base(p, m, c, read)
			a.Status = StatusOK
			a = sample(a)
			if ms, ok := demoModels[c.ID]; ok {
				a.Models = fixedModels(ms...)
			}
			for _, w := range a.Windows {
				if w.Model != "" {
					a.Models = append(a.Models, Model{ID: w.Model, Media: firstNonEmpty(w.Media, "text")})
				}
			}
			out = append(out, a)
		case !m.Live:
			out = append(out, m.Read(context.Background(), local, c))
		default:
			a := base(p, m, c, now)
			a.Status, a.Message = StatusError, "Demo mode doesn't read providers. Start Ant without -demo to read this account."
			out = append(out, a)
		}
	}
	return out
}

// demoInsights are sample outlooks for the demo accounts, derived from
// their own numbers, so the page shows what history adds without reading
// anything: allowances below half that reset in more than three hours run
// out first; the rest last; balances drain over a few weeks.
func demoInsights(accounts []Account, now time.Time) map[string][]Insight {
	out := map[string][]Insight{}
	for _, a := range accounts {
		if a.Status != StatusOK {
			continue
		}
		for _, w := range a.Windows {
			left := demoLeft(w)
			if left == nil || w.ResetsAt == nil || *left <= 0 {
				continue
			}
			in := Insight{Window: w.Name, Model: w.Model, Outlook: "lasts", Unit: w.Unit}
			if *left < 50 && w.ResetsAt.Sub(now) > 3*time.Hour {
				at := now.Add(w.ResetsAt.Sub(now) * time.Duration(*left) / 100)
				in.Outlook, in.At = "runs_out", &at
			}
			out[a.ID] = append(out[a.ID], in)
		}
		for i, b := range a.Balances {
			amount, err := strconv.ParseFloat(b.Amount, 64)
			if err != nil || amount <= 0 {
				continue
			}
			days := 18 + 9*float64(i+len(a.ID)%5)
			perDay := amount / days
			at := now.Add(time.Duration(days * float64(24*time.Hour)))
			in := Insight{Currency: b.Currency, Label: b.Label, Outlook: "runs_out", At: &at, PerDay: &perDay, Unit: b.Currency}
			for d := 7; d >= 0; d-- {
				t := now.Add(-time.Duration(d) * 24 * time.Hour)
				in.Points = append(in.Points, [2]float64{float64(t.UnixMilli()), amount + perDay*float64(d) + float64(d%3)*perDay/3})
			}
			out[a.ID] = append(out[a.ID], in)
		}
	}
	return out
}

func demoLeft(w Window) *float64 {
	switch {
	case w.UsedPercent != nil:
		v := 100 - *w.UsedPercent
		return &v
	case w.Left != nil && w.Limit != nil && *w.Limit > 0:
		v := *w.Left / *w.Limit * 100
		return &v
	}
	return nil
}

// DemoAccounts are the sample accounts, as demo mode shows them now.
func DemoAccounts(now time.Time) []Account { return demoAccounts(demoConnections(), now) }
