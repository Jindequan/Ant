package quotasource

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ant/server/internal/providers/deepseek"
	"ant/server/internal/providers/zai"
	"ant/server/internal/quota"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
	"ant/server/internal/supply"
	"ant/server/internal/transport"
)

type failReadAfter struct {
	store.Store
	mu        sync.Mutex
	countdown int
}

type loseWriteReceipt struct{ store.Store }

func (s loseWriteReceipt) SubmitWrite(_ context.Context, command store.WriteCommand) error {
	if err := s.Store.SubmitWrite(context.Background(), command); err != nil {
		return err
	}
	// The transaction committed, but its receipt didn't reach the caller.
	return context.Canceled
}

func (s *failReadAfter) failOnRead(n int) {
	s.mu.Lock()
	s.countdown = n
	s.mu.Unlock()
}

func (s *failReadAfter) Read(ctx context.Context, fn func(store.ReaderTx) error) error {
	s.mu.Lock()
	if s.countdown > 0 {
		s.countdown--
		if s.countdown == 0 {
			s.mu.Unlock()
			return errors.New("injected read failure")
		}
	}
	s.mu.Unlock()
	return s.Store.Read(ctx, fn)
}

func TestNativeRouteOrderAPIIsPerModelPersistentAndPauseSafe(t *testing.T) {
	modelReply := `{"data":[{"id":"shared-model"},{"id":"other-model"}]}`
	zaiSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/coding/paas/v4/models" {
			_, _ = w.Write([]byte(modelReply))
			return
		}
		_, _ = w.Write(fixture(t, "../../providers/zai/testdata/plan.json"))
	}))
	defer zaiSrv.Close()
	dsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(modelReply))
			return
		}
		_, _ = w.Write(fixture(t, "../../providers/deepseek/testdata/balance.json"))
	}))
	defer dsSrv.Close()

	zm, dm := zai.Module(zaiSrv.URL), deepseek.SupplyModule(dsSrv.URL)
	zf, _ := transport.NewObservationFetch(zm.Supply.Origins, zaiSrv.Client().Transport)
	df, _ := transport.NewObservationFetch(dm.Supply.Origins, dsSrv.Client().Transport)
	clock, fixtureNow := previewFixtureClock()
	local := quota.Local{Home: t.TempDir(), Getenv: func(k string) string {
		return map[string]string{"ZAI_API_KEY": "key-one", "DEEPSEEK_API_KEY": "sk-test"}[k]
	}, HTTP: &http.Client{Transport: loopbackOnly{}}, Now: func() time.Time { return fixtureNow }}
	var providers []quota.Provider
	for _, p := range quota.Providers() {
		if p.ID == "zai" || p.ID == "deepseek" {
			providers = append(providers, p)
		}
	}
	accounts := quota.NewStore("")
	src := New(local, accounts, providers, Native{Module: zm, Fetch: zf}, Native{Module: dm, Fetch: df})
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: clock, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	newPage := func(st store.Store) *quota.Service {
		engine := supply.NewEngine("local", st, src, clock, supply.DefaultPolicy(), nil)
		reader := Reader{Source: src, Engine: engine}
		return quota.NewService(local, accounts, quota.Options{Reader: reader, Router: reader.Routes()})
	}
	page := newPage(st)
	snap := page.Refresh(context.Background())
	if len(snap.Accounts) != 2 {
		t.Fatalf("accounts = %d, want both fake providers: %+v", len(snap.Accounts), snap.Accounts)
	}
	var zaiID, deepseekID string
	for _, a := range snap.Accounts {
		switch a.Provider {
		case "zai":
			zaiID = a.ID
		case "deepseek":
			deepseekID = a.ID
		}
	}
	if zaiID == "" || deepseekID == "" {
		t.Fatalf("provider cards = %+v", snap.Accounts)
	}

	type response struct {
		code int
		body map[string]any
	}
	call := func(h http.Handler, method, path, body string) response {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("X-Ant", "1")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s returned invalid JSON: %s", method, path, rec.Body.String())
		}
		return response{rec.Code, out}
	}
	get := func(h http.Handler, model string) response {
		return call(h, http.MethodGet, "/api/route?model="+model, "")
	}
	order := func(h http.Handler, model string, ids []string, version int) response {
		b, _ := json.Marshal(map[string]any{"model": model, "offerIds": ids, "expectedVersion": version})
		return call(h, http.MethodPost, "/api/route/order", string(b))
	}
	use := func(t *testing.T, r response) []map[string]any {
		t.Helper()
		if r.code != http.StatusOK {
			t.Fatalf("route status=%d response=%v", r.code, r.body)
		}
		payload := r.body
		if nested, ok := r.body["preview"].(map[string]any); ok {
			payload = nested
		}
		raw, ok := payload["use"].([]any)
		if !ok {
			t.Fatalf("route use = %v", payload["use"])
		}
		choices := make([]map[string]any, len(raw))
		for i, x := range raw {
			choices[i] = x.(map[string]any)
		}
		return choices
	}

	h := page.Handler()
	initial := use(t, get(h, "shared-model"))
	if len(initial) != 2 || initial[0]["provider"] != "zai" || initial[1]["provider"] != "deepseek" {
		t.Fatalf("default plan/prepaid ranking = %v", initial)
	}
	zaiOffer, deepseekOffer := initial[0]["offerId"].(string), initial[1]["offerId"].(string)
	if zaiOffer == "" || deepseekOffer == "" {
		t.Fatalf("route offers lack IDs: %v", initial)
	}
	other := use(t, get(h, "other-model"))
	if len(other) != 2 || get(h, "other-model").body["orderVersion"] != float64(0) {
		t.Fatalf("route order leaked to other model: %v", other)
	}

	saved := order(h, "shared-model", []string{deepseekOffer, zaiOffer}, 0)
	savedUse := use(t, saved)
	if saved.body["orderVersion"] != float64(1) || saved.body["manualOrder"] != true || savedUse[0]["provider"] != "deepseek" {
		t.Fatalf("manual order wasn't applied: %v", saved.body)
	}
	getAfterSave := get(h, "shared-model")
	savedIDs, ok := getAfterSave.body["savedOfferIds"].([]any)
	if !ok || len(savedIDs) != 2 || savedIDs[0] != deepseekOffer || savedIDs[1] != zaiOffer {
		t.Fatalf("route GET didn't expose the persisted preference: %v", getAfterSave.body)
	}

	// A second Store/Engine over the same SQLite file models a process restart.
	st2, err := sqlite.Open(path, sqlite.Config{Clock: clock, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st2.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st2); err != nil {
		t.Fatal(err)
	}
	restarted := newPage(st2).Handler()
	restartUse := use(t, get(restarted, "shared-model"))
	if restartUse[0]["provider"] != "deepseek" || get(restarted, "shared-model").body["orderVersion"] != float64(1) {
		t.Fatalf("restart lost route order: %v", restartUse)
	}

	// Two changes based on one revision cannot silently overwrite each other.
	var wg sync.WaitGroup
	results := make(chan response, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- order(restarted, "shared-model", []string{deepseekOffer, zaiOffer}, 1)
		}()
	}
	wg.Wait()
	close(results)
	got200, got409 := 0, 0
	for r := range results {
		switch r.code {
		case http.StatusOK:
			got200++
		case http.StatusConflict:
			got409++
		default:
			t.Fatalf("concurrent route order status=%d body=%v", r.code, r.body)
		}
	}
	if got200 != 1 || got409 != 1 {
		t.Fatalf("concurrent route order successes=%d conflicts=%d", got200, got409)
	}

	if r := call(restarted, http.MethodPost, "/api/pause", `{"id":"`+deepseekID+`","paused":true}`); r.code != http.StatusOK {
		t.Fatalf("pause deepseek: %d %v", r.code, r.body)
	}
	paused := use(t, get(restarted, "shared-model"))
	if len(paused) != 1 || paused[0]["provider"] != "zai" {
		t.Fatalf("paused provider remained eligible: %v", paused)
	}
	pausedState := get(restarted, "shared-model")
	pausedSaved, ok := pausedState.body["savedOfferIds"].([]any)
	if !ok || len(pausedSaved) != 2 || pausedSaved[0] != deepseekOffer || pausedSaved[1] != zaiOffer {
		t.Fatalf("candidate change hid the persisted order preference: %v", pausedState.body)
	}
	if r := order(restarted, "shared-model", []string{deepseekOffer, zaiOffer}, 2); r.code != http.StatusConflict || r.body["outcome"] != "not_committed" {
		t.Fatalf("order containing paused offer should conflict, got %d: %v", r.code, r.body)
	}
	if r := call(restarted, http.MethodPost, "/api/pause", `{"id":"`+deepseekID+`","paused":false}`); r.code != http.StatusOK {
		t.Fatalf("resume deepseek: %d %v", r.code, r.body)
	}
	resumed := use(t, get(restarted, "shared-model"))
	if len(resumed) != 2 || resumed[0]["provider"] != "deepseek" || get(restarted, "shared-model").body["orderVersion"] != float64(2) {
		t.Fatalf("resume lost saved order: %v", resumed)
	}
	reset := order(restarted, "shared-model", []string{}, 2)
	resetUse := use(t, reset)
	if reset.body["orderVersion"] != float64(3) || reset.body["manualOrder"] != false || resetUse[0]["provider"] != "zai" {
		t.Fatalf("reset did not restore Ant's default order: %v", reset.body)
	}
	resetState := get(restarted, "shared-model")
	resetIDs, ok := resetState.body["savedOfferIds"].([]any)
	if !ok || len(resetIDs) != 0 {
		t.Fatalf("reset preference should be an empty persisted order: %v", resetState.body)
	}

	// A committed preference remains a committed result even when the following
	// route-preview read fails in SQLite. The caller can refresh through GET.
	faulty := &failReadAfter{Store: st2}
	faultPage := newPage(faulty).Handler()
	current := use(t, get(faultPage, "shared-model"))
	version := int(get(faultPage, "shared-model").body["orderVersion"].(float64))
	desired := []string{current[1]["offerId"].(string), current[0]["offerId"].(string)}
	faulty.failOnRead(2) // reorder preflight succeeds; post-commit preview read fails
	committed := order(faultPage, "shared-model", desired, version)
	if committed.code != http.StatusOK || committed.body["outcome"] != "committed" || committed.body["preview"] != nil {
		t.Fatalf("post-commit preview failure misreported save outcome: %d %v", committed.code, committed.body)
	}
	confirmed := use(t, get(faultPage, "shared-model"))
	if confirmed[0]["offerId"] != desired[0] || confirmed[1]["offerId"] != desired[1] {
		t.Fatalf("GET did not confirm committed order %v: %v", desired, confirmed)
	}

	// A lost writer receipt must be reported as unknown. A following GET can
	// confirm it without issuing another write.
	lostPage := newPage(loseWriteReceipt{Store: st2}).Handler()
	beforeLost := get(lostPage, "shared-model")
	lostUse := use(t, beforeLost)
	lostVersion := int(beforeLost.body["orderVersion"].(float64))
	lostDesired := []string{lostUse[1]["offerId"].(string), lostUse[0]["offerId"].(string)}
	unknown := order(lostPage, "shared-model", lostDesired, lostVersion)
	if unknown.code != http.StatusOK || unknown.body["outcome"] != "unknown" {
		t.Fatalf("lost writer receipt wasn't reported as unknown: %d %v", unknown.code, unknown.body)
	}
	afterLost := use(t, get(lostPage, "shared-model"))
	if afterLost[0]["offerId"] != lostDesired[0] || afterLost[1]["offerId"] != lostDesired[1] {
		t.Fatalf("GET didn't confirm order after unknown result %v: %v", lostDesired, afterLost)
	}
}
