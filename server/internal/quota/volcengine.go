package quota

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Volcengine Ark (火山方舟) Coding Plan.
//
// Signing in is Volcengine's own sign-in for developer tools, as its
// official CLI does it (`ve login`, volcengine/volcengine-cli
// cmd/consolelogin_*.go): an OAuth device-code flow at signin.volcengine.com
// with the public client "trn:signin:::devtools/cross-device". The user
// approves in their own browser; the token endpoint then answers with
// short-lived STS credentials (as a JSON string in access_token) and a
// refresh token that renews them.
//
// The plan is read with the official control-plane OpenAPI
// GetCodingPlanUsage (open.volcengineapi.com, service ark, version
// 2024-01-01), signed with those STS credentials — or with a read-only
// AccessKey the user already has (VOLC_ACCESSKEY / VOLC_SECRETKEY).

const (
	volcSignin   = "https://signin.volcengine.com"
	volcClientID = "trn:signin:::devtools/cross-device"
	volcScope    = "Console:All:All"
	volcAPIHost  = "open.volcengineapi.com"
	volcArkHost  = "ark.cn-beijing.volcengineapi.com"
)

var volcSignIn = &SignIn{URL: volcSignin, Browser: true, Login: volcLogin}

func volcengineProvider() Provider {
	return Provider{ID: "volcengine", Name: "Volcengine Ark", Link: "https://console.volcengine.com/ark/region:cn-beijing/subscription/coding-plan", Modes: []Mode{{
		ID: "coding-plan", Name: "Coding Plan", Kind: KindSubscription, Live: true,
		Rule: "A 5-hour rolling window, a weekly window (renewing Monday 00:00) and a monthly window (renewing on the subscription day).",
		Ways: []Way{
			{ID: "ve", Name: "Volcengine CLI", Hint: "Ant reads the keys the Volcengine CLI holds (~/.volcengine). Run ve login.",
				Fields: []Field{}, Discover: discoverVolcCLI, Read: readVolcCodingPlan},
			{ID: "arkcli", Name: "Ark CLI", Hint: "Ant reads the sign-in arkcli keeps (~/.arkcli). Run arkcli login.",
				Fields: []Field{}, Discover: discoverArkCLI, Read: readVolcCodingPlan},
			{ID: "sign-in", Name: "Sign in with Volcengine", SignIn: volcSignIn, Fields: []Field{},
				Hint: "Sign in with Volcengine in your browser, the way Volcengine's own CLI signs in. Ant never sees your password.",
				Read: readVolcCodingPlan},
			{ID: "accesskey", Name: "AccessKey", Key: "accessKeyId",
				Fields: []Field{
					{ID: "accessKeyId", Label: "AccessKey ID", Type: "text", Help: "An IAM user with ArkReadOnlyAccess is enough."},
					{ID: "secretAccessKey", Label: "Secret AccessKey", Type: "secret"},
				},
				Discover: discoverVolcEnv, Read: readVolcCodingPlan},
		},
	}}}
}

func discoverVolcEnv(l Local) []Connection {
	id, n := l.env("VOLC_ACCESSKEY", "VOLCENGINE_ACCESS_KEY_ID", "VOLCENGINE_ACCESS_KEY")
	secret, _ := l.env("VOLC_SECRETKEY", "VOLCENGINE_SECRET_ACCESS_KEY", "VOLCENGINE_SECRET_KEY")
	if id == "" || secret == "" {
		return nil
	}
	return []Connection{{ID: fingerprint("volcengine", "coding-plan", id), Provider: "volcengine", Mode: "coding-plan", Way: "accesskey",
		Origin: OriginFound, Source: "$" + n, Values: map[string]string{"accessKeyId": id, "secretAccessKey": secret}}}
}

// volcCLIConfig is the Volcengine CLI's ~/.volcengine/config.json: its
// current profile's keys, static or short-lived from ve login.
func volcCLIConfig(path string) (map[string]string, bool) {
	var cfg struct {
		Current  string `json:"current"`
		Profiles map[string]struct {
			AccessKey    string `json:"access-key"`
			SecretKey    string `json:"secret-key"`
			SessionToken string `json:"session-token"`
			Expiration   any    `json:"sts-expiration"`
		} `json:"profiles"`
	}
	if !readJSON(path, &cfg) {
		return nil, false
	}
	pr, ok := cfg.Profiles[cfg.Current]
	if !ok || pr.AccessKey == "" || pr.SecretKey == "" {
		return nil, false
	}
	v := map[string]string{"accessKeyId": pr.AccessKey, "secretAccessKey": pr.SecretKey, "sessionToken": pr.SessionToken, "profile": cfg.Current}
	if exp := volcExpiry(pr.Expiration); !exp.IsZero() {
		v["stsExpires"] = strconv.FormatInt(exp.Unix(), 10)
	}
	return v, true
}

