package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// Claude (Pro / Max) subscription windows, read three ways, easiest first:
//
//  1. Ask Claude Code itself: `claude -p /usage` is answered by the CLI from
//     its own sign-in, calling no model (as yetone/magpie reads it).
//  2. Claude Code's sign-in (~/.claude/.credentials.json): its OAuth token
//     reads Anthropic's usage endpoint, as Claude Code's /usage does.
//  3. An OAuth token the user gives (`claude setup-token`).

func claudeProvider() Provider {
	return Provider{ID: "claude", Name: "Claude", Link: "https://claude.ai/settings/usage", Modes: []Mode{{
		ID: "plan", Name: "Claude plan", Kind: KindSubscription, Live: true,
		Rule: "A 5-hour session window and a weekly window (some plans also count Opus or Sonnet weekly on their own), each renewing on its own clock.",
		Ways: []Way{
			{ID: "claude-code", Name: "Claude Code", Hint: "Ant asks Claude Code itself (claude -p /usage). It calls no model. Install Claude Code and sign in with your Claude account.",
				Fields: []Field{}, Discover: discoverClaudeCode, Read: readClaudeCode},
			{ID: "sign-in", Name: "Claude Code's sign-in", Hint: "Ant reads the sign-in Claude Code keeps in ~/.claude/.credentials.json.",
				Fields: []Field{}, Discover: discoverClaudeSignIn, Read: readClaudeOAuth},
			{ID: "token", Name: "OAuth token", Key: "accessToken",
				Fields: []Field{{ID: "accessToken", Label: "OAuth token", Type: "secret", Placeholder: "sk-ant-oat01-…", Help: "Run claude setup-token in Terminal and paste what it prints."}},
				Read:   readClaudeOAuth},
		},
	}}}
}

// claudeBinary is where Claude Code is installed, "" when it isn't.
func claudeBinary(l Local) string {
	for _, p := range []string{l.path(".claude", "local", "claude"), l.path(".local", "bin", "claude")} {
		if fileExists(p) {
			return p
		}
	}
	return claudeLookPath()
}

// claudeLookPath finds Claude Code outside the home; tests replace it.
var claudeLookPath = func() string {
	for _, p := range []string{"/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
		if fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return ""
}

func discoverClaudeCode(l Local) []Connection {
	bin := claudeBinary(l)
	if bin == "" {
		return nil
	}
	return []Connection{{ID: "claude-code", Provider: "claude", Mode: "plan", Origin: OriginFound, Source: "Claude Code", Values: map[string]string{"binary": bin}}}
}

// claudeRun runs Claude Code; tests replace it.
var claudeRun = func(ctx context.Context, bin string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "ant-claude-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cmd := exec.CommandContext(ctx, bin, "-p", "/usage", "--output-format", "json",
		"--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence")
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader("")
	// the subscription's own sign-in, not an API key or another address
	// the environment points Claude Code at
	var env []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		switch k {
		case "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT":
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err = cmd.Run()
	if out.Len() == 0 && err != nil {
		return nil, errors.New(short(strings.TrimSpace(stderr.String() + " " + err.Error())))
	}
	return out.Bytes(), nil
}

func readClaudeCode(ctx context.Context, l Local, c Connection) Account {
	p := claudeProvider()
	m := p.Modes[0]
	a := base(p, m, c, l.Now())
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := claudeRun(ctx, c.Values["binary"])
	if err != nil {
		a.Status, a.Message = StatusError, "Claude Code couldn't run /usage: "+short(err.Error())
		return a
	}
	text, isErr := claudeResultText(out)
	ws, err := parseClaudeUsageText(text, l.Now())
	if err != nil || isErr {
		a.Status, a.Message = StatusSignedOut, "Claude Code isn't signed in with a Claude plan. Run claude and /login with your Claude account."
		if err != nil && !errors.Is(err, errClaudeNoAllowance) {
			a.Status, a.Message = StatusError, short(err.Error())
		}
		return a
	}
	a.Status, a.Windows, a.Identity = StatusOK, ws, "claude-plan"
	return a
}

// claudeResultText is the result of `claude -p --output-format json`: one
// object, or an array of messages ending with the result.
func claudeResultText(b []byte) (string, bool) {
	type run struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	var one run
	if json.Unmarshal(b, &one) == nil && (one.Result != "" || one.Type != "") {
		return one.Result, one.IsError
	}
	var all []run
	if json.Unmarshal(b, &all) == nil {
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].Type == "result" {
				return all[i].Result, all[i].IsError
			}
		}
	}
	return string(b), false
}

