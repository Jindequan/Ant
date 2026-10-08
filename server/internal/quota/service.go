package quota

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Snapshot is the latest read of every account.
type Snapshot struct {
	RefreshedAt time.Time `json:"refreshedAt"`
	Refreshing  bool      `json:"refreshing"`
	Accounts    []Account `json:"accounts"`
	Demo        bool      `json:"demo,omitempty"`
	// BrowserSites: the sites the user allowed Ant to read from their browsers.
	BrowserSites []string `json:"browserSites"`
	Notify       bool     `json:"notify"`
}

// Reading is the latest read of every connection, as whoever schedules the
// reads holds it.
type Reading struct {
	Conns       []Connection
	Accounts    []Account // one per connection read so far
	RefreshedAt time.Time
	Running     bool
}

// Reader schedules and runs the reads: the supply engine (design 11), through
// its quota source. Refresh returns when the reads of only (every
// connection, when empty) have ended.
type Reader interface {
	Latest() Reading
	Refresh(ctx context.Context, only []string)
}

// Service is the page's view of every account: it finds what to show
// through its Reader, keeps what the user adds or hides in the Store, and
// merges readings of one provider account into one card. In demo mode it
// reads nothing and shows sample accounts.
type Service struct {
	local       Local
	providers   []Provider
	store       *Store
	reader      Reader
	insights    InsightSource
	notifier    Notifier
	sent        map[string]bool // alert keys already shown
	interval    time.Duration
	readTimeout time.Duration
	demo        bool

	refreshMu sync.Mutex // one refresh at a time
	mu        sync.Mutex
	snap      Snapshot
	conns     map[string]Connection // as of the last refresh
	running   bool

	wg sync.WaitGroup
}

// Options tune a Service; zero values take defaults.
type Options struct {
	Reader      Reader        // required unless Demo
	Insights    InsightSource // optional: history-based outlooks
	Notifier    Notifier      // optional: notifications on this computer (never in demo)
	Interval    time.Duration // demo refresh; default 10 minutes
	ReadTimeout time.Duration // a page-triggered refresh waits this plus 5s; default 20 seconds
	Demo        bool          // sample accounts; reads nothing, keeps additions in memory
}

func NewService(local Local, store *Store, opt Options) *Service {
	if opt.Interval <= 0 {
		opt.Interval = 10 * time.Minute
	}
	if opt.ReadTimeout <= 0 {
		opt.ReadTimeout = 20 * time.Second
	}
	return &Service{local: local, providers: Providers(), store: store, reader: opt.Reader, insights: opt.Insights, notifier: opt.Notifier, sent: map[string]bool{}, interval: opt.Interval,
		readTimeout: opt.ReadTimeout, demo: opt.Demo, conns: map[string]Connection{}}
}

// Start refreshes the demo accounts now and every interval until ctx ends;
// outside demo mode the Reader schedules reads and Start does nothing. Wait
// blocks until the loop has returned.
func (s *Service) Start(ctx context.Context) {
	if !s.demo {
		if s.notifier != nil {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				t := time.NewTicker(notifyInterval)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						s.notifyOnce(ctx)
					}
				}
			}()
		}
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			s.Refresh(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) Snapshot() Snapshot {
	s.sync()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.snap
	out.Accounts = slices.Clone(s.snap.Accounts)
	out.Refreshing = s.running
	out.Demo = s.demo
	out.BrowserSites = []string{}
	if d, err := s.store.Data(); err == nil && d.BrowserSites != nil {
		out.BrowserSites = d.BrowserSites
	}
	out.Notify = s.store.NotifyOn()
	return out
}

func (s *Service) connections() ([]Connection, error) {
	return connections(s.local, s.store, s.providers, s.demo)
}

// Connections is every account to read: found on the machine, then the
// Store's, which replace found ones with the same id; hidden ones are left
// out.
func Connections(local Local, store *Store, providers []Provider) ([]Connection, error) {
	return connections(local, store, providers, false)
}

func connections(local Local, store *Store, providers []Provider, demo bool) ([]Connection, error) {
	d, err := store.Data()
	if err != nil {
		return nil, err
	}
	byID := map[string]Connection{}
	var order []string
	put := func(c Connection) {
		if _, seen := byID[c.ID]; !seen {
			order = append(order, c.ID)
		}
		byID[c.ID] = c
	}
	if demo {
		for _, c := range demoConnections() {
			put(c)
		}
	} else {
		for _, p := range providers {
			for _, m := range p.Modes {
				for _, w := range m.allWays() {
					if w.Discover == nil {
						continue
					}
					for _, c := range w.Discover(local) {
						if prev, ok := byID[c.ID]; ok && prev.Origin == OriginFound {
							continue // the same sign-in found twice: keep the first place
						}
						if c.Way == "" {
							c.Way = w.ID
						}
						put(c)
					}
				}
			}
		}
	}
	for _, c := range migrate(d.Accounts) {
		if found, ok := byID[c.ID]; ok && c.Source == "" {
			c.Source = found.Source
		}
		put(c)
	}
	out := make([]Connection, 0, len(order))
	for _, id := range order {
		if !slices.Contains(d.Hidden, id) {
			out = append(out, byID[id])
		}
	}
	return out, nil
}

