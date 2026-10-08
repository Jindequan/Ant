package quota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local is where Readers look for sign-ins: the user's home, environment and
// an HTTP client. Tests point it at a temporary home and fake servers.
type Local struct {
	Home   string
	Getenv func(string) string
	HTTP   *http.Client
	Now    func() time.Time
	// URLs replaces provider addresses by name ("zai.global", "suno.auth"…);
	// only tests set it.
	URLs map[string]string
}

func (l Local) url(name, def string) string {
	if v, ok := l.URLs[name]; ok {
		return v
	}
	return def
}

// DefaultLocal is the real machine.
func DefaultLocal() Local {
	home, _ := os.UserHomeDir()
	return Local{Home: home, Getenv: os.Getenv, HTTP: readerClient(), Now: time.Now}
}

// readerClient keeps a reader's credentials on the origin it addressed.
// Go drops Authorization and Cookie on a redirect to another domain but
// forwards every other header, and readers send keys in custom ones
// (xi-api-key, x-manus-api-key…): so a redirect to another host, or from
// HTTPS to HTTP, is not followed, and plain HTTP goes only to this machine.
func readerClient() *http.Client {
	return &http.Client{
		Timeout:       15 * time.Second,
		Transport:     httpsOnly{http.DefaultTransport.(*http.Transport).Clone()},
		CheckRedirect: sameOriginRedirect,
	}
}

func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	first := via[0].URL
	if !strings.EqualFold(req.URL.Host, first.Host) || (first.Scheme == "https" && req.URL.Scheme != "https") {
		return http.ErrUseLastResponse // answered as a non-2xx reply
	}
	return nil
}

type httpsOnly struct{ next http.RoundTripper }

func (t httpsOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" && !isLocalHost(req.URL.Hostname()) {
		return nil, errors.New("Ant reads providers only over HTTPS")
	}
	return t.next.RoundTrip(req)
}

func (l Local) env(names ...string) (value, name string) {
	for _, n := range names {
		if v := strings.TrimSpace(l.Getenv(n)); v != "" {
			return v, n
		}
	}
	return "", ""
}

func (l Local) path(parts ...string) string {
	return filepath.Join(append([]string{l.Home}, parts...)...)
}

// display shortens a path under home to ~/… for the page.
func (l Local) display(p string) string {
	if l.Home != "" && strings.HasPrefix(p, l.Home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(p, l.Home)
	}
	return p
}

// accountsFile is where accounts added on the page are kept.
func (l Local) AccountsFile() string { return l.path(".config", "ant", "accounts.json") }

// zcodeKey is one API key ZCode keeps for a provider, with the address it
// calls it at.
type zcodeKey struct {
	Name, BaseURL, Key string
}

// zcodeKeys reads the providers ZCode is set up with
// (~/.zcode/v2/config.json, "provider" → {name, options{apiKey, baseURL}}).
// Its encrypted credentials.json isn't read.
func (l Local) zcodeKeys() []zcodeKey {
	var cfg struct {
		Provider map[string]struct {
			Name    string `json:"name"`
			Options struct {
				APIKey  string `json:"apiKey"`
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
	}
	if !readJSON(l.path(".zcode", "v2", "config.json"), &cfg) {
		return nil
	}
	var out []zcodeKey
	for _, p := range cfg.Provider {
		if k := strings.TrimSpace(p.Options.APIKey); k != "" && p.Options.BaseURL != "" {
			out = append(out, zcodeKey{Name: p.Name, BaseURL: p.Options.BaseURL, Key: k})
		}
	}
	return out
}

// claudeEnv is the env block of Claude Code's settings, where a coding
// plan's key and address are often set.
func (l Local) claudeEnv() (map[string]string, string) {
	var s struct {
		Env map[string]string `json:"env"`
	}
	p := l.path(".claude", "settings.json")
	if !readJSON(p, &s) {
		return nil, ""
	}
	return s.Env, l.display(p)
}

func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func readJSON(path string, dst any) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 4<<20 {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

// statusError is a non-2xx reply.
type statusError struct {
	Code int
	Body string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%d %s", e.Code, http.StatusText(e.Code))
}

func isAuthError(err error) bool {
	var se *statusError
	return errors.As(err, &se) && (se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden)
}

// fetch makes one bounded request and returns the body of a 2xx reply.
func (l Local) fetch(ctx context.Context, method, url string, headers map[string]string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := l.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, &statusError{Code: res.StatusCode, Body: string(b)}
	}
	return b, nil
}

// jwtClaims decodes a JWT's claims without verifying it: they only label
// what the provider's own app already trusts.
func jwtClaims(tok string) map[string]any {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func claimString(c map[string]any, keys ...string) string {
	var cur any = c
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

// short makes a reply or error one line fit for the page.
func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "…"
	}
	return s
}
