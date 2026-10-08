package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// GitHub Copilot's monthly allowances (premium requests, chat,
// completions), read from GitHub's copilot_internal/user with a GitHub
// sign-in, found four ways, easiest first (as yetone/magpie finds them):
//
//  1. The Copilot CLI's sign-in: ~/.copilot/config.json names the user; the
//     token is in that file or in the Keychain (service "copilot-cli").
//  2. The editors' sign-in: ~/.config/github-copilot/apps.json.
//  3. The GitHub CLI's token: ~/.config/gh/hosts.yml.
//  4. A GitHub token the user gives.

func copilotProvider() Provider {
	return Provider{ID: "copilot", Name: "GitHub Copilot", Link: "https://github.com/settings/copilot", Modes: []Mode{{
		ID: "plan", Name: "Copilot plan", Kind: KindSubscription, Live: true,
		Rule: "Premium requests per month, renewing on the 1st; chat and completions are unlimited on paid plans and monthly on Free.",
		Ways: []Way{
			{ID: "cli", Name: "Copilot CLI", Hint: "Ant reads the Copilot CLI's sign-in (~/.copilot). Run copilot and /login.", Fields: []Field{}, Discover: discoverCopilotCLI, Read: readCopilot},
			{ID: "editor", Name: "Editor sign-in", Hint: "Ant reads the sign-in VS Code, JetBrains or Neovim's Copilot keeps.", Fields: []Field{}, Discover: discoverCopilotEditor, Read: readCopilot},
			{ID: "gh", Name: "GitHub CLI", Hint: "Ant reads the GitHub CLI's token (gh auth login).", Fields: []Field{}, Discover: discoverGitHubCLI, Read: readCopilot},
			{ID: "token", Name: "GitHub token", Key: "token",
				Fields: []Field{{ID: "token", Label: "GitHub token", Type: "secret", Placeholder: "gho_… or ghu_…", Help: "A token of the GitHub account that has Copilot, such as what gh auth token prints."}},
				Read:   readCopilot},
		},
	}}}
}