var (
	claudeLineRE         = regexp.MustCompile(`^Current (session|week(?: \(([^)]+)\))?):\s*([0-9.]+)% used(?:\s*·\s*resets (.+))?$`)
	errClaudeNoAllowance = errors.New("no allowance")
)

// parseClaudeUsageText reads /usage's lines:
//
//	Current session: 12% used · resets Oct 8 at 3pm (Asia/Shanghai)
//	Current week (all models): 40% used · resets Oct 13 at 9am (Asia/Shanghai)
//	Current week (Opus): 5% used · resets …
func parseClaudeUsageText(text string, now time.Time) ([]Window, error) {
	text = regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(text, "")
	var out []Window
	for _, line := range strings.Split(text, "\n") {
		m := claudeLineRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		used, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			continue
		}
		w := Window{Name: "5 hours", UsedPercent: ptr(used)}
		span := 5 * time.Hour
		if m[1] != "session" {
			w.Name, span = "Week", 7*24*time.Hour
			if scope := strings.TrimSpace(m[2]); scope != "" && !strings.EqualFold(scope, "all models") {
				w.Name = "Week · " + scope
			}
		}
		if t, ok := claudeResetAt(m[4], now, span); ok {
			w.ResetsAt = ptr(t.UTC())
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil, errClaudeNoAllowance
	}
	return out, nil
}

