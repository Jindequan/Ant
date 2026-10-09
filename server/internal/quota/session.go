package quota

import (
	"context"
	"time"
)

// When a website sign-in ends: the expiry of the cookie that holds the
// session, in Ant's profile for that sign-in or in the everyday browser
// the account is read from. Ant can then say so before it happens.

// sessionEnds is when connection c's website sign-in ends, or nil when Ant
// can't tell (no session cookie named, a pasted cookie, a cookie that lasts
// as long as the browser is open).
func sessionEnds(ctx context.Context, l Local, m Mode, c Connection) *time.Time {
	var si *SignIn
	for _, w := range m.allWays() {
		if w.SignIn != nil && len(w.SignIn.Session) > 0 {
			si = w.SignIn
			break
		}
	}
	if si == nil || c.Values["cookie"] != "" || c.Values["token"] != "" {
		return nil
	}
	var ends map[string]time.Time
	switch {
	case c.Values["session"] != "": // signed in with Ant: its own profile
		_, e, err := profileCookies(ctx, l, si.Domain, c.Values["profile"])
		if err != nil {
			return nil
		}
		ends = e
	case c.Origin == OriginFound: // the everyday browser's sign-in
		s, err := l.findBrowserSession(ctx, si.Domain, si.Session[0])
		if err != nil {
			return nil
		}
		ends = s.ends
	default:
		return nil
	}
	for _, name := range si.Session {
		if t, ok := ends[name]; ok {
			return &t
		}
	}
	return nil
}
