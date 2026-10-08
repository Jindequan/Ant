package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// MiniMax's Token Plan (Coding Plan) is a subscription on its own sk-cp-
// key, whose /v1/token_plan/remains reports, per model group, a short
// window and a weekly one as percent remaining (shape as read by
// yetone/magpie). A pay-as-you-go key has no plan to report.

func miniMaxProvider() Provider {
	return Provider{ID: "minimax", Name: "MiniMax", Link: "https://platform.minimaxi.com/user-center/payment/token-plan", Modes: []Mode{{
		ID: "token-plan", Name: "Token Plan", Kind: KindSubscription, Live: true, Key: "apiKey",
		Rule: "A subscription per model group: a short rolling window and a weekly one, each renewing on its own clock. Video and other groups are counted apart from text.",
		Fields: []Field{
			{ID: "apiKey", Label: "Token Plan key", Type: "secret", Placeholder: "sk-cp-…", Help: "From MiniMax › Token Plan. An ordinary pay-as-you-go key has no plan to read."},
			{ID: "region", Label: "Platform", Type: "select", Options: []Option{{"cn", "minimaxi.com (China)"}, {"io", "minimax.io (International)"}}},
		},
		Discover: func(l Local) []Connection {
			cn := keyConnections(l, "minimax", "token-plan", keyHint{Hosts: []string{"api.minimaxi.com"}, Prefix: "sk-cp-",
				OpenCode: []string{"minimax-cn-coding-plan", "minimax-cn"}, Env: []string{"MINIMAX_API_KEY"}},
				func(foundKey) map[string]string { return map[string]string{"region": "cn"} })
			io := keyConnections(l, "minimax", "token-plan", keyHint{Hosts: []string{"api.minimax.io"}, Prefix: "sk-cp-",
				OpenCode: []string{"minimax-coding-plan", "minimax"}},
				func(foundKey) map[string]string { return map[string]string{"region": "io"} })
			return append(cn, io...)
		},
		Read: readMiniMaxPlan,
	}}}
}

func readMiniMaxPlan(ctx context.Context, l Local, c Connection) Account {
	p := miniMaxProvider()
	a := base(p, p.Modes[0], c, l.Now())
	host := "https://api.minimaxi.com"
	if c.Values["region"] == "io" {
		host, a.Link = "https://api.minimax.io", "https://platform.minimax.io/user-center/payment/token-plan"
	}
	var raw json.RawMessage
	if !getJSON(ctx, l, l.url("minimax", host)+"/v1/token_plan/remains", c.Values["apiKey"], "MiniMax", &a, &raw) {
		return a
	}
	ws, err := parseMiniMaxRemains(raw)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status, a.Windows = StatusOK, ws
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("minimax", host)+"/v1/models")
	return a
}

// parseMiniMaxRemains reads
//
//	{"model_remains":[{"model_name":"general","start_time":…,"end_time":…,
//	   "current_interval_remaining_percent":100,"current_interval_status":1,
//	   "weekly_start_time":…,"weekly_end_time":…,"current_weekly_remaining_percent":100,
//	   "current_weekly_status":1},{"model_name":"video",…}],
//	 "base_resp":{"status_code":0,"status_msg":"success"}}
//
// status 3 is unlimited, 2 used up. The "general" group is the plan's main
// allowance; other groups are shown beside it.
func parseMiniMaxRemains(b []byte) ([]Window, error) {
	type bucket struct {
		Model      string   `json:"model_name"`
		Start      int64    `json:"start_time"`
		End        int64    `json:"end_time"`
		Left       *float64 `json:"current_interval_remaining_percent"`
		Status     int      `json:"current_interval_status"`
		WeekEnd    int64    `json:"weekly_end_time"`
		WeekLeft   *float64 `json:"current_weekly_remaining_percent"`
		WeekStatus int      `json:"current_weekly_status"`
	}
	var r struct {
		Remains []bucket `json:"model_remains"`
		Base    *struct {
			Code int    `json:"status_code"`
			Msg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if json.Unmarshal(b, &r) != nil || r.Base == nil {
		return nil, fmt.Errorf("MiniMax answered without a plan")
	}
	if r.Base.Code != 0 {
		return nil, fmt.Errorf("MiniMax: %s", r.Base.Msg)
	}
	at := func(n int64) *time.Time {
		if n <= 0 {
			return nil
		}
		if n < 1e12 {
			return ptr(time.Unix(n, 0).UTC())
		}
		return ptr(time.UnixMilli(n).UTC())
	}
	var out []Window
	for _, k := range r.Remains {
		name := strings.TrimSpace(k.Model)
		if name == "" {
			continue
		}
		general := strings.EqualFold(name, "general")
		for _, x := range []struct {
			left       *float64
			status     int
			start, end int64
			week       bool
		}{{k.Left, k.Status, k.Start, k.End, false}, {k.WeekLeft, k.WeekStatus, 0, k.WeekEnd, true}} {
			if x.status == 3 || (x.left == nil && x.status != 2) {
				continue // unlimited, or not told
			}
			used := 100.0
			if x.status != 2 {
				used = max(0, min(100, 100-*x.left))
			}
			w := Window{UsedPercent: ptr(used), ResetsAt: at(x.end)}
			switch {
			case x.week:
				w.Name = "Week"
			case x.start > 0 && x.end > x.start:
				s, e := at(x.start), at(x.end)
				w.Name = catalog.WindowName(e.Sub(*s))
			default:
				w.Name = "Allowance"
			}
			if !general {
				w.Name = strings.ToUpper(name[:1]) + name[1:] + " · " + w.Name
				w.Aside = true
			}
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("MiniMax reported no plan windows for this key")
	}
	return out, nil
}
