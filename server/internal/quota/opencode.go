package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// OpenCode Go is a subscription on an API key (zen/go), whose usage
// endpoint reports a rolling 5-hour, a weekly and a monthly window
// (shape as read by yetone/magpie).

func openCodeProvider() Provider {
	return Provider{ID: "opencode", Name: "OpenCode Go", Link: "https://opencode.ai/auth", Modes: []Mode{{
		ID: "go", Name: "Go plan", Kind: KindSubscription, Live: true, Key: "apiKey",
		Rule: "A rolling 5-hour window, a weekly window and a monthly window, all on one key.",
		Fields: []Field{
			{ID: "apiKey", Label: "API key", Type: "secret", Placeholder: "sk-…", Help: "The key you use with opencode.ai/zen/go."},
		},
		Discover: discoverOpenCode,
		Read:     readOpenCode,
	}}}
}

func discoverOpenCode(l Local) []Connection {
	var out []Connection
	for _, k := range l.zcodeKeys() {
		if hostOf(k.BaseURL) == "opencode.ai" && strings.Contains(k.BaseURL, "/zen/go") {
			out = append(out, Connection{ID: fingerprint("opencode", "go", k.Key), Provider: "opencode", Mode: "go",
				Origin: OriginFound, Source: "ZCode", Values: map[string]string{"apiKey": k.Key}})
		}
	}
	return out
}

func readOpenCode(ctx context.Context, l Local, c Connection) Account {
	p := openCodeProvider()
	a := base(p, p.Modes[0], c, l.Now())
	b, err := l.fetch(ctx, http.MethodGet, l.url("opencode.usage", "https://opencode.ai/zen/go/v1/usage"),
		map[string]string{"Authorization": "Bearer " + c.Values["apiKey"]}, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read usage: "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "OpenCode refused the key, or it isn't on the Go plan."
		}
		return a
	}
	ws, err := parseOpenCodeUsage(b)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status, a.Windows = StatusOK, ws
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("opencode.models", "https://opencode.ai/zen/go/v1/models"))
	return a
}

// parseOpenCodeUsage reads
//
//	{"usage":{"rolling":{"status":"ok","percent":37,"resetsAt":"2026-08-26T14:12:03.000Z"},
//	 "weekly":{…},"monthly":{…}}}
func parseOpenCodeUsage(b []byte) ([]Window, error) {
	var r struct {
		Usage map[string]struct {
			Percent  *float64 `json:"percent"`
			ResetsAt string   `json:"resetsAt"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, errors.New("OpenCode answered something Ant doesn't understand")
	}
	var out []Window
	for _, x := range []struct{ key, name string }{{"rolling", "5 hours"}, {"weekly", "Week"}, {"monthly", "Month"}} {
		u, ok := r.Usage[x.key]
		if !ok || u.Percent == nil {
			continue
		}
		w := Window{Name: x.name, UsedPercent: u.Percent}
		if t, err := time.Parse(time.RFC3339, u.ResetsAt); err == nil {
			w.ResetsAt = ptr(t.UTC())
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil, errors.New("OpenCode reported no usage windows")
	}
	return out, nil
}
