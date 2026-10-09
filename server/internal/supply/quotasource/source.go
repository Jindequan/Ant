// Package quotasource is design 11's phase-2 adapter: it lets the supply
// engine find and read credentials with today's quota providers, and lets
// the quota page show what the engine read. It goes away with
// internal/quota in phase 4, when providers are ported to their own modules.
package quotasource

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/quota"
	"ant/server/internal/store"
	"ant/server/internal/supply"
)

// Source is a supply.Source over the quota providers. It keeps the latest
// Account of each connection for the page, and hands each read to the
// engine as a card to keep (design 15); the cards kept by the last run are
// shown until each connection is read again.
type Source struct {
	local     quota.Local
	store     *quota.Store
	providers []quota.Provider

	natives *natives

	mu     sync.Mutex
	conns  []quota.Connection
	byID   map[domain.ID]quota.Connection
	latest map[string]quota.Account
	at     time.Time
	found  bool // discovered at least once

	kept   map[string]quota.Account // the last run's cards, until read again
	keptAt time.Time
}

// card is an Account as kept: the page's fields and what merging accounts
// needs. A reader's renewed values are never in it (Account.Renewed isn't
// encoded).
type card struct {
	Account  quota.Account `json:"account"`
	Identity string        `json:"identity,omitempty"`
	Quiet    bool          `json:"quiet,omitempty"`
}

func cardOf(a quota.Account) []byte {
	b, err := json.Marshal(card{Account: a, Identity: a.Identity, Quiet: a.Quiet})
	if err != nil {
		return nil // not kept; the reading still is
	}
	return b
}

// Restore shows the cards the engine kept until each is read again.
func (s *Source) Restore(cards []store.SupplyCardRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range cards {
		var c card
		if json.Unmarshal(r.CardJSON, &c) != nil || c.Account.ID != string(r.CredentialID) {
			continue
		}
		c.Account.Identity, c.Account.Quiet = c.Identity, c.Quiet
		s.kept[c.Account.ID] = c.Account
		if at := time.UnixMilli(int64(r.ReadAt)).UTC(); at.After(s.keptAt) {
			s.keptAt = at
		}
	}
}

