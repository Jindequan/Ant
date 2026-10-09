package quota

import (
	"context"
	"errors"
	"strings"
)

// A website whose allowance only its own signed-in pages show is read with
// that sign-in, found three ways, easiest first: the user's everyday
// browser (Chrome, Edge, Brave, Arc, Firefox, Safari), Ant's own sign-in
// window, or a Cookie header the user pastes. Each site still reads its
// own endpoints with its own reader; webSite only finds the session.

type webSite struct {
	provider, name string
	domain         string // whose cookies make the session
	host           string // the host the reads go to, for the Cookie header
	marker         string // the cookie a signed-in browser has; "" when no one cookie says so
	signIn         *SignIn
	pasteHelp      string // where to copy the Cookie header from
}

var errWebSignedOut = errors.New("signed out")

// newWebSite builds a site; with no marker, its sign-in window waits for
// the user to say they're signed in.
func newWebSite(provider, name, domain, host, marker, signInURL, pasteHelp string) *webSite {
	w := &webSite{provider: provider, name: name, domain: domain, host: host, marker: marker, pasteHelp: pasteHelp}
	w.signIn = &SignIn{URL: signInURL, Domain: domain, Confirm: marker == "", Done: func(cs []siteCookie) bool {
		if marker == "" {
			return len(cs) > 0
		}
		return cookieValue(cs, marker) != ""
	}}
	return w
}

// ways are the site's three ways for one mode.
func (w *webSite) ways(mode string, read func(context.Context, Local, Connection) Account) []Way {
	return []Way{
		{ID: "browser", Name: "Your browser", Fields: []Field{}, Discover: w.discover(mode), Read: read, BrowserDomain: w.domain,
			Hint: "Ant reads the " + w.name + " sign-in of Chrome, Edge, Brave, Arc, Firefox or Safari on this Mac. Sign in to " + w.domain + " there. (Safari needs Full Disk Access for the app that runs Ant.)"},
		{ID: "sign-in", Name: "Sign in to " + w.name, Fields: []Field{}, SignIn: w.signIn, Read: read,
			Hint: "Sign in to " + w.name + " in the window Ant opens; Google sign-in works too. Ant keeps the session; it never sees your password."},
		{ID: "cookie", Name: "Paste the cookie", Key: "cookie", Read: read,
			Hint:   "Only if signing in doesn't work: " + w.pasteHelp,
			Fields: []Field{{ID: "cookie", Label: "Cookie", Type: "secret", Placeholder: "name=value; …", Help: "The whole Cookie header of a request to " + w.host + "."}}},
	}
}

// discover finds an everyday browser's session. With no marker cookie it
// can't tell signed in from merely visited: the read decides, and a
// visit without a sign-in stays quiet (Account.Quiet).
func (w *webSite) discover(mode string) func(Local) []Connection {
	return func(l Local) []Connection {
		s, err := l.findBrowserSession(context.Background(), w.domain, w.marker)
		if err != nil || len(s.cookies) == 0 {
			return nil
		}
		return []Connection{{ID: w.provider + "-browser-" + mode, Provider: w.provider, Mode: mode, Way: "browser",
			Origin: OriginFound, Source: s.source}}
	}
}

// session is the connection's session: pasted, Ant's window's, or the browser's.
func (w *webSite) session(ctx context.Context, l Local, c Connection) (browserSession, error) {
	if ck := strings.TrimSpace(c.Values["cookie"]); ck != "" {
		if !strings.Contains(ck, "=") && w.marker != "" {
			ck = w.marker + "=" + ck // a bare token is the marker cookie's value
		}
		return browserSession{cookies: parseCookieHeader(ck, w.domain), source: "Pasted cookie"}, nil
	}
	if c.Way == "cookie" {
		return browserSession{}, errWebSignedOut
	}
	if c.Values["session"] != "" {
		if s, ok := signedInSession(ctx, l, *w.signIn, c.Values["session"], c.Values["profile"]); ok {
			return s, nil
		}
		return browserSession{}, errWebSignedOut
	}
	s, err := l.findBrowserSession(ctx, w.domain, w.marker)
	if err != nil {
		return browserSession{}, errWebSignedOut
	}
	return s, nil
}

// signedOut fills a for a session the site no longer accepts. A browser's
// session found without a marker cookie may never have been a sign-in:
// that one stays quiet.
func (w *webSite) signedOut(a *Account, c Connection) {
	a.Status, a.Message = StatusSignedOut, "Your "+w.name+" sign-in has ended. Sign in again."
	if c.Origin == OriginFound && c.Way == "browser" {
		a.Quiet = true
	}
}
