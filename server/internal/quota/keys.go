package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Finding an API key on this machine. Many providers are paid for with a
// key that coding tools already hold; this looks for one in the places
// those tools keep it. Each provider says what its keys look like and
// reads them with its own reader.

// keyHint says where a provider's key may be found.
type keyHint struct {
	Hosts    []string // API hosts the key is used with (ZCode, Claude Code's ANTHROPIC_BASE_URL)
	Path     string   // and a path the base URL must contain, when the host serves several plans
	OpenCode []string // provider ids in OpenCode's auth.json
	Env      []string // environment variables
	Prefix   string   // what the key starts with, when that tells the plan apart
}

// foundKey is one key found, with where, and the address it was used at.
type foundKey struct {
	key, source, base string
}

func (h keyHint) matchBase(base string) bool {
	host := hostOf(base)
	for _, x := range h.Hosts {
		if host == x && (h.Path == "" || strings.Contains(base, h.Path)) {
			return true
		}
	}
	return false
}

func (h keyHint) matchKey(k string) bool { return h.Prefix == "" || strings.HasPrefix(k, h.Prefix) }

// findKeys looks for keys matching h: in ZCode's providers, Claude Code's
// settings, OpenCode's auth.json and the environment.
func (l Local) findKeys(h keyHint) []foundKey {
	var out []foundKey
	seen := map[string]bool{}
	add := func(k, source, base string) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] || !h.matchKey(k) {
			return
		}
		seen[k] = true
		out = append(out, foundKey{k, source, base})
	}
	for _, z := range l.zcodeKeys() {
		if h.matchBase(z.BaseURL) {
			add(z.Key, "ZCode", z.BaseURL)
		}
	}
	if env, src := l.claudeEnv(); env != nil && h.matchBase(env["ANTHROPIC_BASE_URL"]) {
		key := env["ANTHROPIC_AUTH_TOKEN"]
		if key == "" {
			key = env["ANTHROPIC_API_KEY"]
		}
		add(key, src, env["ANTHROPIC_BASE_URL"])
	}
	if len(h.OpenCode) > 0 {
		var auth map[string]struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		}
		p := l.path(".local", "share", "opencode", "auth.json")
		if readJSON(p, &auth) {
			for _, id := range h.OpenCode {
				if a, ok := auth[id]; ok && a.Type == "api" {
					add(a.Key, "OpenCode", "")
				}
			}
		}
	}
	for _, n := range h.Env {
		if v, _ := l.env(n); v != "" {
			add(v, "$"+n, "")
		}
	}
	return out
}

// keyConnections turns found keys into connections of one mode.
func keyConnections(l Local, provider, mode string, h keyHint, values func(foundKey) map[string]string) []Connection {
	var out []Connection
	for _, k := range l.findKeys(h) {
		v := map[string]string{"apiKey": k.key}
		if values != nil {
			for a, b := range values(k) {
				v[a] = b
			}
		}
		out = append(out, Connection{ID: fingerprint(provider, mode, k.key), Provider: provider, Mode: mode,
			Origin: OriginFound, Source: k.source, Values: v})
	}
	return out
}

// getJSON reads url with the key as a bearer token into dst, filling a with
// why not when it fails. who names the provider in messages.
func getJSON(ctx context.Context, l Local, url, key, who string, a *Account, dst any) bool {
	b, err := l.fetch(ctx, http.MethodGet, url, map[string]string{"Authorization": "Bearer " + key}, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read "+who+": "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, who+" refused the key. Check it in your "+who+" API keys."
		}
		return false
	}
	if json.Unmarshal(b, dst) != nil {
		a.Status, a.Message = StatusError, who+" answered something Ant doesn't understand."
		return false
	}
	return true
}

// keyField is the API key field most key-paid modes ask for.
func keyField(placeholder, help string) []Field {
	return []Field{{ID: "apiKey", Label: "API key", Type: "secret", Placeholder: placeholder, Help: help}}
}
