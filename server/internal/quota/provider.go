package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// A Provider is one service the user has accounts with. It lists the ways
// it can be paid for — its Modes — and each Mode owns its own rule: what
// it takes to set up, where Ant finds it on this machine, and how its
// allowance or balance is read. Nothing is shared between modes but the
// provider's name.
type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"` // where the account is managed
	// Media is what the provider makes: text, image, video, music, speech,
	// design, agent or search. The page filters and groups by it.
	Media []string `json:"media"`
	Modes []Mode   `json:"modes"`
}

// Mode is one way of paying a provider, with its own rule.
type Mode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Rule says, in a sentence, how this mode's allowance works.
	Rule string `json:"rule"`
	// Media, when set, is what this mode makes, narrower than its provider.
	Media []string `json:"media,omitempty"`
	// Fields are what adding it by hand asks for. None means it can only be
	// found on this machine; Connect then says how to make it findable.
	Fields  []Field `json:"fields"`
	Connect string  `json:"connect,omitempty"`
	// SignIn: the mode is connected by signing in to the provider's
	// website in a window Ant opens.
	SignIn *SignIn `json:"signIn,omitempty"`
	// Key is the field that identifies the account (its API key), so the
	// same key added on the page and found on the machine is one account.
	Key string `json:"-"`
	// Live: the provider reports this mode's allowance or balance. When
	// false Ant shows what follows from the mode's rule and the setup.
	Live bool `json:"live"`

	// Discover finds this mode's sign-ins already on the machine.
	Discover func(Local) []Connection `json:"-"`
	// Read reads one connection of this mode.
	Read func(context.Context, Local, Connection) Account `json:"-"`
	// Link, when set, is where a connection of this mode is managed, from
	// its values (a platform per region); otherwise the provider's link.
	Link func(values map[string]string) string `json:"-"`

	// Ways are the ways of reading this mode, easiest first: what another
	// app on this machine already holds, then keys, then sign-ins. Each
	// finds its own information or takes it from the user. A mode without
	// Ways has one: its own Fields, SignIn, Discover and Read.
	Ways []Way `json:"ways,omitempty"`
}

// Way is one way of reading a mode.
type Way struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Hint says what the way uses, or how to make it findable.
	Hint   string  `json:"hint,omitempty"`
	Fields []Field `json:"fields"`
	SignIn *SignIn `json:"signIn,omitempty"`
	// BrowserDomain: the way reads this site's sign-in from the everyday
	// browsers, which the user must allow first (R6).
	BrowserDomain string                                           `json:"browserDomain,omitempty"`
	Key           string                                           `json:"-"`
	Discover      func(Local) []Connection                         `json:"-"`
	Read          func(context.Context, Local, Connection) Account `json:"-"`
	// Resolve, when set, reads the connection's values at read time (bl's
	// sign-in, which bl login replaces): a provider module reads with them.
	Resolve func(Local, Connection) (map[string]string, error) `json:"-"`
}

// ErrSignedOut: the values a connection reads with are gone (the app that
// held them signed out). Wrapped with what the user should do.
var ErrSignedOut = errors.New("signed out")

// Resolve is connection c with the values it reads with now: its way's
// Resolve, else the values it has.
func Resolve(providers []Provider, l Local, c Connection) (Connection, error) {
	p, _ := providerByID(providers, c.Provider)
	m, _ := p.mode(c.Mode)
	for i, w := range m.Ways {
		if w.ID == c.Way || (c.Way == "" && i == 0) {
			if w.Resolve != nil {
				v, err := w.Resolve(l, c)
				if err != nil {
					return c, err
				}
				c.Values = v
			}
			break
		}
	}
	return c, nil
}

// allWays is the mode's ways, or its one way.
func (m Mode) allWays() []Way {
	if len(m.Ways) > 0 {
		return m.Ways
	}
	return []Way{{ID: "", Name: m.Name, Fields: m.Fields, SignIn: m.SignIn, Key: m.Key, Discover: m.Discover, Read: m.Read}}
}

// withWay is the mode as read by one of its ways: that way's fields,
// sign-in, finding and reading. An unknown id is the first way.
func (m Mode) withWay(id string) Mode {
	if len(m.Ways) == 0 {
		return m
	}
	w := m.Ways[0]
	for _, x := range m.Ways {
		if x.ID == id {
			w = x
		}
	}
	m.Fields, m.SignIn, m.Key, m.Discover, m.Read = w.Fields, w.SignIn, w.Key, w.Discover, w.Read
	if w.Hint != "" {
		m.Connect = w.Hint
	}
	return m
}