// migrate turns accounts saved by earlier versions into today's: the one
// combined Bailian account (mode "console") became an account per instance,
// each keeping the sign-in; a Volcengine account names the way it was added.
func migrate(in []Connection) []Connection {
	var out []Connection
	have := map[string]bool{}
	for _, c := range in {
		have[c.ID] = true
	}
	for _, c := range in {
		if c.Provider == "aliyun" && c.Mode == "accesskey" { // folded into pay-as-you-go
			c.Mode = "payg"
		}
		if c.Provider == "aliyun" && c.Way == "" && c.Mode != "console" { // before Bailian had ways
			c.Way = "sign-in"
			if c.Values["accessKeyId"] != "" {
				c.Way = "accesskey"
			}
		}
		if (c.Provider == "suno" || c.Provider == "xiaomi") && c.Way == "" {
			c.Way = "cookie"
			if c.Values["session"] != "" {
				c.Way = "sign-in"
			}
		}
		if c.Provider == "volcengine" && c.Way == "" { // before Volcengine had ways
			c.Way = "accesskey"
			if c.Values["refreshToken"] != "" || c.Values["sts"] != "" {
				c.Way = "sign-in"
			}
		}
		if c.Provider != "aliyun" || c.Mode != "console" {
			out = append(out, c)
			continue
		}
		for _, mode := range []string{"token-plan", "payg", "free-quota"} {
			id := "aliyun-" + mode + "-signin"
			if have[id] {
				continue
			}
			n := c
			n.ID, n.Mode, n.Way, n.Values = id, mode, "sign-in", map[string]string{"session": c.Values["session"]}
			out = append(out, n)
		}
	}
	return out
}

// Refresh reads every account and returns the new snapshot.
func (s *Service) Refresh(ctx context.Context) Snapshot { return s.refresh(ctx, nil) }

// refresh reads the accounts; with only set, it reads just those ids and
// keeps the last read of the rest.
func (s *Service) refresh(ctx context.Context, only map[string]bool) Snapshot {
	if !s.demo {
		ids := make([]string, 0, len(only))
		for id := range only {
			ids = append(ids, id)
		}
		s.reader.Refresh(ctx, ids)
		return s.Snapshot()
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	conns, err := s.connections()
	var accounts []Account
	if err != nil {
		accounts = []Account{{ID: "store", Provider: "ant", Name: "Ant", Status: StatusError, Message: err.Error(), ReadAt: s.local.Now()}}
	} else {
		accounts = demoAccounts(conns, s.local.Now())
	}
	s.publish(conns, accounts, time.Now().UTC())
	return s.Snapshot()
}

// sync publishes the Reader's latest reads; demo accounts are published by
// refresh.
func (s *Service) sync() {
	if s.demo {
		return
	}
	r := s.reader.Latest()
	s.publish(r.Conns, r.Accounts, r.RefreshedAt)
	s.mu.Lock()
	s.running = r.Running
	s.mu.Unlock()
}

func (s *Service) publish(conns []Connection, accounts []Account, at time.Time) {
	accounts = mergeSame(slices.Clone(accounts))
	sortAccounts(accounts)
	byID := make(map[string]Connection, len(conns))
	for _, c := range conns {
		byID[c.ID] = c
	}
	s.mu.Lock()
	s.snap = Snapshot{RefreshedAt: at, Accounts: accounts}
	s.conns = byID
	s.mu.Unlock()
}

// Read reads one connection by its mode's rule.
func Read(ctx context.Context, providers []Provider, local Local, c Connection) Account {
	p, ok := providerByID(providers, c.Provider)
	m, okMode := p.mode(c.Mode)
	m = m.withWay(c.Way)
	if !ok || !okMode || m.Read == nil {
		return Account{ID: c.ID, Provider: c.Provider, Name: c.Provider, Status: StatusError, Origin: c.Origin,
			Message: "Ant no longer knows this kind of account. Remove it.", ReadAt: local.Now()}
	}
	return m.Read(ctx, local, c)
}

// KeepRenewed saves what a reader renewed into the connection's stored
// values, so a rotated refresh token isn't lost, and clears it from a.
// Readers renew only sign-ins Ant made itself (design 11 §3.8).
func KeepRenewed(store *Store, c Connection, a *Account) error {
	r := a.Renewed
	a.Renewed = nil
	if len(r) == 0 || a.ID != c.ID {
		return nil
	}
	v := map[string]string{}
	for k, x := range c.Values {
		v[k] = x
	}
	for k, x := range r {
		v[k] = x
	}
	c.Values = v
	if c.Origin == OriginFound {
		c.Origin = OriginAdded
	}
	return store.Put(c)
}
