package quota

import (
	"errors"
	"maps"
	"os"
	"strconv"
	"strings"
	"testing"
)

type fakeVault struct {
	secrets map[string]map[string]string // ref@revision → values
	n       int
	corrupt bool // Open answers something else
	seals   int
}

func newFakeVault() *fakeVault { return &fakeVault{secrets: map[string]map[string]string{}} }

func (v *fakeVault) Seal(values map[string]string) (string, string, error) {
	v.n++
	v.seals++
	ref, rev := "ref-"+strconv.Itoa(v.n), "rev-"+strconv.Itoa(v.n)
	v.secrets[ref+"@"+rev] = maps.Clone(values)
	return ref, rev, nil
}

func (v *fakeVault) Reseal(ref, _ string, values map[string]string) (string, error) {
	v.n++
	v.seals++
	rev := "rev-" + strconv.Itoa(v.n)
	v.secrets[ref+"@"+rev] = maps.Clone(values)
	return rev, nil
}

func (v *fakeVault) Open(ref, rev string) (map[string]string, error) {
	if v.corrupt {
		return map[string]string{"apiKey": "something else"}, nil
	}
	x, ok := v.secrets[ref+"@"+rev]
	if !ok {
		return nil, errors.New("no such secret")
	}
	return maps.Clone(x), nil
}

func plaintextFile(t *testing.T, l Local) string {
	t.Helper()
	path := l.AccountsFile()
	writeFile(t, path, `{"version":1,"accounts":[{"id":"deepseek-1","provider":"deepseek","mode":"api","origin":"added","values":{"apiKey":"sk-plain"}}]}`)
	return path
}

// Moving to the vault: the key leaves the file, and the account still reads
// back with it.
func TestVaultMovesKeysOutOfTheFile(t *testing.T) {
	l := testLocal(t, nil)
	path := plaintextFile(t, l)
	st, v := NewStore(path), newFakeVault()
	if err := st.UseVault(v); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "sk-plain") || !strings.Contains(string(b), `"secretRef": "ref-1"`) {
		t.Fatalf("file after moving:\n%s", b)
	}
	d, err := st.Data()
	if err != nil || d.Accounts[0].Values["apiKey"] != "sk-plain" {
		t.Fatalf("read back: %+v %v", d.Accounts, err)
	}
	// unchanged values are not sealed again; changed ones are, under the same ref
	if err := st.Put(d.Accounts[0]); err != nil || v.seals != 1 {
		t.Fatalf("put unchanged: %v, seals %d", err, v.seals)
	}
	c := d.Accounts[0]
	c.Values = map[string]string{"apiKey": "sk-new"}
	if err := st.Put(c); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	d, _ = st.Data()
	if strings.Contains(string(b), "sk-new") || d.Accounts[0].Values["apiKey"] != "sk-new" || d.Accounts[0].SecretRef != "ref-1" || v.seals != 2 {
		t.Fatalf("after edit: file\n%s\naccounts %+v seals %d", b, d.Accounts, v.seals)
	}
}

// If a sealed key doesn't read back the same, nothing is written: the file
// keeps its plaintext rather than losing the key.
func TestVaultLeavesTheFileAloneWhenReadbackFails(t *testing.T) {
	l := testLocal(t, nil)
	path := plaintextFile(t, l)
	before, _ := os.ReadFile(path)
	v := newFakeVault()
	v.corrupt = true
	if err := NewStore(path).UseVault(v); !errors.Is(err, errVaultReadback) {
		t.Fatalf("UseVault = %v, want a readback error", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("the file changed although the vault failed")
	}
}

func TestVaultFailureIsAnErrorNotAnEmptyKey(t *testing.T) {
	l := testLocal(t, nil)
	path := plaintextFile(t, l)
	if err := NewStore(path).UseVault(newFakeVault()); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	if err := st.UseVault(newFakeVault()); err == nil { // a vault that never saw these secrets
		t.Fatal("opening with the wrong vault succeeded")
	}
	if _, err := st.Data(); err == nil || !strings.Contains(err.Error(), "couldn't open the saved keys") {
		t.Fatalf("reading after a failed UseVault: %v; want the reason, not accounts without keys", err)
	}
}
