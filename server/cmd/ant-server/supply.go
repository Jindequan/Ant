package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/providers/zai"
	"ant/server/internal/quota"
	"ant/server/internal/security"
	"ant/server/internal/store"
	"ant/server/internal/supply"
	"ant/server/internal/supply/quotasource"
	"ant/server/internal/transport"
)

// localOwner owns the secrets added on the page. Ant is a local app with no
// login (ruling R8): this is an ownership record, not an identity anyone
// signs in as.
const localOwner domain.ID = "local"

// newQuotaPage wires the page's accounts. In demo mode the page shows sample
// accounts and nothing is read. Otherwise the supply engine discovers and
// reads every account through the quota source (design 11 phase 2) and
// records them in namespace ns, and keys added on the page are kept
// encrypted in Ant's database. The returned engine must be started and
// waited for alongside the server.
func newQuotaPage(demo bool, st store.Store, clock supply.Clock, ns domain.ID, logger *slog.Logger) (*quota.Service, *supply.Engine, error) {
	local := quota.DefaultLocal()
	if demo {
		return quota.NewService(local, quota.NewStore(""), quota.Options{Demo: true}), nil, nil // additions kept in memory
	}
	accounts := quota.NewStore(local.AccountsFile())
	vault, err := newLocalVault(st, clock, filepath.Join(filepath.Dir(local.AccountsFile()), "master.key"), ns)
	if err != nil {
		return nil, nil, err
	}
	if err := accounts.UseVault(vault); err != nil {
		// the page shows this on every read until it is fixed; found accounts keep working
		logger.Error("saved keys unavailable", "err", err)
	}
	local.Browser = accounts.BrowserAllowed // only sites the user allowed (R6)
	natives, err := providerModules()
	if err != nil {
		return nil, nil, err
	}
	src := quotasource.New(local, accounts, quota.Providers(), natives...)
	engine := supply.NewEngine(ns, st, src, clock, supply.DefaultPolicy(), logger.With("component", "supply"))
	reader := quotasource.Reader{Source: src, Engine: engine}
	return quota.NewService(local, accounts, quota.Options{Reader: reader, Insights: reader, Notifier: quota.MacNotifier{}}), engine, nil
}

// providerModules are the providers read through their own module (design
// 12), each with a Fetch limited to the origins it declares.
func providerModules() ([]quotasource.Native, error) {
	var out []quotasource.Native
	for _, m := range []catalog.Module{zai.Module(zai.DefaultBase), deepseek.SupplyModule(deepseek.DefaultBase)} {
		fetch, err := transport.NewObservationFetch(m.Supply.Origins, nil)
		if err != nil {
			return nil, fmt.Errorf("provider module %s: %w", m.ID, err)
		}
		out = append(out, quotasource.Native{Module: m, Fetch: fetch})
	}
	return out, nil
}

// localVault keeps the page's account values encrypted in Ant's database
// (design 11 §5) with a 32-byte key file Ant creates on first use, next to
// accounts.json and as private (0600 in a 0700 directory).
type localVault struct {
	secrets *application.SecretCommands
	auth    domain.AuthContext
}

func newLocalVault(st store.Store, clock supply.Clock, keyPath string, ns domain.ID) (localVault, error) {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return localVault{}, fmt.Errorf("create key directory: %w", err)
	}
	if _, err := os.Lstat(keyPath); errors.Is(err, os.ErrNotExist) {
		if err := generateMasterKey(keyPath); err != nil {
			return localVault{}, err
		}
	}
	key, err := loadMasterKey(keyPath)
	if err != nil {
		return localVault{}, fmt.Errorf("load %s: %w", keyPath, err)
	}
	cipher, err := security.NewSecretCipher(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return localVault{}, err
	}
	secrets, err := application.NewSecretCommands(st, cipher, clock, "ant-local-key-v1")
	if err != nil {
		return localVault{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = st.SubmitWrite(ctx, store.WriteCommand{ID: "supply-local-owner", Run: func(tx store.WriterTx) error {
		stx, ok := tx.(store.SupplyWriterTx)
		if !ok {
			return store.ErrUnavailable
		}
		return stx.EnsureLocalOwner(localOwner, "This Mac", clock.Now())
	}})
	if err != nil {
		return localVault{}, fmt.Errorf("create the local owner: %w", err)
	}
	return localVault{secrets: secrets, auth: domain.AuthContext{PrincipalID: localOwner, Namespace: ns,
		Permissions: []domain.Permission{domain.PermConfigure}}}, nil
}

func vaultContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func vaultCommandID() domain.ID {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return domain.ID("vault-" + hex.EncodeToString(b))
}

func (v localVault) Seal(values map[string]string) (string, string, error) {
	b, err := json.Marshal(values)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := vaultContext()
	defer cancel()
	view, err := v.secrets.Create(ctx, v.auth, vaultCommandID(), string(b))
	return string(view.Ref), string(view.Revision), err
}

func (v localVault) Reseal(ref, revision string, values map[string]string) (string, error) {
	b, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	ctx, cancel := vaultContext()
	defer cancel()
	view, err := v.secrets.Rotate(ctx, v.auth, domain.ID(ref), vaultCommandID(), domain.ID(revision), string(b))
	return string(view.Revision), err
}

func (v localVault) Open(ref, revision string) (map[string]string, error) {
	ctx, cancel := vaultContext()
	defer cancel()
	m, err := v.secrets.ResolveOwned(ctx, v.auth, domain.ID(ref), domain.ID(revision))
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(m["value"]), &values); err != nil {
		return nil, errors.New("a saved key is damaged")
	}
	return values, nil
}
