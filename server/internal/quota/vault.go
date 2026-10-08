package quota

import (
	"errors"
	"maps"
)

// Vault keeps the values of added accounts — keys, cookies, tokens, and the
// settings typed with them — out of accounts.json, encrypted in Ant's
// database (design 11 §5). The file keeps only a reference.
type Vault interface {
	Seal(values map[string]string) (ref, revision string, err error)
	Reseal(ref, revision string, values map[string]string) (newRevision string, err error)
	Open(ref, revision string) (map[string]string, error)
}

type openedSecret struct {
	revision string
	values   map[string]string
}

// UseVault makes the Store keep account values in v, and moves values still
// written in the file into it. The file is rewritten only after every
// moved value has been read back from the vault; if anything fails, the
// file is left exactly as it was.
func (s *Store) UseVault(v Vault) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vault, s.opened = v, map[string]openedSecret{}
	if s.path == "" {
		return nil
	}
	d, err := s.load()
	if err != nil {
		// Keep the vault: every later read fails with this reason, which the
		// page shows, instead of quietly reading accounts without their keys.
		return err
	}
	for _, c := range d.Accounts {
		if len(c.Values) > 0 && c.SecretRef == "" {
			return s.save(d) // seals, verifies, then writes
		}
	}
	return nil
}

func (s *Store) open(ref, revision string) (map[string]string, error) {
	if o, ok := s.opened[ref]; ok && o.revision == revision {
		return maps.Clone(o.values), nil
	}
	v, err := s.vault.Open(ref, revision)
	if err != nil {
		return nil, err
	}
	s.opened[ref] = openedSecret{revision: revision, values: maps.Clone(v)}
	return v, nil
}

var errVaultReadback = errors.New("a saved key read back differently")

// seal returns d as it is written to the file: every account's values in
// the vault (sealed anew only when they changed), and none in the file.
func (s *Store) seal(d storeData) (storeData, error) {
	out := cloneData(d)
	for i, c := range out.Accounts {
		if len(c.Values) == 0 {
			out.Accounts[i].SecretRef, out.Accounts[i].SecretRevision = "", ""
			continue
		}
		if o, ok := s.opened[c.SecretRef]; !ok || c.SecretRef == "" || o.revision != c.SecretRevision || !maps.Equal(o.values, c.Values) {
			var err error
			if c.SecretRef == "" {
				c.SecretRef, c.SecretRevision, err = s.vault.Seal(c.Values)
			} else {
				c.SecretRevision, err = s.vault.Reseal(c.SecretRef, c.SecretRevision, c.Values)
			}
			if err != nil {
				return storeData{}, errors.New("Ant couldn't save the key safely: " + err.Error())
			}
			delete(s.opened, c.SecretRef)
			back, err := s.open(c.SecretRef, c.SecretRevision)
			if err != nil || !maps.Equal(back, c.Values) {
				return storeData{}, errVaultReadback
			}
		}
		out.Accounts[i].SecretRef, out.Accounts[i].SecretRevision, out.Accounts[i].Values = c.SecretRef, c.SecretRevision, nil
	}
	return out, nil
}
