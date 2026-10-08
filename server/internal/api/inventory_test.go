package api

import (
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/domain/supply"
	"ant/server/internal/security"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryHTTPStrictInputsPermissionsAndReadback(t *testing.T) {
	clock := &fixedSessionClock{now: 1790000000000}
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "api.db"), sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	if err = sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	svc, _ := application.NewInventoryService(st, clock)
	tokens := security.NewMemoryTokenStore()
	for _, p := range []struct {
		token      string
		ns         domain.ID
		permission domain.Permission
	}{{"admin-test", "one", domain.PermConfigure}, {"reader-test", "one", domain.PermRead}, {"other-test", "two", domain.PermRead}} {
		if err = tokens.Put(security.HashToken(p.token), security.TokenRecord{PrincipalID: "test", Namespace: p.ns, Permissions: []domain.Permission{p.permission}, Kind: security.TokenKindAPIToken}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.SubmitWrite(context.Background(), store.WriteCommand{ID: "seed", Run: func(tx store.WriterTx) error {
		return tx.(store.AuthWriterTx).InsertPrincipal("test", "Test", clock.Now())
	}})
	handler := AuthMiddleware(security.NewAuthProvider(tokens), InventoryHandler(svc))
	request := func(method, token, body, match string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/inventory", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body, _ := json.Marshal(application.InventoryInput{CommandID: "first", Document: supply.Empty()})
	cases := []struct {
		name, token, body, match string
		status                   int
	}{
		{"reader write", "reader-test", string(body), `"0"`, 403},
		{"missing strong version", "admin-test", string(body), "", 412},
		{"unknown root", "admin-test", strings.Replace(string(body), `"commandId"`, `"namespace":"other","commandId"`, 1), `"0"`, 400},
		{"duplicate key", "admin-test", strings.Replace(string(body), `"commandId"`, `"commandId":"duplicate","commandId"`, 1), `"0"`, 400},
		{"missing body version", "admin-test", `{"commandId":"x","document":{"schemaVersion":1,"accounts":[],"packages":[],"resources":[],"pools":[]}}`, `"0"`, 400},
		{"secret-shaped field", "admin-test", strings.Replace(string(body), `"schemaVersion":1`, `"schemaVersion":1,"apiKey":"not-a-key"`, 1), `"0"`, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := request(http.MethodPut, c.token, c.body, c.match)
			if w.Code != c.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	w := request(http.MethodPut, "admin-test", string(body), `"0"`)
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
	w = request(http.MethodGet, "reader-test", "", "")
	var view application.InventoryView
	if err = json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || view.Version != 1 {
		t.Fatalf("reader read: %d %s", w.Code, w.Body.String())
	}
	w = request(http.MethodGet, "other-test", "", "")
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if view.Version != 0 || len(view.Document.Packages) != 0 {
		t.Fatal("cross namespace leak")
	}
}
