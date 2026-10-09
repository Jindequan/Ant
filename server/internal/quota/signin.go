package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Signing in to a website that has no API key: Ant opens the site's own
// sign-in page in a plain Chrome window with a profile of Ant's own, one
// per sign-in (~/.config/ant/browsers/<key>), so two accounts of one site
// keep two sessions. Nothing controls that window — to the site, and
// to Google's sign-in, it is ordinary Chrome — and Ant never sees what is
// typed. Ant only reads the cookies the profile saves to disk, and closes
// the window once the site's session is there.
//
// The profile's cookies stay encrypted as Chrome always encrypts them, with
// the browser's key in the macOS Keychain: the first time, macOS asks to
// let Ant read that key ("Always Allow" makes it once).

// SignIn is how a mode's website is signed in to.
type SignIn struct {
	URL string `json:"url"` // where the window opens
	// Domain is the site whose cookies make the session; Done says, from
	// those cookies, that the user is signed in.
	Domain string                          `json:"-"`
	Done   func(cookies []siteCookie) bool `json:"-"`
	// Session names the cookies that hold the session, first choice first:
	// when they expire, the sign-in ends.
	Session []string `json:"-"`
	// Login, when set, is the provider's own sign-in instead of a window:
	// it returns the values that connect the account (Alibaba Cloud's
	// console-login hands back a token).
	Login func(ctx context.Context, l Local) (map[string]string, error) `json:"-"`
	// Browser: the sign-in happens in the user's own default browser.
	Browser bool `json:"browser,omitempty"`
	// Confirm: no cookie tells that the user is signed in, so the window
	// stays until they say so (I'm signed in); Done then only checks that
	// the site set cookies.
	Confirm bool `json:"confirm,omitempty"`
}

var (
	errSignInClosed    = errors.New("Chrome was closed before you signed in")
	errSignInCancelled = errors.New("signing in was cancelled")
)

// chromeApps are Chromium browsers Ant can open a window of its own in, with
// the Keychain item each keeps its cookie key in.
var chromeApps = []struct{ path, keychain string }{
	{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "Chrome Safe Storage"},
	{"/Applications/Chromium.app/Contents/MacOS/Chromium", "Chromium Safe Storage"},
	{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "Microsoft Edge Safe Storage"},
	{"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser", "Brave Safe Storage"},
}

