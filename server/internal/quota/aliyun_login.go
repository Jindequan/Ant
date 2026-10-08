package quota

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Alibaba Cloud's own sign-in for programs, as the official Bailian CLI
// (`bl auth login --console`, bailian-cli 2.1.0) does it: Ant listens on a
// one-off port on 127.0.0.1, opens Bailian's console-login page in the
// user's own default browser (any browser; Google sign-in works there),
// and the page hands the console access token back to that port once the
// user has signed in. A random state ties the answer to this request.
// The token then reads Bailian's CLI gateway, which is built for programs.

const bailianLoginPage = "https://bailian.console.aliyun.com/console-login"

// openURL opens a page in the user's default browser; tests replace it.
var openURL = func(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	}
	return exec.Command("xdg-open", u).Start()
}

// aliyunLogin runs the console-login handshake and returns the values that
// connect every Bailian instance: the console token and, when the page
// sends them, the site and region it is for.
func aliyunLogin(ctx context.Context, l Local) (map[string]string, error) {
	w, release, err := beginSignIn()
	if err != nil {
		return nil, err
	}
	defer release()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("couldn't open a local port for the sign-in")
	}
	state := make([]byte, 16)
	_, _ = rand.Read(state)
	st := hex.EncodeToString(state)
	got := make(chan map[string]string, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: consoleLoginHandler(st, got)}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	page := l.url("aliyun.login", bailianLoginPage) + "?notice=127.0.0.1:" + itoa(port) + "?state=" + url.QueryEscape(st)
	if err := openURL(page); err != nil {
		return nil, errors.New("couldn't open your browser for the sign-in")
	}
	ctx, stop := context.WithTimeout(ctx, 15*time.Minute)
	defer stop()
	select {
	case v := <-got:
		return v, nil
	case <-w.cancel:
		return nil, errSignInCancelled
	case <-ctx.Done():
		return nil, errors.New("signing in took too long")
	}
}

// consoleLoginHandler takes the console's hand-back: the token in the query,
// or in a JSON or form body, on a request carrying the right state.
func consoleLoginHandler(state string, got chan<- map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// the console page calls this port from its own origin
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Allow-Private-Network", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "bad state", http.StatusBadRequest)
			return
		}
		v := map[string]string{}
		take := func(get func(string) string) {
			for key, names := range map[string][]string{
				"token":  {"access_token", "accessToken"},
				"site":   {"console_site", "consoleSite"},
				"region": {"console_region", "consoleRegion"},
			} {
				for _, n := range names {
					if s := strings.TrimSpace(get(n)); s != "" && v[key] == "" {
						v[key] = s
					}
				}
			}
		}
		take(r.URL.Query().Get)
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			var m map[string]any
			if json.Unmarshal(body, &m) == nil {
				take(func(k string) string { return jsonField(m, k) })
				if d, ok := m["data"].(map[string]any); ok {
					take(func(k string) string { return jsonField(d, k) })
				}
			} else if form, err := url.ParseQuery(string(body)); err == nil && (ct == "application/x-www-form-urlencoded" || ct == "") {
				take(form.Get)
			}
		}
		if v["token"] == "" {
			http.Error(w, "no token", http.StatusBadRequest)
			return
		}
		h.Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("OK — Ant is signed in. You can close this tab.\n"))
		select {
		case got <- v:
		default:
		}
	})
}

func jsonField(m map[string]any, k string) string {
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
