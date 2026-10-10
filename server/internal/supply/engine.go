// Package supply is the supply engine of design 11: it discovers the
// credentials on this machine, decides when each is read, runs the reads,
// and records them in the supply store, which it alone writes. What a read
// returns, and how a provider is reached, belongs to the Source.
package supply

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/store"
)

// Outcomes of one collection; they match collector_runs.outcome.
const (
	OutcomeOK        = "ok"
	OutcomeSignedOut = "signed_out"
	OutcomeError     = "error"
)

// Origins of a credential; they match supply_credentials.origin.
const (
	OriginFound = "found"
	OriginAdded = "added"
)

// detailLimit caps run and status detail (design 11 §3.3, size caps).
const detailLimit = 512

// Credential is one way to read one provider account, as the engine
// schedules it. Locator says where a found secret lives; it never holds one.
type Credential struct {
	ID         domain.ID
	ProviderID domain.ID
	Kind       string // api_key | oauth | session | sts | console_token | none
	Origin     string // found | added
	Strategy   string
	Locator    []byte    // JSON
	SecretRef  domain.ID // an added credential's encrypted secret; "" for found ones
}

// Result is what one collection tells the scheduler.
type Result struct {
	Outcome     string
	Detail      string
	NextReset   *domain.Millis      // the earliest window reset the provider reported
	BalanceOnly bool                // the account holds balances and no windows
	Inventory   *observed.Inventory // what the account holds; recorded when the read is ok
	Offers      []observed.Offer    // what it serves; nil leaves the recorded offers as they are
	// Card is the read as the page shows it (design 15), kept with the
	// reading so it can be shown at start; nil keeps the last one.
	Card []byte
}

// Source finds credentials and reads them. The engine never sees secrets
// or provider replies.
type Source interface {
	Discover(ctx context.Context) ([]Credential, error)
	Collect(ctx context.Context, c Credential) Result
}

type Clock interface{ Now() domain.Millis }

// Engine schedules reads: one at a time per credential, at most
// Policy.MaxInFlight at once, each credential at its own cadence.
//
// Found credentials are recorded with where their secret lives; added ones
// with their encrypted secret reference. An added credential without one
// (only possible before its key was saved) is read but not recorded, since
// supply_credentials never holds a secret itself.
type Engine struct {
	ns     domain.ID
	st     store.Store
	src    Source
	clock  Clock
	policy Policy
	log    *slog.Logger
	sem    chan struct{}
	wake   chan struct{}
	wg     sync.WaitGroup

	mu           sync.Mutex
	slots        map[domain.ID]*slot
	order        []domain.ID
	discoveredAt domain.Millis
	discovering  chan struct{} // non-nil while discovery runs
	inFlight     int
}

type slot struct {
	cred       Credential
	createdAt  domain.Millis
	status     string // as last written to supply_credentials
	verifiedAt *domain.Millis
	accountID  domain.ID // the account the last good read resolved to
	due        domain.Millis
	failures   int
	queued     bool          // the loop has started a read that hasn't begun
	flight     chan struct{} // closed when the running read ends
}

func NewEngine(ns domain.ID, st store.Store, src Source, clock Clock, policy Policy, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Engine{ns: ns, st: st, src: src, clock: clock, policy: policy, log: log,
		sem: make(chan struct{}, max(policy.MaxInFlight, 1)), wake: make(chan struct{}, 1), slots: map[domain.ID]*slot{}}
}

// Running reports whether discovery or any read is under way.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inFlight > 0 || e.discovering != nil
}

