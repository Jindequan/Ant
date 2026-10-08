package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"ant/server/internal/domain"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/store"
)

var (
	ErrDeepSeekBalanceInvalid     = errors.New("deepseek balance: invalid input")
	ErrDeepSeekBalanceForbidden   = errors.New("deepseek balance: forbidden")
	ErrDeepSeekBalanceUnavailable = errors.New("deepseek balance: unavailable")
	ErrDeepSeekBalanceBusy        = errors.New("deepseek balance: capacity reached")
	ErrDeepSeekBalanceResponse    = errors.New("deepseek balance: invalid provider response")
	ErrDeepSeekBalanceNotFound    = errors.New("deepseek balance: observation not found")
)

const deepSeekBalanceMaxInFlight = 8

type OwnedSecretResolver interface {
	ResolveOwned(context.Context, domain.AuthContext, domain.ID, domain.ID) (map[string]string, error)
}

type DeepSeekBalanceFetcher interface {
	FetchBalance(context.Context, string) ([]byte, error)
}

type DeepSeekBalanceObservation struct {
	IsAvailable bool                     `json:"isAvailable"`
	ObservedAt  domain.Millis            `json:"observedAt"`
	Balances    []DeepSeekAccountBalance `json:"balances"`
}

type DeepSeekAccountBalance struct {
	Currency      string       `json:"currency"`
	TotalCents    domain.Units `json:"totalCents"`
	GrantedCents  domain.Units `json:"grantedCents"`
	ToppedUpCents domain.Units `json:"toppedUpCents"`
}

// DeepSeekBalanceVerification performs one explicitly requested provider read
// and records its result and audit record atomically. Receipt replay never
// repeats the provider request.
type DeepSeekBalanceVerification struct {
	store     store.Store
	secrets   OwnedSecretResolver
	fetcher   DeepSeekBalanceFetcher
	clock     Clock
	flightsMu sync.Mutex
	flights   map[string]*balanceFlight
}

type balanceFlight struct{ done chan struct{} }

func NewDeepSeekBalanceVerification(st store.Store, secrets OwnedSecretResolver, fetcher DeepSeekBalanceFetcher, clock Clock) (*DeepSeekBalanceVerification, error) {
	if st == nil || secrets == nil || fetcher == nil || clock == nil {
		return nil, ErrDeepSeekBalanceInvalid
	}
	return &DeepSeekBalanceVerification{store: st, secrets: secrets, fetcher: fetcher, clock: clock, flights: make(map[string]*balanceFlight)}, nil
}

func (s *DeepSeekBalanceVerification) Observe(ctx context.Context, auth domain.AuthContext, commandID, ref, revision domain.ID) (DeepSeekBalanceObservation, error) {
	if s == nil || ctx == nil || commandID == "" || len(commandID) > 128 || ref == "" || revision == "" {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermConfigure) {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceForbidden
	}
	scope := domain.ID("deepseek-balance/" + string(auth.PrincipalID) + "/" + string(auth.Namespace))
	inputHash := hashBalanceRequest(ref, revision)
	if observation, found, err := s.receipt(ctx, scope, commandID, inputHash); err != nil || found {
		return observation, err
	}
	release, err := s.acquire(ctx, string(scope)+"/"+string(commandID))
	if err != nil {
		if errors.Is(err, ErrDeepSeekBalanceBusy) {
			return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceBusy
		}
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceUnavailable
	}
	defer release()
	if observation, found, err := s.receipt(ctx, scope, commandID, inputHash); err != nil || found {
		return observation, err
	}
	secret, err := s.secrets.ResolveOwned(ctx, auth, ref, revision)
	if err != nil {
		return DeepSeekBalanceObservation{}, mapBalanceSecretError(err)
	}
	key := secret["value"]
	for name := range secret {
		delete(secret, name)
	}
	if key == "" {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceInvalid
	}
	data, err := s.fetcher.FetchBalance(ctx, key)
	key = ""
	if err != nil {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceUnavailable
	}
	parsed, err := deepseek.ParseBalanceObservation(data, s.clock.Now())
	for i := range data {
		data[i] = 0
	}
	if err != nil {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceResponse
	}
	observation := DeepSeekBalanceObservation{IsAvailable: parsed.IsAvailable, ObservedAt: parsed.ObservedAt, Balances: make([]DeepSeekAccountBalance, len(parsed.Balances))}
	for i, balance := range parsed.Balances {
		observation.Balances[i] = DeepSeekAccountBalance{Currency: balance.Currency, TotalCents: balance.TotalCents, GrantedCents: balance.GrantedCents, ToppedUpCents: balance.ToppedUpCents}
	}
	resultJSON, err := json.Marshal(observation)
	if err != nil {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceUnavailable
	}
	var result resultSlot[DeepSeekBalanceObservation]
	err = s.store.SubmitWrite(ctx, store.WriteCommand{ID: "deepseek-balance/" + string(commandID), Run: func(tx store.WriterTx) error {
		old, found, err := tx.GetReceipt(scope, commandID)
		if err != nil {
			return err
		}
		if found {
			if old.Kind != "deepseek_balance_observe" || old.InputHash != inputHash {
				return ErrSecretConflict
			}
			var replay DeepSeekBalanceObservation
			if err := json.Unmarshal(old.ResultJSON, &replay); err != nil {
				return err
			}
			result.set(replay)
			return nil
		}
		now := s.clock.Now()
		if err := tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: commandID, Kind: "deepseek_balance_observe", InputHash: inputHash, ResultJSON: resultJSON, CreatedAt: now}); err != nil {
			return err
		}
		idHash := sha256.Sum256([]byte(string(scope) + "/" + string(commandID)))
		auditID := domain.ID("deepseek-balance-" + hex.EncodeToString(idHash[:16]))
		if err := tx.InsertAudit(store.AuditRecord{AuditID: auditID, PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: "provider.deepseek.balance.observe", TargetRef: ref, EvidenceRef: revision, CreatedAt: now}); err != nil {
			return err
		}
		result.set(observation)
		return nil
	}})
	if err != nil {
		if errors.Is(err, ErrSecretConflict) {
			return DeepSeekBalanceObservation{}, ErrSecretConflict
		}
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceUnavailable
	}
	return result.get(), nil
}