// New reads with providers (quota.Providers() in production); a provider
// with a native module is read through that module instead of its reader.
func New(local quota.Local, store *quota.Store, providers []quota.Provider, native ...Native) *Source {
	n := &natives{byProvider: map[string]Native{}, models: map[string]cachedModels{}}
	for _, x := range native {
		n.byProvider[x.Module.ID] = x
	}
	return &Source{local: local, store: store, providers: providers, natives: n,
		byID: map[domain.ID]quota.Connection{}, latest: map[string]quota.Account{}, kept: map[string]quota.Account{}}
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
	s.conns, s.byID, s.found = conns, byID, true
	for _, cards := range []map[string]quota.Account{s.latest, s.kept} { // a vanished connection's card goes with it
		for id := range cards {
			if _, ok := byID[domain.ID(id)]; !ok {
				delete(cards, id)
			}
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
	if nat, ok := s.natives.of(c.Provider); ok {
		// the module reads the sessions holding what it needs; the others
		// (a browser sign-in) stay with the quota reader
		rc, err := quota.Resolve(s.providers, s.local, c)
		if err != nil || nat.Module.Supply.Reads(catalog.Session{Product: rc.Mode, Values: rc.Values}) {
			a, r := s.natives.collect(ctx, s.providers, rc, s.local.Now(), nat, err)
			s.keep(c, a)
			r.Card = cardOf(a)
			return r
		}
	}
	a := quota.Read(ctx, s.providers, s.local, c)
	if err := quota.KeepRenewed(s.store, c, &a); err != nil {
		a.Status, a.Message = quota.StatusError, "Couldn't save the renewed sign-in: "+err.Error()
	}
	s.keep(c, a)
	r := result(a)
	r.Card = cardOf(a)
	return r
}

func (s *Source) keep(c quota.Connection, a quota.Account) {
	s.mu.Lock()
	s.latest[c.ID] = a
	s.at = s.local.Now().UTC()
	s.mu.Unlock()
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
	if out.RefreshedAt.IsZero() {
		out.RefreshedAt = s.keptAt
	}
	if !s.found { // before the first discovery: every kept card
		ids := slices.Sorted(maps.Keys(s.kept))
		for _, id := range ids {
			out.Accounts = append(out.Accounts, s.kept[id])
		}
	}
	for _, c := range s.conns {
		if a, ok := s.latest[c.ID]; ok {
			out.Accounts = append(out.Accounts, a)
		} else if a, ok := s.kept[c.ID]; ok {
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

// Insights turns the engine's outlooks into the page's insights, matching
// each meter back to its card row by the names the reader gave it.
func (r Reader) Insights(ctx context.Context) (map[string][]quota.Insight, error) {
	outlooks, err := r.Engine.Outlooks(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]quota.Insight, len(outlooks))
	for cred, meters := range outlooks {
		for _, m := range meters {
			out[string(cred)] = append(out[string(cred)], insight(m))
		}
	}
	return out, nil
}

func insight(m supply.MeterOutlook) quota.Insight {
	var ev struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	_ = json.Unmarshal(m.Latest.EvidenceJSON, &ev)
	var sc struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(m.Resource.ScopeJSON, &sc)
	in := quota.Insight{Outlook: m.Outlook.Kind, Unit: m.Resource.Unit}
	if m.Resource.Currency != "" {
		in.Currency, in.Label = m.Resource.Currency, ev.Label
	} else {
		in.Window, in.Model = ev.Name, sc.Model
	}
	if m.Outlook.At != nil {
		t := time.UnixMilli(int64(*m.Outlook.At)).UTC()
		in.At = &t
	}
	if m.Outlook.PerDay != nil {
		f := fromUnits(*m.Outlook.PerDay)
		in.PerDay = &f
	}
	for _, p := range m.Points {
		if left, ok := remaining(p); ok {
			in.Points = append(in.Points, [2]float64{float64(p.ObservedAt), fromUnits(left)})
		}
	}
	return in
}

func remaining(r store.SupplyReadingRecord) (domain.Units, bool) {
	switch {
	case r.Remaining != nil:
		return *r.Remaining, true
	case r.Limit != nil && r.Used != nil:
		return *r.Limit - *r.Used, true
	}
	return 0, false
}

// fromUnits is an adapter meter's units (quantumExp) as a number.
func fromUnits(u domain.Units) float64 { return float64(u) / 1e6 }

// Routes answers the page's route previews and pauses from the engine;
// Name is how a credential's card is called.
type Routes struct {
	Engine *supply.Engine
	Name   func(credentialID string) string
}

// Preview is the engine's route preview for a model named any way the user
// or a provider names it, with each account as its card calls it.
func (r Routes) Preview(ctx context.Context, model string) (quota.RoutePreview, error) {
	p, err := r.Engine.Preview(ctx, observed.ModelID(model))
	if err != nil {
		return quota.RoutePreview{}, err
	}
	out := quota.RoutePreview{Model: p.Model, Use: []quota.RouteChoice{}, NotUsed: []quota.RouteChoice{}}
	for _, c := range p.Eligible {
		out.Use = append(out.Use, r.choice(c.Route, c.Class, c.Reason, c.Stale))
	}
	for _, x := range p.Excluded {
		out.NotUsed = append(out.NotUsed, r.choice(x.Route, "", x.Reason, ""))
	}
	return out, nil
}

func (r Routes) choice(route supply.Route, class, reason, stale string) quota.RouteChoice {
	return quota.RouteChoice{ID: string(route.CredentialID), Provider: string(route.ProviderID), Name: r.Name(string(route.CredentialID)),
		Endpoint: route.Endpoint, Model: route.UpstreamModel, Class: class, Reason: reason, Stale: stale}
}

func (r Routes) SetPaused(ctx context.Context, ids []string, paused bool) error {
	cred := make([]domain.ID, len(ids))
	for i, id := range ids {
		cred[i] = domain.ID(id)
	}
	return r.Engine.SetPaused(ctx, cred, paused)
}

func (r Routes) Paused(ctx context.Context) (map[string]bool, error) {
	got, err := r.Engine.Paused(ctx)
	out := make(map[string]bool, len(got))
	for id := range got {
		out[string(id)] = true
	}
	return out, err
}

// Routes are the reader's route previews and pauses, named as the cards are.
func (r Reader) Routes() Routes { return Routes{Engine: r.Engine, Name: r.Source.name} }

// name is how a connection's card is called.
func (s *Source) name(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.latest[id]; ok {
		return firstOf(a.Label, a.Name)
	}
	return id
}
