package quota

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
)

// Store keeps what the user set on the page: accounts they added (or
// completed), and found accounts they chose to hide. It is one JSON file
// readable by the user alone — ~/.config/ant/accounts.json — like the
// sign-in files of the apps Ant reads. With no path it lives in memory.
type Store struct {
	path   string
	mu     sync.Mutex
	mem    storeData
	vault  Vault
	opened map[string]openedSecret // by secret ref: what the vault last gave or took
}

type storeData struct {
	Version  int          `json:"version"`
	Accounts []Connection `json:"accounts"`
	Hidden   []string     `json:"hidden,omitempty"` // ids of found accounts the user removed
	// BrowserSites are the sites whose sign-in the user allowed Ant to read
	// from their everyday browsers (ruling R6: per-site opt-in).
	BrowserSites []string `json:"browserSites,omitempty"`
	// Notify: whether Ant shows notifications on this computer; nil is on.
	Notify *bool `json:"notify,omitempty"`
	// Prices are what the user pays for an account, by card id.
	Prices map[string]Price `json:"prices,omitempty"`
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) load() (storeData, error) {
	if s.path == "" {
		return cloneData(s.mem), nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return storeData{Version: 1}, nil
	}
	if err != nil {
		return storeData{}, err
	}
	var d storeData
	if err := json.Unmarshal(b, &d); err != nil {
		return storeData{}, errors.New("Ant's accounts file is damaged: " + s.path)
	}
	if s.vault != nil {
		// one key that won't open marks its own account; none opening at
		// all (another key file) is an error for every account
		sealed, opened := 0, 0
		var last error
		for i, c := range d.Accounts {
			if c.SecretRef == "" {
				continue
			}
			sealed++
			v, err := s.open(c.SecretRef, c.SecretRevision)
			if err != nil {
				d.Accounts[i].KeyError, last = err.Error(), err
				continue
			}
			opened++
			d.Accounts[i].Values = v
		}
		if sealed > 0 && opened == 0 {
			return storeData{}, errors.New("Ant couldn't open the saved keys: " + last.Error())
		}
	}
	return d, nil
}

func (s *Store) save(d storeData) error {
	d.Version = 1
	if s.path == "" {
		s.mem = cloneData(d)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if s.vault != nil {
		sealed, err := s.seal(d)
		if err != nil {
			return err // never written in plaintext instead
		}
		d = sealed
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".accounts-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Data returns what is stored.
func (s *Store) Data() (storeData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Put adds or replaces the account with c's id and un-hides it.
func (s *Store) Put(c Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	d.Accounts = slices.DeleteFunc(d.Accounts, func(x Connection) bool { return x.ID == c.ID })
	d.Accounts = append(d.Accounts, c)
	d.Hidden = slices.DeleteFunc(d.Hidden, func(x string) bool { return x == c.ID })
	return s.save(d)
}

// Migrate rewrites the saved accounts with fn, once: only when fn changes
// them, so what earlier versions saved becomes today's records for good
// (and what the user removes afterwards stays removed).
func (s *Store) Migrate(fn func([]Connection) []Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	for _, c := range d.Accounts {
		if c.KeyError != "" {
			return nil // never rewrite records while a key won't open
		}
	}
	out := fn(cloneData(d).Accounts)
	if reflect.DeepEqual(out, d.Accounts) {
		return nil
	}
	d.Accounts = out
	return s.save(d)
}

// Remove deletes an added account; a found one is hidden instead, so it
// doesn't come back on the next refresh.
func (s *Store) Remove(id string, found bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	d.Accounts = slices.DeleteFunc(d.Accounts, func(x Connection) bool { return x.ID == id })
	if found && !slices.Contains(d.Hidden, id) {
		d.Hidden = append(d.Hidden, id)
	}
	return s.save(d)
}

// BrowserAllowed reports whether the user allowed reading domain's sign-in
// from their everyday browsers. Any doubt is a no.
func (s *Store) BrowserAllowed(domain string) bool {
	d, err := s.Data()
	return err == nil && slices.Contains(d.BrowserSites, domain)
}

// SetBrowserSite allows or stops reading domain's sign-in from the
// everyday browsers.
func (s *Store) SetBrowserSite(domain string, allow bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	d.BrowserSites = slices.DeleteFunc(d.BrowserSites, func(x string) bool { return x == domain })
	if allow {
		d.BrowserSites = append(d.BrowserSites, domain)
	}
	return s.save(d)
}

// NotifyOn reports whether notifications are on (they are unless turned off).
func (s *Store) NotifyOn() bool {
	d, err := s.Data()
	return err == nil && (d.Notify == nil || *d.Notify)
}

// SetNotify turns notifications on or off.
func (s *Store) SetNotify(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	d.Notify = &on
	return s.save(d)
}

func cloneData(d storeData) storeData {
	out := storeData{Version: d.Version, Hidden: slices.Clone(d.Hidden), BrowserSites: slices.Clone(d.BrowserSites), Notify: d.Notify, Prices: maps.Clone(d.Prices)}
	// (Values are copied below; SecretRef/SecretRevision travel with c.)
	for _, c := range d.Accounts {
		v := map[string]string{}
		for k, x := range c.Values {
			v[k] = x
		}
		c.Values = v
		out.Accounts = append(out.Accounts, c)
	}
	return out
}