// Discover asks the source for the current credentials, keeps each known
// one's schedule, schedules new ones now, and records found ones. Calls
// that overlap share one discovery.
func (e *Engine) Discover(ctx context.Context) error {
	e.mu.Lock()
	if ch := e.discovering; ch != nil {
		e.mu.Unlock()
		select {
		case <-ch:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ch := make(chan struct{})
	e.discovering = ch
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.discovering = nil
		e.mu.Unlock()
		close(ch)
		e.poke()
	}()

	creds, err := e.src.Discover(ctx)
	if err != nil {
		return err // keep the previous set
	}
	now := e.clock.Now()
	e.mu.Lock()
	slots := make(map[domain.ID]*slot, len(creds))
	order := make([]domain.ID, 0, len(creds))
	var found []store.SupplyCredentialRecord
	for _, c := range creds {
		if _, dup := slots[c.ID]; dup {
			continue
		}
		s, ok := e.slots[c.ID]
		if !ok {
			s = &slot{createdAt: now, status: "unverified", due: now}
		}
		s.cred = c
		slots[c.ID], order = s, append(order, c.ID)
		if recordable(c) {
			found = append(found, e.credentialRecord(s, ""))
		}
	}
	e.slots, e.order, e.discoveredAt = slots, order, now
	e.mu.Unlock()

	if len(found) > 0 {
		err = e.write(ctx, "supply-discover", func(tx store.SupplyWriterTx) error {
			for _, r := range found {
				if err := tx.UpsertSupplyCredential(r); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			e.log.Warn("supply: recording discovered credentials failed", "err", err)
		}
	}
	e.retireGone(ctx, creds)
	return nil
}

// retireGone marks every stored credential this discovery didn't find
// (removed by the user, or no longer on this Mac) as hidden and drops its
// offers: what Ant routes over never includes an account that's gone.
// Found again, discovery records it anew.
func (e *Engine) retireGone(ctx context.Context, creds []Credential) {
	current := make(map[domain.ID]bool, len(creds))
	for _, c := range creds {
		current[c.ID] = true
	}
	var gone []store.SupplyCredentialRecord
	err := e.st.Read(ctx, func(rx store.ReaderTx) error {
		sx, ok := rx.(store.SupplyReaderTx)
		if !ok {
			return errNoSupplyStore
		}
		all, err := sx.ListSupplyCredentials(e.ns)
		for _, r := range all {
			if !current[r.CredentialID] && r.Status != "hidden" {
				r.Status, r.StatusDetail = "hidden", "Removed, or no longer on this Mac"
				gone = append(gone, r)
			}
		}
		return err
	})
	if err != nil || len(gone) == 0 {
		return
	}
	err = e.write(ctx, "supply-retire", func(tx store.SupplyWriterTx) error {
		for _, r := range gone {
			if err := tx.UpsertSupplyCredential(r); err != nil {
				return err
			}
			if err := tx.ReplaceSupplyOffers(e.ns, r.CredentialID, nil); err != nil {
				return err
			}
			if err := tx.DeleteSupplyCard(e.ns, r.CredentialID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		e.log.Warn("supply: retiring credentials no longer found failed", "err", err)
	}
}

// Cards are the kept cards of the credentials still current (design 15), by
// credential id: what the page showed when Ant last read them.
func (e *Engine) Cards(ctx context.Context) ([]store.SupplyCardRecord, error) {
	var out []store.SupplyCardRecord
	err := e.st.Read(ctx, func(rx store.ReaderTx) error {
		sx, ok := rx.(store.SupplyReaderTx)
		if !ok {
			return errNoSupplyStore
		}
		creds, err := sx.ListSupplyCredentials(e.ns)
		if err != nil {
			return err
		}
		hidden := map[domain.ID]bool{}
		for _, c := range creds {
			hidden[c.CredentialID] = c.Status == "hidden"
		}
		cards, err := sx.ListSupplyCards(e.ns)
		for _, c := range cards {
			if !hidden[c.CredentialID] {
				out = append(out, c)
			}
		}
		return err
	})
	return out, err
}

// credentialRecord is s as a supply_credentials row; call with e.mu held or
// on a slot no one else touches.
func (e *Engine) credentialRecord(s *slot, detail string) store.SupplyCredentialRecord {
	return store.SupplyCredentialRecord{Namespace: e.ns, CredentialID: s.cred.ID, ProviderID: s.cred.ProviderID,
		Kind: s.cred.Kind, Origin: s.cred.Origin, Strategy: s.cred.Strategy, LocatorJSON: s.cred.Locator, SecretRef: s.cred.SecretRef,
		AccountID: s.accountID, Status: s.status, StatusDetail: clip(detail), VerifiedAt: s.verifiedAt, CreatedAt: s.createdAt}
}

// RunNow reads the given credentials (all of them, after a fresh discovery,
// when ids is empty) and returns when those reads have ended. A credential
// already being read is not read twice: the call waits for that read.
func (e *Engine) RunNow(ctx context.Context, ids []domain.ID) error {
	if len(ids) == 0 || e.anyUnknown(ids) {
		if err := e.Discover(ctx); err != nil {
			e.log.Warn("supply: discovery failed", "err", err)
		}
	}
	if len(ids) == 0 {
		e.mu.Lock()
		ids = append([]domain.ID(nil), e.order...)
		e.mu.Unlock()
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.run(ctx, id, false)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

func (e *Engine) anyUnknown(ids []domain.ID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range ids {
		if _, ok := e.slots[id]; !ok {
			return true
		}
	}
	return false
}

// run reads one credential. fromLoop runs are skipped when someone else has
// read the credential since the loop queued it.
func (e *Engine) run(ctx context.Context, id domain.ID, fromLoop bool) {
	e.mu.Lock()
	s, ok := e.slots[id]
	if !ok || (fromLoop && !s.queued) {
		e.mu.Unlock()
		return
	}
	s.queued = false
	if ch := s.flight; ch != nil {
		e.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
		}
		return
	}
	ch := make(chan struct{})
	s.flight, e.inFlight = ch, e.inFlight+1
	cred := s.cred
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		s.flight, e.inFlight = nil, e.inFlight-1
		e.mu.Unlock()
		close(ch)
		e.poke()
	}()

	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-e.sem }()

	recorded := recordable(cred)
	start := e.clock.Now()
	runID := newID("run-")
	if recorded {
		err := e.write(ctx, "supply-run-start", func(tx store.SupplyWriterTx) error {
			return tx.StartCollectorRun(store.CollectorRunRecord{Namespace: e.ns, RunID: runID, CredentialID: cred.ID, Capability: "inventory", StartedAt: start})
		})
		if err != nil {
			e.log.Warn("supply: recording run start failed", "credential", cred.ID, "err", err)
			recorded = false
		}
	}

	rctx, cancel := context.WithTimeout(ctx, e.policy.ReadTimeout)
	r := e.src.Collect(rctx, cred)
	cancel()
	if r.Outcome != OutcomeOK && r.Outcome != OutcomeSignedOut {
		r.Outcome = OutcomeError
	}
	end := max(e.clock.Now(), start)

	e.mu.Lock()
	if r.Outcome == OutcomeOK {
		s.failures, s.verifiedAt = 0, &end
	} else {
		s.failures++
	}
	s.status, s.due = r.Outcome, e.policy.NextDue(end, r, s.failures)
	rec := e.credentialRecord(s, r.Detail)
	e.mu.Unlock()

	if recorded {
		e.finish(ctx, s, cred, r, rec, runID, end)
	}
}

// finish records a run's end in one transaction: for a good read its
// inventory (reconcile), then the run's outcome and the credential's status.
// If that fails, the run is still closed, as an error that says why.
func (e *Engine) finish(ctx context.Context, s *slot, cred Credential, r Result, rec store.SupplyCredentialRecord, runID domain.ID, end domain.Millis) {
	ctx = context.WithoutCancel(ctx)
	var accountID domain.ID
	err := e.write(ctx, "supply-run-finish", func(tx store.SupplyWriterTx) error {
		detail := r.Detail
		if r.Outcome == OutcomeOK && r.Inventory != nil {
			id, skipped, err := e.reconcile(tx, cred, *r.Inventory, runID, end)
			if err != nil {
				return err
			}
			accountID, rec.AccountID = id, id
			if r.Offers != nil {
				if err := tx.ReplaceSupplyOffers(e.ns, cred.ID, e.offerRecords(cred.ID, r.Offers, end)); err != nil {
					return err
				}
			}
			if skipped > 0 {
				detail = strings.TrimSpace(detail + " " + strconv.Itoa(skipped) + " reading(s) not newer than the stored ones: skipped")
			}
		}
		if err := tx.FinishCollectorRun(e.ns, runID, end, r.Outcome, clip(detail)); err != nil {
			return err
		}
		if err := tx.UpsertSupplyCredential(rec); err != nil {
			return err
		}
		if r.Card == nil {
			return nil
		}
		return tx.PutSupplyCard(store.SupplyCardRecord{Namespace: e.ns, CredentialID: cred.ID, CardJSON: r.Card, ReadAt: end})
	})
	if err == nil {
		if accountID != "" {
			e.mu.Lock()
			s.accountID = accountID
			e.mu.Unlock()
		}
		return
	}
	e.log.Warn("supply: recording the read failed", "credential", cred.ID, "err", err)
	err = e.write(ctx, "supply-run-fail", func(tx store.SupplyWriterTx) error {
		return tx.FinishCollectorRun(e.ns, runID, end, OutcomeError, clip("the read could not be recorded: "+err.Error()))
	})
	if err != nil {
		e.log.Warn("supply: closing the run failed", "credential", cred.ID, "err", err)
	}
}

// Start reads every credential when it is due, and rediscovers every
// Policy.Rediscover, until ctx ends. Wait blocks until the loop and its
// reads have returned.
func (e *Engine) Start(ctx context.Context) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		if err := e.Discover(ctx); err != nil {
			e.log.Warn("supply: discovery failed", "err", err)
		}
		for {
			due := e.queueDue()
			for _, id := range due {
				e.wg.Add(1)
				go func() {
					defer e.wg.Done()
					e.run(ctx, id, true)
				}()
			}
			timer := time.NewTimer(e.untilNext())
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-e.wake:
			case <-timer.C:
			}
			timer.Stop()
			e.mu.Lock()
			rediscover := e.clock.Now() >= e.discoveredAt+ms(e.policy.Rediscover)
			e.mu.Unlock()
			if rediscover {
				if err := e.Discover(ctx); err != nil {
					e.log.Warn("supply: discovery failed", "err", err)
					e.mu.Lock()
					e.discoveredAt = e.clock.Now() // retry at the next interval, not in a loop
					e.mu.Unlock()
				}
			}
		}
	}()
}

func (e *Engine) Wait() { e.wg.Wait() }

// queueDue marks every idle credential that is due as queued and returns them.
func (e *Engine) queueDue() []domain.ID {
	now := e.clock.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []domain.ID
	for _, id := range e.order {
		s := e.slots[id]
		if s.flight == nil && !s.queued && s.due <= now {
			s.queued, s.due = true, math.MaxInt64
			out = append(out, id)
		}
	}
	return out
}

// untilNext is the wait until the next due read or rediscovery.
func (e *Engine) untilNext() time.Duration {
	now := e.clock.Now()
	e.mu.Lock()
	next := e.discoveredAt + ms(e.policy.Rediscover)
	for _, s := range e.slots {
		if s.flight == nil && !s.queued && s.due < next {
			next = s.due
		}
	}
	e.mu.Unlock()
	if next <= now {
		return 0
	}
	return time.Duration(next-now) * time.Millisecond
}

func (e *Engine) poke() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

var errNoSupplyStore = store.ErrUnavailable

// ErrWriteOutcomeUnknown means the writer accepted a command but the caller
// stopped waiting before it received the transaction result. The command may
// still commit, so callers must inspect durable state and must not retry it.
var ErrWriteOutcomeUnknown = errors.New("supply: write outcome unknown")

func (e *Engine) write(ctx context.Context, id string, fn func(store.SupplyWriterTx) error) error {
	return e.st.SubmitWrite(ctx, store.WriteCommand{ID: id, Run: func(tx store.WriterTx) error {
		stx, ok := tx.(store.SupplyWriterTx)
		if !ok {
			return errNoSupplyStore
		}
		return fn(stx)
	}})
}

func newID(prefix string) domain.ID {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return domain.ID(prefix + hex.EncodeToString(b))
}

func recordable(c Credential) bool {
	return c.Origin == OriginFound || c.SecretRef != "" || c.Kind == "none"
}

func clip(s string) string {
	if r := []rune(s); len(r) > detailLimit {
		return string(r[:detailLimit])
	}
	return s
}

// SetPaused pauses or resumes credentials' part in routing (design 12 §6).
// Reading them goes on; only route previews (and, later, routing) skip them.
func (e *Engine) SetPaused(ctx context.Context, ids []domain.ID, paused bool) error {
	return e.write(ctx, "supply-pause", func(tx store.SupplyWriterTx) error {
		_, err := tx.SetSupplyPaused(e.ns, ids, paused)
		return err
	})
}

// Paused is every paused credential.
func (e *Engine) Paused(ctx context.Context) (map[domain.ID]bool, error) {
	out := map[domain.ID]bool{}
	err := e.st.Read(ctx, func(rx store.ReaderTx) error {
		sx, ok := rx.(store.SupplyReaderTx)
		if !ok {
			return errNoSupplyStore
		}
		creds, err := sx.ListSupplyCredentials(e.ns)
		for _, c := range creds {
			if c.Paused {
				out[c.CredentialID] = true
			}
		}
		return err
	})
	return out, err
}