// stripJSONC drops // comments, which the Copilot CLI's config allows.
func stripJSONC(b []byte) []byte {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

func discoverCopilotCLI(l Local) []Connection {
	home := l.path(".copilot")
	if v, _ := l.env("COPILOT_HOME"); v != "" {
		home = v
	}
	b, err := os.ReadFile(home + "/config.json")
	if err != nil {
		return nil
	}
	var cfg struct {
		Last  *struct{ Host, Login string }  `json:"lastLoggedInUser"`
		Users []struct{ Host, Login string } `json:"loggedInUsers"`
		Tok   map[string]string              `json:"copilotTokens"`
	}
	if json.Unmarshal(stripJSONC(b), &cfg) != nil {
		return nil
	}
	login := ""
	if cfg.Last != nil {
		login = cfg.Last.Login
	} else if len(cfg.Users) > 0 {
		login = cfg.Users[0].Login
	}
	if login == "" {
		return nil
	}
	v := map[string]string{"login": login, "keychain": "https://github.com:" + login, "headers": "cli"}
	if t := cfg.Tok["https://github.com:"+login]; t != "" {
		v["token"] = t
	}
	return []Connection{{ID: "copilot-" + strings.ToLower(login), Provider: "copilot", Mode: "plan", Origin: OriginFound, Source: "Copilot CLI", Values: v}}
}

func discoverCopilotEditor(l Local) []Connection {
	var out []Connection
	for _, f := range []string{"apps.json", "hosts.json"} {
		var apps map[string]struct {
			User  string `json:"user"`
			Token string `json:"oauth_token"`
		}
		p := l.path(".config", "github-copilot", f)
		if !readJSON(p, &apps) {
			continue
		}
		for k, a := range apps {
			if a.Token != "" && strings.HasPrefix(k, "github.com") {
				out = append(out, Connection{ID: "copilot-" + strings.ToLower(a.User), Provider: "copilot", Mode: "plan", Origin: OriginFound,
					Source: l.display(p), Values: map[string]string{"login": a.User, "token": a.Token}})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

var ghTokenRE = regexp.MustCompile(`^\s+oauth_token:\s*(\S+)`)

func discoverGitHubCLI(l Local) []Connection {
	p := l.path(".config", "gh", "hosts.yml")
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	inGitHub, user, token := false, "", ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") {
			inGitHub = strings.HasPrefix(line, "github.com:")
			continue
		}
		if !inGitHub {
			continue
		}
		if m := ghTokenRE.FindStringSubmatch(line); m != nil && token == "" {
			token = m[1]
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "user:") && user == "" {
			user = strings.TrimSpace(strings.TrimPrefix(t, "user:"))
		}
	}
	v := map[string]string{"login": user, "token": token}
	if token == "" { // gh keeps it in the Keychain: gh itself hands it over at each read
		bin := ghLookPath()
		if bin == "" || user == "" {
			return nil
		}
		v = map[string]string{"login": user, "gh": bin}
	}
	return []Connection{{ID: "copilot-" + strings.ToLower(user), Provider: "copilot", Mode: "plan", Origin: OriginFound,
		Source: "GitHub CLI", Values: v}}
}

// ghLookPath finds the GitHub CLI; tests replace it.
var ghLookPath = func() string {
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
		if fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	return ""
}

// ghToken asks the GitHub CLI for its token (gh auth token); tests replace it.
var ghToken = func(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "auth", "token", "--hostname", "github.com").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// copilotKeychain reads the Copilot CLI's token from the Keychain; tests replace it.
var copilotKeychain = func(account string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("security", "find-generic-password", "-s", "copilot-cli", "-a", account, "-w").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readCopilot(ctx context.Context, l Local, c Connection) Account {
	p := copilotProvider()
	a := base(p, p.Modes[0], c, l.Now())
	a.User = c.Values["login"]
	token := c.Values["token"]
	if token == "" && c.Values["keychain"] != "" {
		token = copilotKeychain(c.Values["keychain"])
	}
	if token == "" && c.Values["gh"] != "" {
		token = ghToken(ctx, c.Values["gh"])
	}
	if token == "" {
		a.Status, a.Message = StatusSignedOut, "The Copilot sign-in has no token Ant can use. Sign in to Copilot again."
		return a
	}
	h := map[string]string{"Authorization": "token " + token,
		"Editor-Version": "vscode/1.140.0", "Editor-Plugin-Version": "copilot-chat/0.68.0", "Copilot-Integration-Id": "vscode-chat", "User-Agent": "GitHubCopilotChat/0.68.0"}
	if c.Values["headers"] == "cli" {
		h = map[string]string{"Authorization": "token " + token,
			"Editor-Version": "copilot/1.0.88", "Copilot-Integration-Id": "copilot-developer-cli", "X-GitHub-Api-Version": "2026-07-01", "User-Agent": "copilot/1.0.88"}
	}
	b, err := l.fetch(ctx, http.MethodGet, l.url("copilot", "https://api.github.com/copilot_internal/user"), h, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read Copilot: "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "GitHub refused the sign-in, or this account has no Copilot."
		}
		return a
	}
	if err := parseCopilotUser(b, &a); err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status = StatusOK
	if a.User != "" {
		a.Identity = "copilot:" + strings.ToLower(a.User)
	}
	withModels(&a, l, fingerprint("copilot", "models", token), func() ([]Model, error) { return copilotModels(ctx, l, token, h) })
	return a
}

// copilotModels is Copilot's model picker for this account: the GitHub
// token is exchanged for a Copilot token (copilot_internal/v2/token), which
// reads {endpoints.api}/models as the editors do.
func copilotModels(ctx context.Context, l Local, token string, h map[string]string) ([]Model, error) {
	b, err := l.fetch(ctx, http.MethodGet, l.url("copilot.token", "https://api.github.com/copilot_internal/v2/token"), h, nil)
	if err != nil {
		return nil, err
	}
	var t struct {
		Token     string `json:"token"`
		Endpoints struct {
			API string `json:"api"`
		} `json:"endpoints"`
	}
	if json.Unmarshal(b, &t) != nil || t.Token == "" {
		return nil, errors.New("no Copilot token")
	}
	api := firstNonEmpty(l.URLs["copilot.api"], t.Endpoints.API, "https://api.githubcopilot.com")
	mh := map[string]string{"Authorization": "Bearer " + t.Token}
	for k, v := range h {
		if k != "Authorization" {
			mh[k] = v
		}
	}
	b, err = l.fetch(ctx, http.MethodGet, strings.TrimRight(api, "/")+"/models", mh, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Picker bool   `json:"model_picker_enabled"`
			Caps   struct {
				Type string `json:"type"`
			} `json:"capabilities"`
			Policy *struct {
				State string `json:"state"`
			} `json:"policy"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("not a model list")
	}
	var out []Model
	for _, m := range r.Data {
		if m.ID == "" || !m.Picker || (m.Policy != nil && m.Policy.State == "disabled") {
			continue
		}
		media := "text"
		if m.Caps.Type == "embeddings" {
			media = "embedding"
		}
		out = append(out, Model{ID: m.ID, Name: m.Name, Media: media})
	}
	return out, nil
}

// parseCopilotUser reads copilot_internal/user:
//
//	{"copilot_plan":"individual","access_type_sku":"…","quota_reset_date_utc":"2026-11-01T00:00:00Z",
//	 "quota_snapshots":{"premium_interactions":{"entitlement":300,"quota_remaining":212.5,
//	   "percent_remaining":70.8,"unlimited":false},"chat":{"unlimited":true},"completions":{…}}}
func parseCopilotUser(b []byte, a *Account) error {
	type snap struct {
		Entitlement flexNumber `json:"entitlement"`
		Remaining   flexNumber `json:"quota_remaining"`
		Percent     *float64   `json:"percent_remaining"`
		Unlimited   bool       `json:"unlimited"`
	}
	var r struct {
		Plan     string          `json:"copilot_plan"`
		SKU      string          `json:"access_type_sku"`
		Login    string          `json:"login"`
		Reset    string          `json:"quota_reset_date_utc"`
		ResetDay string          `json:"quota_reset_date"`
		Snaps    map[string]snap `json:"quota_snapshots"`
	}
	if json.Unmarshal(b, &r) != nil {
		return errors.New("GitHub answered something Ant doesn't understand")
	}
	if r.Login != "" {
		a.User = r.Login
	}
	a.Plan = map[string]string{"individual": "Pro", "individual_pro": "Pro+", "business": "Business", "enterprise": "Enterprise", "free": "Free"}[r.Plan]
	if a.Plan == "" {
		a.Plan = catalog.PlanName(r.Plan)
	}
	if strings.EqualFold(r.Plan, "free") || strings.Contains(r.SKU, "free") {
		a.Kind = KindFree
	}
	var resets *time.Time
	if t, err := time.Parse(time.RFC3339, r.Reset); err == nil {
		resets = ptr(t.UTC())
	} else if t, err := time.Parse("2006-01-02", r.ResetDay); err == nil {
		resets = ptr(t.UTC())
	}
	for _, x := range []struct{ id, name string }{{"premium_interactions", "Premium requests"}, {"chat", "Chat"}, {"completions", "Completions"}} {
		s, ok := r.Snaps[x.id]
		if !ok || s.Unlimited {
			continue
		}
		w := Window{Name: x.name, ResetsAt: resets, Unit: "requests"}
		if s.Entitlement > 0 {
			w.Limit, w.Left = ptr(float64(s.Entitlement)), ptr(max(0, float64(s.Remaining)))
		} else if s.Percent != nil {
			w.UsedPercent = ptr(100 - *s.Percent)
		}
		a.Windows = append(a.Windows, w)
	}
	if len(a.Windows) == 0 {
		a.Message = "Unlimited chat and completions; no metered allowance on this plan."
	}
	return nil
}