// acquire serializes concurrent retries of one caller-scoped command ID.
// After the first caller commits, waiters observe its receipt instead of
// repeating the external request.
func (s *DeepSeekBalanceVerification) acquire(ctx context.Context, key string) (func(), error) {
	for {
		s.flightsMu.Lock()
		if current, exists := s.flights[key]; exists {
			done := current.done
			s.flightsMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if len(s.flights) >= deepSeekBalanceMaxInFlight {
			s.flightsMu.Unlock()
			return nil, ErrDeepSeekBalanceBusy
		}
		current := &balanceFlight{done: make(chan struct{})}
		s.flights[key] = current
		s.flightsMu.Unlock()
		return func() {
			s.flightsMu.Lock()
			delete(s.flights, key)
			close(current.done)
			s.flightsMu.Unlock()
		}, nil
	}
}

// Get returns a previously committed observation using the idempotency key
// returned by Observe. It never contacts the provider.
func (s *DeepSeekBalanceVerification) Get(ctx context.Context, auth domain.AuthContext, commandID domain.ID) (DeepSeekBalanceObservation, error) {
	if s == nil || ctx == nil || commandID == "" || len(commandID) > 128 {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermConfigure) {
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceForbidden
	}
	scope := domain.ID("deepseek-balance/" + string(auth.PrincipalID) + "/" + string(auth.Namespace))
	var observation DeepSeekBalanceObservation
	err := s.store.Read(ctx, func(rx store.ReaderTx) error {
		receipts, ok := rx.(store.ReceiptReaderTx)
		if !ok {
			return ErrDeepSeekBalanceUnavailable
		}
		receipt, found, err := receipts.GetReceipt(scope, commandID)
		if err != nil {
			return err
		}
		if !found || receipt.Kind != "deepseek_balance_observe" {
			return ErrDeepSeekBalanceNotFound
		}
		return json.Unmarshal(receipt.ResultJSON, &observation)
	})
	if err != nil {
		if errors.Is(err, ErrDeepSeekBalanceNotFound) || errors.Is(err, ErrDeepSeekBalanceForbidden) || errors.Is(err, ErrDeepSeekBalanceInvalid) {
			return DeepSeekBalanceObservation{}, err
		}
		return DeepSeekBalanceObservation{}, ErrDeepSeekBalanceUnavailable
	}
	return observation, nil
}

func (s *DeepSeekBalanceVerification) receipt(ctx context.Context, scope, commandID domain.ID, inputHash string) (DeepSeekBalanceObservation, bool, error) {
	var observation DeepSeekBalanceObservation
	foundReceipt := false
	err := s.store.SubmitWrite(ctx, store.WriteCommand{ID: "deepseek-balance-receipt/" + string(commandID), Run: func(tx store.WriterTx) error {
		receipt, found, err := tx.GetReceipt(scope, commandID)
		if err != nil || !found {
			return err
		}
		if receipt.Kind != "deepseek_balance_observe" || receipt.InputHash != inputHash {
			return ErrSecretConflict
		}
		if err := json.Unmarshal(receipt.ResultJSON, &observation); err != nil {
			return err
		}
		foundReceipt = true
		return nil
	}})
	if errors.Is(err, ErrSecretConflict) {
		return DeepSeekBalanceObservation{}, false, ErrSecretConflict
	}
	if err != nil {
		return DeepSeekBalanceObservation{}, false, ErrDeepSeekBalanceUnavailable
	}
	return observation, foundReceipt, nil
}

func hashBalanceRequest(ref, revision domain.ID) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%d:%s", len(ref), ref, len(revision), revision)))
	return hex.EncodeToString(hash[:])
}

func mapBalanceSecretError(err error) error {
	switch {
	case errors.Is(err, ErrSecretForbidden):
		return ErrDeepSeekBalanceForbidden
	case errors.Is(err, ErrSecretInvalid):
		return ErrDeepSeekBalanceInvalid
	case errors.Is(err, ErrSecretNotFound):
		return ErrSecretNotFound
	case errors.Is(err, ErrSecretConflict):
		return ErrSecretConflict
	default:
		return ErrDeepSeekBalanceUnavailable
	}
}
