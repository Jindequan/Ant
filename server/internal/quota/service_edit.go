package quota

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// --- adding, editing and removing accounts ---

// accountView is a connection as the edit form sees it: secrets are never
// sent back, only whether they are set.
type accountView struct {
	ID         string            `json:"id"`
	Provider   string            `json:"provider"`
	Mode       string            `json:"mode"`
	Way        string            `json:"way,omitempty"`
	Label      string            `json:"label"`
	Origin     Origin            `json:"origin"`
	Source     string            `json:"source,omitempty"`
	Values     map[string]string `json:"values"`
	SecretsSet []string          `json:"secretsSet"`
}

type accountInput struct {
	Provider string            `json:"provider"`
	Mode     string            `json:"mode"`
	Way      string            `json:"way"`
	Label    string            `json:"label"`
	Values   map[string]string `json:"values"`
}

var errBadInput = errors.New("bad input")

func inputError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errBadInput, fmt.Sprintf(format, args...))
}

// validate checks values against the mode's fields and returns the clean
// set. With prev, an empty secret keeps the previous one and an empty
// field that the previous had keeps it.
func validate(m Mode, in map[string]string, prev map[string]string) (map[string]string, error) {
	if len(m.Fields) == 0 {
		return nil, inputError("%s is found on this machine only. %s", m.Name, m.Connect)
	}
	out := map[string]string{}
	for _, f := range m.Fields {
		v := strings.TrimSpace(in[f.ID])
		if len(v) > 8192 {
			return nil, inputError("%s is too long", f.Label)
		}
		if v == "" && prev != nil {
			v = prev[f.ID]
		}
		if v == "" {
			if !f.Optional {
				return nil, inputError("%s is needed", f.Label)
			}
			continue
		}
		switch f.Type {
		case "select":
			if !slices.ContainsFunc(f.Options, func(o Option) bool { return o.Value == v }) {
				return nil, inputError("choose a %s", strings.ToLower(f.Label))
			}
		case "date":
			if _, ok := dateValue(v); !ok {
				return nil, inputError("%s must be a date", f.Label)
			}
		case "number":
			if n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64); err != nil || n < 0 {
				return nil, inputError("%s must be a number", f.Label)
			}
		}
		out[f.ID] = v
	}
	return out, nil
}

func dateValue(s string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
	return t, err == nil
}

func newID(provider string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return provider + "-" + hex.EncodeToString(b)
}

func (s *Service) add(in accountInput) (Connection, error) {
	p, ok := providerByID(s.providers, in.Provider)
	if !ok {
		return Connection{}, inputError("unknown provider")
	}
	m, ok := p.mode(in.Mode)
	if !ok {
		return Connection{}, inputError("unknown mode")
	}
	m = m.withWay(in.Way)
	values, err := validate(m, in.Values, nil)
	if err != nil {
		return Connection{}, err
	}
	id := newID(p.ID)
	if m.Key != "" && values[m.Key] != "" {
		// the same key as one found on the machine is that account
		id = fingerprint(p.ID, m.ID, values[m.Key])
	}
	c := Connection{ID: id, Provider: p.ID, Mode: m.ID, Way: in.Way, Label: label(in.Label), Origin: OriginAdded, Values: values, AddedAt: time.Now().UTC()}
	return c, s.store.Put(c)
}

func (s *Service) update(id string, in accountInput) (Connection, error) {
	s.sync()
	s.mu.Lock()
	c, ok := s.conns[id]
	s.mu.Unlock()
	if !ok {
		return Connection{}, inputError("no such account")
	}
	p, _ := providerByID(s.providers, c.Provider)
	m, ok := p.mode(c.Mode)
	m = m.withWay(c.Way)
	if !ok {
		return Connection{}, inputError("unknown mode")
	}
	values, err := validate(m, in.Values, c.Values)
	if err != nil {
		return Connection{}, err
	}
	// a found account the user completes or edits is kept as theirs, under
	// the same id, so the machine's copy no longer overrides it; what isn't
	// a field (a signed-in session) stays as it was
	merged := map[string]string{}
	for k, v := range c.Values {
		merged[k] = v
	}
	for _, f := range m.Fields {
		delete(merged, f.ID)
	}
	for k, v := range values {
		merged[k] = v
	}
	c.Values, c.Label, c.Origin = merged, label(in.Label), OriginAdded
	if c.AddedAt.IsZero() {
		c.AddedAt = time.Now().UTC()
	}
	return c, s.store.Put(c)
}