// claudeResetAt reads "Oct 8 at 3pm (Asia/Shanghai)"; a date without a
// year is the next one within the window's span.
func claudeResetAt(s string, now time.Time, span time.Duration) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	loc := time.Local
	if i := strings.LastIndex(s, "("); i >= 0 && strings.HasSuffix(s, ")") {
		if z, err := time.LoadLocation(s[i+1 : len(s)-1]); err == nil {
			loc = z
		}
		s = strings.TrimSpace(s[:i])
	}
	s = strings.NewReplacer("AM", "am", "PM", "pm").Replace(s)
	for _, layout := range []string{"Jan 2 at 3:04pm", "Jan 2 at 3pm", "Jan 2, 3:04pm", "Jan 2, 3pm", "3:04pm", "3pm"} {
		t, err := time.ParseInLocation(layout, s, loc)
		if err != nil {
			continue
		}
		ref := now.In(loc)
		if t.Month() == 1 && t.Day() == 1 && !strings.Contains(layout, "Jan") { // a time today or tomorrow
			t = time.Date(ref.Year(), ref.Month(), ref.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if t.Before(ref) {
				t = t.AddDate(0, 0, 1)
			}
			return t, true
		}
		for y := ref.Year(); y <= ref.Year()+1; y++ {
			d := time.Date(y, t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if d.After(ref.Add(-time.Hour)) && d.Before(ref.Add(span+48*time.Hour)) {
				return d, true
			}
		}
	}
	return time.Time{}, false
}

// --- Claude Code's sign-in, or a token: Anthropic's usage endpoint ---

type claudeCredentials struct {
	OAuth struct {
		AccessToken      string `json:"accessToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// On macOS Claude Code keeps its sign-in in the Keychain (service "Claude
// Code-credentials"), elsewhere in ~/.claude/.credentials.json. Ant reads
// it as it is and never renews it: a renewal would sign Claude Code out.
const claudeKeychainService = "Claude Code-credentials"

// claudeKeychainHas says whether the Keychain holds Claude Code's sign-in,
// without reading it (no prompt); claudeKeychainRead reads it (macOS asks
// the user once). Tests replace both.
var (
	claudeKeychainHas = func() bool {
		if runtime.GOOS != "darwin" {
			return false
		}
		return exec.Command("security", "find-generic-password", "-s", claudeKeychainService).Run() == nil
	}
	claudeKeychainRead = func() ([]byte, error) {
		out, err := exec.Command("security", "find-generic-password", "-s", claudeKeychainService, "-w").Output()
		if err != nil {
			return nil, errors.New("the Keychain didn't allow reading Claude Code's sign-in")
		}
		return bytes.TrimSpace(out), nil
	}
)

func discoverClaudeSignIn(l Local) []Connection {
	p := l.path(".claude", ".credentials.json")
	var cr claudeCredentials
	if readJSON(p, &cr) && cr.OAuth.AccessToken != "" {
		return []Connection{{ID: "claude-signin", Provider: "claude", Mode: "plan", Origin: OriginFound, Source: l.display(p),
			Values: map[string]string{"credentials": p}}}
	}
	if claudeKeychainHas() {
		return []Connection{{ID: "claude-signin", Provider: "claude", Mode: "plan", Origin: OriginFound, Source: "Keychain · Claude Code",
			Values: map[string]string{"keychain": claudeKeychainService}}}
	}
	return nil
}

func readClaudeOAuth(ctx context.Context, l Local, c Connection) Account {
	p := claudeProvider()
	a := base(p, p.Modes[0], c, l.Now())
	token := c.Values["accessToken"]
	if path, kc := c.Values["credentials"], c.Values["keychain"]; path != "" || kc != "" {
		var cr claudeCredentials
		ok := false
		if path != "" {
			ok = readJSON(path, &cr)
		} else if b, err := claudeKeychainRead(); err == nil {
			ok = json.Unmarshal(b, &cr) == nil
		} else {
			a.Status, a.Message = StatusError, err.Error()+". Allow Ant when macOS asks."
			return a
		}
		if !ok || cr.OAuth.AccessToken == "" {
			a.Status, a.Message = StatusSignedOut, "Claude Code is signed out. Run claude and /login."
			return a
		}
		token, a.Plan = cr.OAuth.AccessToken, catalog.PlanName(cr.OAuth.SubscriptionType)
		if cr.OAuth.ExpiresAt > 0 && time.UnixMilli(cr.OAuth.ExpiresAt).Before(l.Now()) {
			a.Status, a.Message = StatusSignedOut, "Claude Code's sign-in has expired. Open Claude Code once and it renews it."
			return a
		}
	}
	b, err := l.fetch(ctx, http.MethodGet, l.url("claude.usage", "https://api.anthropic.com/api/oauth/usage"), map[string]string{
		"Authorization": "Bearer " + token, "anthropic-beta": "oauth-2025-04-20",
	}, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read Claude usage: "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "Anthropic refused the sign-in. Sign in to Claude Code again."
		}
		return a
	}
	ws, err := parseClaudeOAuthUsage(b)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status, a.Windows, a.Identity = StatusOK, ws, "claude-plan"
	withModels(&a, l, fingerprint("claude", "models", token), func() ([]Model, error) { return claudeModels(ctx, l, token) })
	return a
}

// claudeModels is what the sign-in can call: Anthropic's /v1/models with
// the OAuth token.
func claudeModels(ctx context.Context, l Local, token string) ([]Model, error) {
	b, err := l.fetch(ctx, http.MethodGet, l.url("claude.models", "https://api.anthropic.com/v1/models")+"?limit=100", map[string]string{
		"Authorization": "Bearer " + token, "anthropic-beta": "oauth-2025-04-20", "anthropic-version": "2023-06-01",
	}, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"display_name"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("not a model list")
	}
	var out []Model
	for _, m := range r.Data {
		if m.ID != "" {
			out = append(out, Model{ID: m.ID, Name: m.Name, Media: "text"})
		}
	}
	return out, nil
}

// parseClaudeOAuthUsage reads {"five_hour":{"utilization":12,"resets_at":"…"},
// "seven_day":{…},"seven_day_opus":{…}|null,"seven_day_sonnet":{…}|null}.
func parseClaudeOAuthUsage(b []byte) ([]Window, error) {
	type win struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var r map[string]json.RawMessage
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("Anthropic answered something Ant doesn't understand")
	}
	var out []Window
	for _, x := range []struct{ key, name string }{
		{"five_hour", "5 hours"}, {"seven_day", "Week"}, {"seven_day_opus", "Week · Opus"}, {"seven_day_sonnet", "Week · Sonnet"},
	} {
		var w *win
		if json.Unmarshal(r[x.key], &w) != nil || w == nil || w.Utilization == nil {
			continue
		}
		out = append(out, Window{Name: x.name, UsedPercent: ptr(max(0, min(100, *w.Utilization)))})
		if t, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil {
			out[len(out)-1].ResetsAt = ptr(t.UTC())
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Anthropic reported no plan windows for this sign-in")
	}
	return out, nil
}