// volcExpiry reads an expiry given as seconds, milliseconds or RFC 3339.
func volcExpiry(v any) time.Time {
	switch x := v.(type) {
	case float64:
		if x > 1e12 {
			return time.UnixMilli(int64(x))
		}
		if x > 0 {
			return time.Unix(int64(x), 0)
		}
	case string:
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return volcExpiry(float64(n))
		}
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t
		}
	}
	return time.Time{}
}

func discoverVolcCLI(l Local) []Connection {
	p := l.path(".volcengine", "config.json")
	if _, ok := volcCLIConfig(p); !ok {
		return nil
	}
	return []Connection{{ID: "volcengine-ve", Provider: "volcengine", Mode: "coding-plan", Way: "ve", Origin: OriginFound,
		Source: "Volcengine CLI", Values: map[string]string{"veConfig": p}}}
}

// arkCLIEnv is arkcli's ~/.arkcli/.env (KEY=value lines).
func arkCLIEnv(path string) (map[string]string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if env[k] != "" {
				return env[k]
			}
		}
		return ""
	}
	v := map[string]string{
		"accessKeyId":     pick("VOLCENGINE_ACCESS_KEY_ID", "VOLCENGINE_ACCESS_KEY", "VOLC_ACCESSKEY"),
		"secretAccessKey": pick("VOLCENGINE_SECRET_ACCESS_KEY", "VOLCENGINE_SECRET_KEY", "VOLC_SECRETKEY"),
		"sessionToken":    pick("VOLCENGINE_SESSION_TOKEN", "VOLCENGINE_SECURITY_TOKEN"),
		"signedIn":        pick("VOLCENGINE_REFRESH_TOKEN"),
	}
	if exp := volcExpiry(pick("VOLCENGINE_STS_EXPIRATION", "VOLCENGINE_SESSION_EXPIRATION", "VOLCENGINE_EXPIRATION")); !exp.IsZero() {
		v["stsExpires"] = strconv.FormatInt(exp.Unix(), 10)
	}
	if v["accessKeyId"] == "" && v["signedIn"] == "" {
		return nil, false
	}
	return v, true
}

func discoverArkCLI(l Local) []Connection {
	p := l.path(".arkcli", ".env")
	if _, ok := arkCLIEnv(p); !ok {
		return nil
	}
	return []Connection{{ID: "volcengine-arkcli", Provider: "volcengine", Mode: "coding-plan", Way: "arkcli", Origin: OriginFound,
		Source: "Ark CLI", Values: map[string]string{"arkEnv": p}}}
}

// volcSign signs a Volcengine OpenAPI request (HMAC-SHA256, as Volcengine's
// signing guide describes) and returns the headers to send.
func volcSign(method, id, secret, session, host, region, service string, query url.Values, body string, now time.Time) map[string]string {
	xDate := now.UTC().Format("20060102T150405Z")
	date := xDate[:8]
	bodySha := sha256Hex(body)
	headers := map[string]string{"host": host, "x-content-sha256": bodySha, "x-date": xDate}
	if session != "" {
		headers["x-security-token"] = session
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, volcEscape(k)+"="+volcEscape(query.Get(k)))
	}
	canonical := strings.Join([]string{method, "/", strings.Join(pairs, "&"), canonHeaders.String(), signed, bodySha}, "\n")
	scope := date + "/" + region + "/" + service + "/request"
	toSign := strings.Join([]string{"HMAC-SHA256", xDate, scope, sha256Hex(canonical)}, "\n")
	k := hmacSHA256([]byte(secret), date)
	for _, part := range []string{region, service, "request"} {
		k = hmacSHA256(k, part)
	}
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	out := map[string]string{
		"X-Date": xDate, "X-Content-Sha256": bodySha,
		"Authorization": "HMAC-SHA256 Credential=" + id + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + sig,
	}
	if session != "" {
		out["X-Security-Token"] = session
	}
	return out
}