// signIn opens the provider's sign-in window and keeps the session it
// yields as an account of that mode. A new sign-in adds an account (one
// provider can have many); again names an account to sign in to again,
// which keeps its connections, name and place.
func (s *Service) signIn(ctx context.Context, provider, mode, way, again string) (Connection, error) {
	p, ok := providerByID(s.providers, provider)
	m, okMode := p.mode(mode)
	if way == "" { // the mode's sign-in way
		for _, w := range m.Ways {
			if w.SignIn != nil {
				way = w.ID
				break
			}
		}
	}
	m = m.withWay(way)
	if !ok || !okMode || m.SignIn == nil {
		return Connection{}, inputError("%s isn't signed in to through a window", provider)
	}
	if s.demo {
		return Connection{}, inputError("Demo mode doesn't open sign-in windows.")
	}
	// tail ties one sign-in's connections together, one per mode it reads
	tail := "-signin-" + newID("")[1:]
	if again != "" {
		s.sync()
		s.mu.Lock()
		c, ok := s.conns[again]
		s.mu.Unlock()
		if !ok || c.Provider != p.ID {
			return Connection{}, inputError("no such account")
		}
		if i := strings.LastIndex(c.ID, "-signin"); i >= 0 {
			tail = c.ID[i:] // "-signin" for a sign-in kept before accounts could be many
		}
	}
	var values map[string]string
	if m.SignIn.Login != nil {
		v, err := m.SignIn.Login(ctx, s.local)
		if err != nil {
			return Connection{}, inputError("%s", err.Error())
		}
		values = v
	} else {
		cookies, err := signInRunner(ctx, s.local, *m.SignIn)
		if err != nil {
			return Connection{}, inputError("%s", err.Error())
		}
		values = map[string]string{"session": encodeSession(cookies)}
	}
	// one sign-in connects every instance of the provider that reads with it
	var first Connection
	for _, sibMode := range p.Modes {
		sib, sibWay, found := sibMode, "", false
		for _, w := range sibMode.allWays() {
			if w.SignIn == m.SignIn {
				sibWay, found = w.ID, true
				break
			}
		}
		if !found {
			continue
		}
		v := map[string]string{}
		for k, x := range values {
			v[k] = x
		}
		c := Connection{ID: p.ID + "-" + sib.ID + tail, Provider: p.ID, Mode: sib.ID, Way: sibWay, Origin: OriginAdded,
			Source: "Signed in with Ant", Values: v, AddedAt: time.Now().UTC()}
		s.mu.Lock()
		if old, ok := s.conns[c.ID]; ok { // signed in again: keep its name and place
			c.Label, c.AddedAt = old.Label, old.AddedAt
		}
		s.mu.Unlock()
		if err := s.store.Put(c); err != nil {
			return Connection{}, err
		}
		if sib.ID == m.ID {
			first = c
		}
	}
	return first, nil
}

// remove removes an account and every sign-in merged into it.
func (s *Service) remove(id string) error {
	s.sync()
	s.mu.Lock()
	ids := []string{id}
	for _, a := range s.snap.Accounts {
		if a.ID == id && len(a.Keys) > 0 {
			ids = a.Keys
		}
	}
	conns := map[string]Connection{}
	for _, k := range ids {
		if c, ok := s.conns[k]; ok {
			conns[k] = c
		}
	}
	s.mu.Unlock()
	if len(conns) == 0 {
		return inputError("no such account")
	}
	for k, c := range conns {
		// A sign-in read from the browser goes by taking back the
		// permission: Ant stops opening the browser for that site, and
		// allowing it again brings the account back.
		if d := s.browserDomain(c.Provider, c.Mode, c.Way); d != "" && c.Origin == OriginFound {
			if err := s.store.SetBrowserSite(d, false); err != nil {
				return err
			}
			continue
		}
		// hide it too when the machine would find it again
		if err := s.store.Remove(k, c.Origin == OriginFound || s.foundOnMachine(k)); err != nil {
			return err
		}
	}
	return nil
}

// browserDomain is the site a way reads from the everyday browsers, or "".
func (s *Service) browserDomain(provider, mode, way string) string {
	p, ok := providerByID(s.providers, provider)
	if !ok {
		return ""
	}
	m, ok := p.mode(mode)
	if !ok {
		return ""
	}
	for _, w := range m.allWays() {
		if w.ID == way {
			return w.BrowserDomain
		}
	}
	return ""
}

// allowBrowser lets Ant read (or stops it reading) the site of a browser way.
func (s *Service) allowBrowser(provider, mode, way string, allow bool) error {
	d := s.browserDomain(provider, mode, way)
	if d == "" {
		return inputError("that way doesn't read a browser")
	}
	return s.store.SetBrowserSite(d, allow)
}

func (s *Service) foundOnMachine(id string) bool {
	if s.demo {
		return strings.HasPrefix(id, "demo-")
	}
	for _, p := range s.providers {
		for _, m := range p.Modes {
			for _, w := range m.allWays() {
				if w.Discover == nil {
					continue
				}
				for _, c := range w.Discover(s.local) {
					if c.ID == id {
						return true
					}
				}
			}
		}
	}
	return false
}

func (s *Service) view(id string) (accountView, bool) {
	s.sync()
	s.mu.Lock()
	c, ok := s.conns[id]
	s.mu.Unlock()
	if !ok {
		return accountView{}, false
	}
	p, _ := providerByID(s.providers, c.Provider)
	m, _ := p.mode(c.Mode)
	m = m.withWay(c.Way)
	v := accountView{ID: c.ID, Provider: c.Provider, Mode: c.Mode, Way: c.Way, Label: c.Label, Origin: c.Origin, Source: c.Source,
		Values: map[string]string{}, SecretsSet: []string{}}
	for _, f := range m.Fields {
		val := c.Values[f.ID]
		if f.Type == "secret" {
			if val != "" {
				v.SecretsSet = append(v.SecretsSet, f.ID)
			}
			continue
		}
		v.Values[f.ID] = val
	}
	return v, true
}

func label(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	return s
}
