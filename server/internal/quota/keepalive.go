package quota

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// Keeping Ant's own sign-ins alive. A site extends a session when it's
// used; Ant reads its cookies from disk and never visits, so a sign-in made
// in Ant's window would lapse. Once a day Ant opens the site's page in that
// sign-in's own profile, in Chrome without a window, waits for it to load,
// and quits: to the site, a visit. Nothing is clicked or sent. It can't
// outlast a site's own limit (a forced sign-in every N days, a password
// change). The user turns it off per account.

const (
	keepAliveEvery = 20 * time.Hour   // at most one visit a day per sign-in
	keepAliveCheck = 30 * time.Minute // how often Ant looks
	keepAliveVisit = 90 * time.Second // the most one visit may take
)

// keepAliveRunner visits url in the sign-in profile key; tests replace it.
var keepAliveRunner = visitQuietly

func visitQuietly(ctx context.Context, l Local, url, key string) error {
	chrome, _, err := findChrome(l)
	if err != nil {
		return err
	}
	profile := antProfile(l, key)
	if _, err := os.Stat(profile); err != nil {
		return errors.New("the sign-in's profile is gone")
	}
	ctx, cancel := context.WithTimeout(ctx, keepAliveVisit)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check", "--disable-sync", "--virtual-time-budget=15000", "--dump-dom", url)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run()
}

// keptAlive reports whether c is a sign-in Ant keeps alive, and the page it
// visits for it: the provider's own page, else its sign-in page.
func keptAlive(providers []Provider, c Connection) (url string, ok bool) {
	if c.Values["session"] == "" || c.Values["cookie"] != "" {
		return "", false
	}
	p, found := providerByID(providers, c.Provider)
	if !found {
		return "", false
	}
	m, found := p.mode(c.Mode)
	if !found {
		return "", false
	}
	si := m.withWay(c.Way).SignIn
	if si == nil || si.Login != nil || si.Browser {
		return "", false
	}
	if m.Link != nil {
		if u := m.Link(c.Values); u != "" {
			return u, true
		}
	}
	if p.Link != "" {
		return p.Link, true
	}
	return si.URL, true
}

// keepAliveOnce visits the site of every kept sign-in not visited for a
// day, one at a time; never while a sign-in window is open.
func (s *Service) keepAliveOnce(ctx context.Context) {
	signInMu.Lock()
	busy := signInNow != nil
	signInMu.Unlock()
	if busy {
		return
	}
	// the store, not the last read: sign-ins are added accounts, and a
	// copy from the last read could undo what was saved since
	d, err := s.store.Data()
	if err != nil {
		return
	}
	now := s.local.Now()
	for _, c := range d.Accounts {
		url, ok := keptAlive(s.providers, c)
		if !ok || c.Values["keepAlive"] == "off" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, c.Values["keptAt"]); err == nil && now.Sub(t) < keepAliveEvery {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		_ = keepAliveRunner(ctx, s.local, url, c.Values["profile"]) // a failed visit is tried again tomorrow
		v := map[string]string{}
		for k, x := range c.Values {
			v[k] = x
		}
		v["keptAt"] = now.UTC().Format(time.RFC3339)
		c.Values = v
		_ = s.store.Put(c)
	}
}

// setKeepAlive turns keeping a sign-in alive on or off.
func (s *Service) setKeepAlive(id string, on bool) error {
	d, err := s.store.Data()
	if err != nil {
		return err
	}
	var c Connection
	for _, x := range d.Accounts {
		if x.ID == id {
			c = x
		}
	}
	if c.ID == "" {
		return inputError("no such account")
	}
	if _, kept := keptAlive(s.providers, c); !kept {
		return inputError("Ant keeps alive only the sign-ins made in its own window")
	}
	v := map[string]string{}
	for k, x := range c.Values {
		v[k] = x
	}
	if on {
		delete(v, "keepAlive")
	} else {
		v["keepAlive"] = "off"
	}
	c.Values = v
	return s.store.Put(c)
}