func sha256Hex(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

func volcEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func readVolcCodingPlan(ctx context.Context, l Local, c Connection) Account {
	p := volcengineProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	id, secret, session, ok := volcCredentials(ctx, l, c, &a)
	if !ok {
		return a
	}
	if c.Values["refreshToken"] != "" && c.Values["accessKeyId"] == "" && a.Source == "" {
		a.Source = "Signed in with Volcengine"
	}
	b, err := volcCall(ctx, l, id, secret, session, "GetCodingPlanUsage")
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read the Coding Plan: "+short(volcError(err))
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "Volcengine refused the credentials: "+short(volcError(err))
		}
		return a
	}
	ws, err := parseVolcCodingPlanUsage(b)
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status, a.Windows = StatusOK, ws
	if len(ws) == 0 {
		a.Message = "This Volcengine account has no Coding Plan."
	}
	return a
}

// volcCall calls an Ark control-plane action: POST with a JSON body at
// Ark's regional endpoint (as Ark's console and arkcli call it), then GET
// at the general OpenAPI gateway. Each try has its own time, so one that
// hangs doesn't take the other's; a refusal is final.
func volcCall(ctx context.Context, l Local, id, secret, session, action string) ([]byte, error) {
	q := url.Values{"Action": {action}, "Version": {"2024-01-01"}}
	tries := []struct{ method, host, key, body string }{
		{http.MethodPost, volcArkHost, "volc.ark", "{}"},
		{http.MethodGet, volcAPIHost, "volc.api", ""},
	}
	var last error
	for _, t := range tries {
		h := volcSign(t.method, id, secret, session, t.host, "cn-beijing", "ark", q, t.body, l.Now())
		h["Accept"] = "application/json"
		var body io.Reader
		if t.body != "" {
			h["Content-Type"] = "application/json"
			body = strings.NewReader(t.body)
		}
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		b, err := l.fetch(tctx, t.method, l.url(t.key, "https://"+t.host)+"/?"+q.Encode(), h, body)
		cancel()
		if err == nil || isAuthError(err) {
			return b, err
		}
		last = err
	}
	return nil, last
}

// volcError is Volcengine's ResponseMetadata.Error in one line.
func volcError(err error) string {
	var se *statusError
	if errors.As(err, &se) {
		var r struct {
			ResponseMetadata struct {
				Error struct{ Code, Message string }
			}
		}
		if json.Unmarshal([]byte(se.Body), &r) == nil && r.ResponseMetadata.Error.Code != "" {
			return r.ResponseMetadata.Error.Code + ": " + r.ResponseMetadata.Error.Message
		}
	}
	return err.Error()
}

// parseVolcCodingPlanUsage reads GetCodingPlanUsage:
//
//	{"ResponseMetadata":{…},"Result":{"QuotaUsage":[{"Level":"session","Percent":12.5,"ResetTimestamp":1791400000},
//	 {"Level":"weekly",…},{"Level":"monthly",…}]}}
//
// Percent is the share used.
func parseVolcCodingPlanUsage(b []byte) ([]Window, error) {
	var r struct {
		ResponseMetadata struct {
			Error *struct{ Code, Message string }
		}
		Result struct {
			QuotaUsage []struct {
				Level          string     `json:"Level"`
				Percent        flexNumber `json:"Percent"`
				ResetTimestamp flexNumber `json:"ResetTimestamp"`
			} `json:"QuotaUsage"`
		}
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("Volcengine answered something Ant doesn't understand")
	}
	if e := r.ResponseMetadata.Error; e != nil && e.Code != "" {
		return nil, fmt.Errorf("Volcengine: %s %s", e.Code, e.Message)
	}
	order := map[string]int{"session": 0, "weekly": 1, "monthly": 2}
	names := map[string]string{"session": "5 hours", "weekly": "Week", "monthly": "Month"}
	type ranked struct {
		rank int
		w    Window
	}
	var rs []ranked
	for _, q := range r.Result.QuotaUsage {
		lvl := strings.ToLower(q.Level)
		name, rank := names[lvl], order[lvl]
		if name == "" {
			name, rank = q.Level, 9
		}
		w := Window{Name: name, UsedPercent: ptr(max(0, min(100, float64(q.Percent))))}
		if ts := int64(q.ResetTimestamp); ts > 0 {
			if ts > 1e12 {
				w.ResetsAt = ptr(time.UnixMilli(ts).UTC())
			} else {
				w.ResetsAt = ptr(time.Unix(ts, 0).UTC())
			}
		}
		rs = append(rs, ranked{rank, w})
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].rank < rs[j].rank })
	out := make([]Window, len(rs))
	for i, x := range rs {
		out[i] = x.w
	}
	return out, nil
}
