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
}

// Service finds every account (on the machine and in the Store), reads each
// by its mode's rule, and keeps the latest snapshot. It refreshes on start,
// every Interval and when asked; one refresh runs at a time and each read
// gets ReadTimeout.
type Service struct {
	local       Local
	providers   []Provider
	store       *Store
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
	Interval    time.Duration // default 10 minutes
	ReadTimeout time.Duration // default 20 seconds
	Demo        bool          // sample accounts; reads nothing, keeps additions in memory
}

func NewService(local Local, store *Store, opt Options) *Service {
	if opt.Interval <= 0 {
		opt.Interval = 10 * time.Minute
	}
	if opt.ReadTimeout <= 0 {
		opt.ReadTimeout = 20 * time.Second
	}
	return &Service{local: local, providers: Providers(), store: store, interval: opt.Interval,
		readTimeout: opt.ReadTimeout, demo: opt.Demo, conns: map[string]Connection{}}
}

// Start refreshes now and every interval until ctx ends. Wait blocks until
// the loop has returned.
func (s *Service) Start(ctx context.Context) {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.snap
	out.Accounts = slices.Clone(s.snap.Accounts)
	out.Refreshing = s.running
	out.Demo = s.demo
	return out
}

// connections is every account: found on the machine, then the Store's,
// which replace found ones with the same id; hidden ones are left out.
func (s *Service) connections() ([]Connection, error) {
	d, err := s.store.Data()
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
	if s.demo {
		for _, c := range demoConnections() {
			put(c)
		}
	} else {
		for _, p := range s.providers {
			for _, m := range p.Modes {
				for _, w := range m.allWays() {
					if w.Discover == nil {
						continue
					}
					for _, c := range w.Discover(s.local) {
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
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	s.running = true
	prev := map[string]Account{}
	for _, a := range s.snap.Accounts {
		prev[a.ID] = a
	}
	s.mu.Unlock()

	conns, err := s.connections()
	var accounts []Account
	if err != nil {
		accounts = []Account{{ID: "store", Provider: "ant", Name: "Ant", Status: StatusError, Message: err.Error(), ReadAt: s.local.Now()}}
	} else if s.demo {
		accounts = demoAccounts(conns, s.local.Now())
	} else {
		accounts = make([]Account, len(conns))
		var wg sync.WaitGroup
		for i, c := range conns {
			if only != nil && !only[c.ID] {
				if a, ok := prev[c.ID]; ok {
					accounts[i] = a
					continue
				}
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				rctx, cancel := context.WithTimeout(ctx, s.readTimeout)
				defer cancel()
				accounts[i] = s.read(rctx, c)
			}()
		}
		wg.Wait()
		s.keepRenewed(conns, accounts)
		accounts = mergeSame(accounts)
	}
	sortAccounts(accounts)

	s.mu.Lock()
	s.snap = Snapshot{RefreshedAt: time.Now().UTC(), Accounts: accounts}
	s.conns = map[string]Connection{}
	for _, c := range conns {
		s.conns[c.ID] = c
	}
	s.running = false
	s.mu.Unlock()
	return s.Snapshot()
}

func (s *Service) read(ctx context.Context, c Connection) Account {
	p, ok := providerByID(s.providers, c.Provider)
	m, okMode := p.mode(c.Mode)
	m = m.withWay(c.Way)
	if !ok || !okMode || m.Read == nil {
		return Account{ID: c.ID, Provider: c.Provider, Name: c.Provider, Status: StatusError, Origin: c.Origin,
			Message: "Ant no longer knows this kind of account. Remove it.", ReadAt: s.local.Now()}
	}
	return m.Read(ctx, s.local, c)
}

// keepRenewed saves what readers renewed into their accounts' stored values,
// so a rotated refresh token isn't lost.
func (s *Service) keepRenewed(conns []Connection, accounts []Account) {
	byID := map[string]Connection{}
	for _, c := range conns {
		byID[c.ID] = c
	}
	for i := range accounts {
		r := accounts[i].Renewed
		c, ok := byID[accounts[i].ID]
		if len(r) == 0 || !ok {
			continue
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
		_ = s.store.Put(c)
		accounts[i].Renewed = nil
	}
}