// findChrome is the browser Ant's window runs in, and its Keychain item.
func findChrome(l Local) (path, keychain string, err error) {
	for _, a := range chromeApps {
		if runtime.GOOS == "darwin" {
			if mine := l.path(strings.TrimPrefix(a.path, "/")); fileExists(mine) { // ~/Applications
				return mine, a.keychain, nil
			}
		}
		if fileExists(a.path) {
			return a.path, a.keychain, nil
		}
	}
	return "", "", errors.New("signing in needs Google Chrome (or Chromium, Edge or Brave). Install one and try again")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// antProfile is the Chrome profile of one sign-in. key "" is the profile
// every sign-in shared before accounts could be many; it stays theirs.
func antProfile(l Local, key string) string {
	if key == "" {
		return l.path(".config", "ant", "browser")
	}
	return l.path(".config", "ant", "browsers", key)
}

// profileCookies reads the cookies a sign-in's profile holds for domain,
// and when they expire.
func profileCookies(ctx context.Context, l Local, domain, key string) ([]siteCookie, map[string]time.Time, error) {
	_, keychain, err := findChrome(l)
	if err != nil {
		return nil, nil, errNoCookie
	}
	for _, f := range []string{"Default/Cookies", "Default/Network/Cookies"} {
		p := filepath.Join(antProfile(l, key), filepath.FromSlash(f))
		if fileExists(p) {
			return readSiteCookies(ctx, p, domain, func() ([]byte, error) { return chromiumKey(keychain) })
		}
	}
	return nil, nil, errNoCookie
}

// signedInSession is the session of a signed-in connection: the cookies
// its profile holds now (Chrome keeps them fresh), else those kept when
// signing in.
func signedInSession(ctx context.Context, l Local, s SignIn, stored, key string) (browserSession, bool) {
	if cs, ends, err := profileCookies(ctx, l, s.Domain, key); err == nil && s.Done(cs) {
		return browserSession{cookies: cs, source: "Signed in with Ant", ends: ends}, true
	}
	return decodeSession(stored)
}

// signInWindow is the sign-in in progress, if any.
type signInWindow struct {
	finish chan struct{} // the user says they're signed in
	cancel chan struct{}
	once   sync.Once

	mu   sync.Mutex
	code string // a device code the user confirms in the browser
	page string // the page they confirm it on
}

// SignInStatus is what the page shows while a sign-in is in progress.
type SignInStatus struct {
	Open bool   `json:"open"`
	Code string `json:"code,omitempty"`
	Page string `json:"page,omitempty"`
}

// CurrentSignIn is the sign-in in progress, if any.
func CurrentSignIn() SignInStatus {
	signInMu.Lock()
	w := signInNow
	signInMu.Unlock()
	if w == nil {
		return SignInStatus{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return SignInStatus{Open: true, Code: w.code, Page: w.page}
}

func (w *signInWindow) show(code, page string) {
	w.mu.Lock()
	w.code, w.page = code, page
	w.mu.Unlock()
}

var (
	signInMu  sync.Mutex
	signInNow *signInWindow
)

// FinishSignIn and CancelSignIn answer the window in progress.
func FinishSignIn() bool { return signInSignal(func(w *signInWindow) { close(w.finish) }) }
func CancelSignIn() bool { return signInSignal(func(w *signInWindow) { close(w.cancel) }) }

func signInSignal(f func(*signInWindow)) bool {
	signInMu.Lock()
	w := signInNow
	signInMu.Unlock()
	if w == nil {
		return false
	}
	w.once.Do(func() { f(w) })
	return true
}

// signInRunner signs in through a browser window; tests replace it.
var signInRunner = runSignIn

// beginSignIn registers the one sign-in that may run at a time.
func beginSignIn() (*signInWindow, func(), error) {
	w := &signInWindow{finish: make(chan struct{}), cancel: make(chan struct{})}
	signInMu.Lock()
	defer signInMu.Unlock()
	if signInNow != nil {
		return nil, nil, errors.New("a sign-in is already in progress")
	}
	signInNow = w
	return w, func() {
		signInMu.Lock()
		signInNow = nil
		signInMu.Unlock()
	}, nil
}

// runSignIn opens the window on profile key and waits until the session is
// in the profile, the user says they're done or cancels, Chrome quits, or
// ctx ends.
func runSignIn(ctx context.Context, l Local, s SignIn, key string) ([]siteCookie, error) {
	w, release, err := beginSignIn()
	if err != nil {
		return nil, err
	}
	defer release()

	chrome, _, err := findChrome(l)
	if err != nil {
		return nil, err
	}
	profile := antProfile(l, key)
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	keepSessionCookies(profile)
	cmd := exec.Command(chrome, "--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check", "--disable-sync", "--new-window", "--app="+s.URL)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("couldn't open the sign-in window: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	// quit asks Chrome to quit as it does on Cmd-Q, which also writes its
	// cookies to disk, and waits for it.
	quit := func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	}
	signedIn := func() ([]siteCookie, bool) {
		cs, _, err := profileCookies(ctx, l, s.Domain, key)
		return cs, err == nil && s.Done(cs)
	}

	ctx, stop := context.WithTimeout(ctx, 20*time.Minute)
	defer stop()
	// Chrome writes cookies to disk within about 30 seconds; Ant looks
	// every 2.
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if s.Confirm {
				continue
			}
			if cs, ok := signedIn(); ok {
				quit()
				if again, ok := signedIn(); ok {
					cs = again
				}
				return cs, nil
			}
			continue
		case <-w.finish:
			quit()
		case <-w.cancel:
			quit()
			return nil, errSignInCancelled
		case <-exited:
		case <-ctx.Done():
			quit()
			return nil, errors.New("signing in took too long")
		}
		// Chrome has quit (by the user, or asked to): what it saved decides
		if cs, ok := signedIn(); ok {
			return cs, nil
		}
		return nil, errSignInClosed
	}
}

// keepSessionCookies turns on "Continue where you left off" in Ant's own
// profile. Chrome then writes session cookies (many sites' sign-ins, such as
// Alibaba Cloud's ticket) to disk with the rest, so Ant can read them and
// they survive Chrome quitting. Only Ant's profile is touched.
func keepSessionCookies(profile string) {
	p := filepath.Join(profile, "Default", "Preferences")
	prefs := map[string]any{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &prefs)
	}
	session, _ := prefs["session"].(map[string]any)
	if session == nil {
		session = map[string]any{}
	}
	if session["restore_on_startup"] == float64(1) {
		return
	}
	session["restore_on_startup"] = 1
	prefs["session"] = session
	b, err := json.Marshal(prefs)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, b, 0o600)
}

// Sessions are kept in a connection's values as JSON.

type storedCookie struct {
	Host  string `json:"host"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func encodeSession(cs []siteCookie) string {
	out := make([]storedCookie, len(cs))
	for i, c := range cs {
		out[i] = storedCookie{c.host, c.name, c.value}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func decodeSession(s string) (browserSession, bool) {
	var in []storedCookie
	if s == "" || json.Unmarshal([]byte(s), &in) != nil || len(in) == 0 {
		return browserSession{}, false
	}
	out := browserSession{source: "Signed in with Ant"}
	for _, c := range in {
		out.cookies = append(out.cookies, siteCookie{c.Host, c.Name, c.Value})
	}
	return out, true
}

func cookieValue(cs []siteCookie, name string) string {
	for _, c := range cs {
		if c.name == name {
			return c.value
		}
	}
	return ""
}
