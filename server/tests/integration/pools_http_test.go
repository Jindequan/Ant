package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"ant/server/internal/api"
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
	"ant/server/internal/security"
	"ant/server/internal/store"
)

func TestPoolHTTPActivationProjectionAndFullOrderRoundTrip(t *testing.T) {
	ctx := context.Background()
	clock := &preparationTestClock{}
	clock.SetNow(1_790_000_000_000)
	cfg := testConfig()
	cfg.Clock = clock
	st, path := openTestStore(t, cfg)
	db := openRawDB(t, path)
	execOK(t, db, `INSERT INTO principals(principal_id,display_name,enabled,created_at) VALUES('principal-1','integration',1,1)`)
	execOK(t, db, `INSERT INTO secret_records(secret_ref,revision,nonce,ciphertext,master_key_ref,mask,created_at) VALUES('secret-alpha-key','rev-1',X'00',X'00','fixture','sk-f…',1)`)
	execOK(t, db, `INSERT INTO secret_refs(secret_ref,principal_id,namespace,current_revision,mask,created_at,updated_at) VALUES('secret-alpha-key','principal-1','ns-demo','rev-1','sk-f…',1,1)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	configService, err := application.NewConfigService(st, catalog, clock)
	if err != nil {
		t.Fatal(err)
	}
	poolQueries, err := application.NewPoolQueries(st, catalog, clock)
	if err != nil {
		t.Fatal(err)
	}
	configAuth := domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-demo", Permissions: []domain.Permission{domain.PermConfigure}}
	fixture := readFixture(t, validConfigFixture)
	draft, err := configService.SaveDraft(ctx, configAuth, application.ConfigDraftInput{CommandID: "pool-config-save", DraftID: "pool-config-draft", Definition: fixture})
	if err != nil {
		t.Fatalf("save config draft: %v", err)
	}
	validated, err := configService.ValidateDraft(ctx, configAuth, draft.DraftID, draft.Version)
	if err != nil || validated.Status != "validated" {
		t.Fatalf("validate config: status=%s err=%v issues=%+v", validated.Status, err, validated.FieldIssues)
	}
	activated, err := configService.ActivateDraft(ctx, configAuth, domain.ActivationInput{CommandID: "pool-config-activate", DraftID: draft.DraftID, ExpectedConfigRevision: "", ExpectedDraftVersion: validated.Version})
	if err != nil || activated.ConfigRevision == "" {
		t.Fatalf("activate config: result=%+v err=%v", activated, err)
	}

	tokens := security.NewMemoryTokenStore()
	token := "integration-pools-token"
	if err := tokens.Put(security.HashToken(token), security.TokenRecord{PrincipalID: "principal-1", Namespace: "ns-demo", Permissions: []domain.Permission{domain.PermRead, domain.PermConfigure}, Kind: security.TokenKindAPIToken}); err != nil {
		t.Fatal(err)
	}
	handler := api.AuthMiddleware(security.NewAuthProvider(tokens), api.PoolsHandler(poolQueries, configService))
	request := func(method, path string, body []byte, ifMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	listed := request(http.MethodGet, "/v1/pools", nil, "")
	var pools []api.PoolView
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &pools) != nil || len(pools) != 1 {
		t.Fatalf("list activated pools: status=%d body=%s", listed.Code, listed.Body.String())
	}
	initial := pools[0]
	if initial.Pool.PoolID != "pool-main" || len(initial.Pool.Members) != 2 || len(initial.Groups) == 0 {
		t.Fatalf("active config was not projected into pool view: %+v", initial)
	}
	version := initial.Pool.Version
	entries := make([]domain.ReorderPoolEntry, 0, len(initial.Pool.Members))
	for _, member := range initial.Pool.Members {
		entries = append(entries, domain.ReorderPoolEntry{OfferID: member.OfferID, Priority: 7})
	}
	body, err := json.Marshal(domain.ReorderPoolInput{CommandID: "pool-http-reorder", ExpectedPoolVersion: version, ExpectedConfigRevision: activated.ConfigRevision, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	ordered := request(http.MethodPut, "/v1/pools/pool-main/order", body, `"`+strconv.FormatInt(version, 10)+`"`)
	if ordered.Code != http.StatusOK {
		t.Fatalf("write complete order: status=%d body=%s", ordered.Code, ordered.Body.String())
	}
	var updated api.PoolView
	if err := json.Unmarshal(ordered.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated pool view: %v", err)
	}
	if updated.Pool.Version <= version || len(updated.Pool.Members) != 2 {
		t.Fatalf("write did not return updated pool: %+v", updated.Pool)
	}
	for _, member := range updated.Pool.Members {
		if member.Priority != 7 {
			t.Fatalf("equal priority was rewritten: member=%+v", member)
		}
	}

	readBack := request(http.MethodGet, "/v1/pools/pool-main", nil, "")
	var detail api.PoolView
	if readBack.Code != http.StatusOK || json.Unmarshal(readBack.Body.Bytes(), &detail) != nil || detail.Pool.Version != updated.Pool.Version {
		t.Fatalf("read back updated pool: status=%d body=%s", readBack.Code, readBack.Body.String())
	}
	for _, member := range detail.Pool.Members {
		if member.Priority != 7 {
			t.Fatalf("readback priority=%d, want raw equal priority 7", member.Priority)
		}
	}

	var activeDefinition []byte
	if err := st.Read(ctx, func(rx store.ReaderTx) error {
		active, ok, err := rx.GetActiveConfig()
		if err != nil || !ok {
			return err
		}
		revision, ok, err := rx.GetConfigRevision(active.Revision)
		if err != nil || !ok {
			return err
		}
		activeDefinition = append([]byte(nil), revision.DefinitionJSON...)
		return nil
	}); err != nil {
		t.Fatalf("read active config after order: %v", err)
	}
	var activeDoc struct {
		Pools []struct {
			Members []struct {
				Priority int64 `json:"priority"`
			} `json:"members"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(activeDefinition, &activeDoc); err != nil || len(activeDoc.Pools) != 1 || len(activeDoc.Pools[0].Members) != 2 {
		t.Fatalf("active config pool shape changed: %+v err=%v", activeDoc, err)
	}
	for _, member := range activeDoc.Pools[0].Members {
		if member.Priority != 7 {
			t.Fatalf("active config priority=%d, want 7", member.Priority)
		}
	}
}
