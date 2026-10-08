// Package quotasource is design 11's phase-2 adapter: it lets the supply
// engine find and read credentials with today's quota providers, and lets
// the quota page show what the engine read. It goes away with
// internal/quota in phase 4, when providers are ported to their own modules.
package quotasource

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/quota"
	"ant/server/internal/supply"
)

// Source is a supply.Source over the quota providers. It keeps the latest
// Account of each connection for the page; accounts are presentation and
// are not persisted.
type Source struct {
	local     quota.Local
	store     *quota.Store
	providers []quota.Provider

	mu     sync.Mutex
	conns  []quota.Connection
	byID   map[domain.ID]quota.Connection
	latest map[string]quota.Account
	at     time.Time
}

// New reads with providers (quota.Providers() in production).
func New(local quota.Local, store *quota.Store, providers []quota.Provider) *Source {
	return &Source{local: local, store: store, providers: providers,
		byID: map[domain.ID]quota.Connection{}, latest: map[string]quota.Account{}}
}

// Discover lists every connection quota finds on the machine or in
// accounts.json, as credentials.
func (s *Source) Discover(context.Context) ([]supply.Credential, error) {
	conns, err := quota.Connections(s.local, s.store, s.providers)
	if err != nil {
		return nil, err
	}
	creds := make([]supply.Credential, 0, len(conns))
	byID := make(map[domain.ID]quota.Connection, len(conns))
	for _, c := range conns {
		byID[domain.ID(c.ID)] = c
		creds = append(creds, credential(c))
	}
	s.mu.Lock()
	s.conns, s.byID = conns, byID
	for id := range s.latest { // a vanished connection's card goes with it
		if _, ok := byID[domain.ID(id)]; !ok {
			delete(s.latest, id)
		}
	}
	s.mu.Unlock()
	return creds, nil
}

// Collect reads one connection with its provider's reader.
func (s *Source) Collect(ctx context.Context, cred supply.Credential) supply.Result {
	s.mu.Lock()
	c, ok := s.byID[cred.ID]
	s.mu.Unlock()
	if !ok {
		return supply.Result{Outcome: supply.OutcomeError, Detail: "connection vanished before it was read"}
	}
	a := quota.Read(ctx, s.providers, s.local, c)
	if err := quota.KeepRenewed(s.store, c, &a); err != nil {
		a.Status, a.Message = quota.StatusError, "Couldn't save the renewed sign-in: "+err.Error()
	}
	s.mu.Lock()
	s.latest[c.ID] = a
	s.at = s.local.Now().UTC()
	s.mu.Unlock()
	return result(a)
}

// result is what a reading tells the scheduler.
func result(a quota.Account) supply.Result {
	r := supply.Result{Detail: a.Message}
	switch a.Status {
	case quota.StatusOK:
		r.Outcome = supply.OutcomeOK
	case quota.StatusSignedOut:
		r.Outcome = supply.OutcomeSignedOut
	default:
		r.Outcome = supply.OutcomeError
	}
	windows := slices.Clone(a.Windows)
	for _, g := range a.Groups {
		windows = append(windows, g.Windows...)
	}
	for _, w := range windows {
		if w.ResetsAt != nil && !w.ResetEstimated {
			t := domain.Millis(w.ResetsAt.UnixMilli())
			if r.NextReset == nil || t < *r.NextReset {
				r.NextReset = &t
			}
		}
	}
	r.BalanceOnly = len(windows) == 0 && (len(a.Balances) > 0 || hasGroupBalances(a))
	if r.Outcome == supply.OutcomeOK {
		r.Inventory = inventory(a)
	}
	return r
}

func hasGroupBalances(a quota.Account) bool {
	for _, g := range a.Groups {
		if len(g.Balances) > 0 {
			return true
		}
	}
	return false
}

// locatorKeys are the connection values that say where a secret lives;
// every other value may be the secret itself and never enters the store.
var locatorKeys = []string{"authPath", "creds", "credentials", "binary", "blConfig", "veConfig", "arkEnv", "keychain", "profile", "login", "region", "site", "baseURL"}

// credential describes a connection for the store: where it was found and
// how, never its secret.
func credential(c quota.Connection) supply.Credential {
	loc := map[string]string{"mode": c.Mode}
	if c.Way != "" {
		loc["way"] = c.Way
	}
	if c.Source != "" {
		loc["source"] = c.Source
	}
	for _, k := range locatorKeys {
		if v := c.Values[k]; v != "" {
			loc[k] = v
		}
	}
	b, _ := json.Marshal(loc)
	way := c.Way
	if way == "" {
		way = "default"
	}
	origin := supply.OriginFound
	if c.Origin == quota.OriginAdded {
		origin = supply.OriginAdded
	}
	return supply.Credential{ID: domain.ID(c.ID), ProviderID: domain.ID(c.Provider), Kind: kind(c), Origin: origin,
		Strategy: "quota/" + way, Locator: b, SecretRef: domain.ID(c.SecretRef)}
}

// kind is the credential kind a connection's values show. It is a
// description for the store; nothing schedules or reads by it.
func kind(c quota.Connection) string {
	v := c.Values
	switch {
	case c.Provider == "copilot" || v["authPath"] != "" || v["creds"] != "" || v["credentials"] != "" || v["keychain"] != "" || v["binary"] != "":
		return "oauth" // an app's own OAuth sign-in, read where the app keeps it
	case v["sts"] != "" || v["veConfig"] != "" || v["arkEnv"] != "" || v["refreshToken"] != "" || v["sessionToken"] != "":
		return "sts"
	case v["token"] != "":
		return "console_token"
	case v["cookie"] != "" || v["session"] != "" || c.Way == "browser" || c.Way == "sign-in" || c.Way == "cookie":
		return "session"
	case v["apiKey"] != "" || v["key"] != "" || v["accessKeyId"] != "":
		return "api_key"
	default:
		return "none"
	}
}

// Reader serves the quota page from the engine.
type Reader struct {
	Source *Source
	Engine *supply.Engine
}

func (r Reader) Latest() quota.Reading {
	s := r.Source
	s.mu.Lock()
	out := quota.Reading{Conns: slices.Clone(s.conns), RefreshedAt: s.at}
	for _, c := range s.conns {
		if a, ok := s.latest[c.ID]; ok {
			out.Accounts = append(out.Accounts, a)
		}
	}
	s.mu.Unlock()
	out.Running = r.Engine.Running()
	return out
}

func (r Reader) Refresh(ctx context.Context, only []string) {
	ids := make([]domain.ID, len(only))
	for i, id := range only {
		ids[i] = domain.ID(id)
	}
	_ = r.Engine.RunNow(ctx, ids)
}