// wayName names the way a connection is read by.
func (m Mode) wayName(id string) string {
	for _, w := range m.Ways {
		if w.ID == id {
			return w.Name
		}
	}
	return ""
}

// Field is one input a mode asks for.
type Field struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text, secret, date, select
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
	Options     []Option `json:"options,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Origin says how Ant knows about a connection.
type Origin string

const (
	OriginFound Origin = "found" // on this machine: another app's sign-in or the environment
	OriginAdded Origin = "added" // added on Ant's page
)

// Connection is one sign-in of one mode: found on the machine or added on
// the page. Values hold the mode's fields; secrets never leave the server.
type Connection struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Mode     string            `json:"mode"`
	Way      string            `json:"way,omitempty"` // which of the mode's ways reads it
	Label    string            `json:"label,omitempty"`
	Origin   Origin            `json:"origin"`
	Source   string            `json:"source,omitempty"` // "ZCode", "~/.codex/auth.json", "Chrome · Default"
	Values   map[string]string `json:"values,omitempty"`
	AddedAt  time.Time         `json:"addedAt,omitzero"`
	// SecretRef and SecretRevision say where Values are kept encrypted when
	// the Store has a Vault; accounts.json then holds no Values at all.
	SecretRef      string `json:"secretRef,omitempty"`
	SecretRevision string `json:"secretRevision,omitempty"`
	// KeyError: the saved key couldn't be opened this time. The account
	// shows why and its record is left as it is, never resealed over.
	KeyError string `json:"-"`
}

// fingerprint identifies a connection by its provider, mode and secret, so
// the same key found in two places, or found and also added, is one.
func fingerprint(provider, mode, secret string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + mode + "\x00" + secret))
	return provider + "-" + hex.EncodeToString(sum[:6])
}

// Providers is every provider Ant knows, in the order the page offers them.
func Providers() []Provider {
	return []Provider{
		// coding plans and subscriptions
		chatGPTProvider(), claudeProvider(), copilotProvider(), geminiProvider(), zaiProvider(), bigModelProvider(), kimiProvider(), miniMaxProvider(),
		aliyunProvider(), volcengineProvider(), mimoProvider(), openCodeProvider(),
		// API balances
		deepSeekProvider(), openRouterProvider(), siliconFlowProvider(), stepFunProvider(),
		// agents, search, chat
		manusProvider(), perplexityProvider(), poeProvider(),
		// image and video
		klingProvider(), leonardoProvider(), seaArtProvider(), sdWebProvider(),
		// music and voice
		sunoProvider(), elevenLabsProvider(),
		// design
		figmaProvider(), stitchProvider(),
	}
}

// mediaOf is what the mode makes: its own, else its provider's.
func (m Mode) mediaOf(p Provider) []string {
	if len(m.Media) > 0 {
		return m.Media
	}
	return p.media()
}

// media is what the provider makes; text unless it says otherwise.
func (p Provider) media() []string {
	if len(p.Media) == 0 {
		return []string{"text"}
	}
	return p.Media
}

func providerByID(ps []Provider, id string) (Provider, bool) {
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

func (p Provider) mode(id string) (Mode, bool) {
	for _, m := range p.Modes {
		if m.ID == id {
			return m, true
		}
	}
	return Mode{}, false
}

// base is the Account every read of a connection starts from.
func base(p Provider, m Mode, c Connection, now time.Time) Account {
	a := Account{
		ID: c.ID, Provider: p.ID, Name: p.Name, Mode: m.ID, ModeName: m.Name, Kind: m.Kind, Way: c.Way, WayName: m.wayName(c.Way), Media: m.mediaOf(p),
		Label: c.Label, Origin: c.Origin, Source: c.Source, Link: p.Link, ReadAt: now, Live: m.Live,
	}
	if m.Link != nil {
		if l := m.Link(c.Values); l != "" {
			a.Link = l
		}
	}
	return a
}

// NewAccount is the card for connection c before anything is read: its
// provider's and mode's names, kind, link and where it was found. Provider
// modules (design 12) fill in what they read.
func NewAccount(providers []Provider, c Connection, now time.Time) Account {
	p, ok := providerByID(providers, c.Provider)
	if !ok {
		return Account{ID: c.ID, Provider: c.Provider, Name: c.Provider, Origin: c.Origin, Source: c.Source, Label: c.Label, ReadAt: now}
	}
	m, _ := p.mode(c.Mode)
	return base(p, m.withWay(c.Way), c, now)
}
