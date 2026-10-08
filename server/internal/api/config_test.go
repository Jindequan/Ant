package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/security"
)

type configCommandsStub struct {
	saved     []application.ConfigDraftInput
	got       domain.ConfigDraft
	validated int
	activated int
}

func (s *configCommandsStub) Read(context.Context, domain.AuthContext) (domain.ConfigDraft, error) {
	return s.got, nil
}
func (s *configCommandsStub) SaveDraft(_ context.Context, _ domain.AuthContext, in application.ConfigDraftInput) (domain.ConfigDraft, error) {
	s.saved = append(s.saved, in)
	return domain.ConfigDraft{DraftID: in.DraftID, Version: in.ExpectedVersion + 1, Status: "editing", Definition: in.Definition, FieldIssues: []domain.FieldIssue{}}, nil
}
func (s *configCommandsStub) GetDraft(context.Context, domain.AuthContext, domain.ID) (domain.ConfigDraft, error) {
	return s.got, nil
}
func (s *configCommandsStub) ValidateDraft(context.Context, domain.AuthContext, domain.ID, int64) (domain.ConfigDraft, error) {
	s.validated++
	return s.got, nil
}
func (s *configCommandsStub) ActivateDraft(context.Context, domain.AuthContext, domain.ActivationInput) (domain.ActivationResult, error) {
	s.activated++
	return domain.ActivationResult{ConfigRevision: "rev-1", ControlVersion: 2}, nil
}

func newConfigHandlerForTest(t *testing.T, commands ConfigCommands) (http.Handler, string) {
	t.Helper()
	tokens := security.NewMemoryTokenStore()
	const bearer = "config-handler-test-token"
	if err := tokens.Put(security.HashToken(bearer), security.TokenRecord{
		PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermConfigure}, Kind: security.TokenKindAPIToken,
	}); err != nil {
		t.Fatal(err)
	}
	return AuthMiddleware(security.NewAuthProvider(tokens), ConfigHandler(commands)), bearer
}

func TestConfigHandlerCreateUsesStrictAuthenticatedInput(t *testing.T) {
	stub := &configCommandsStub{}
	handler, bearer := newConfigHandlerForTest(t, stub)
	body := `{"commandId":"cmd-1","draftId":"draft-1","definition":{"version":"1"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || len(stub.saved) != 1 {
		t.Fatalf("create response=%d saves=%d body=%s", rec.Code, len(stub.saved), rec.Body.String())
	}
	if stub.saved[0].CommandID != "cmd-1" || stub.saved[0].DraftID != "draft-1" {
		t.Fatalf("wire identity not preserved: %+v", stub.saved[0])
	}
	var draft domain.ConfigDraft
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil || draft.DraftID != "draft-1" {
		t.Fatalf("invalid draft projection: %+v err=%v", draft, err)
	}

	bad := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", strings.NewReader(`{"commandId":"cmd-2","draftId":"draft-2","namespace":"ns-other","definition":{}}`))
	bad.Header.Set("Authorization", "Bearer "+bearer)
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusBadRequest || len(stub.saved) != 1 {
		t.Fatalf("unknown/self-authorizing field was not rejected: status=%d saves=%d", badRec.Code, len(stub.saved))
	}
}

func TestConfigHandlerRejectsWeakIfMatchBeforeUpdate(t *testing.T) {
	stub := &configCommandsStub{got: domain.ConfigDraft{DraftID: "draft-1", Version: 1}}
	handler, bearer := newConfigHandlerForTest(t, stub)
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/drafts/draft-1", strings.NewReader(`{"commandId":"cmd-1","expectedDraftVersion":1,"definition":{}}`))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("If-Match", `W/"1"`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionFailed || len(stub.saved) != 0 {
		t.Fatalf("weak precondition must fail before mutation: status=%d saves=%d", rec.Code, len(stub.saved))
	}
}

func TestConfigHandlerActivationPathMustMatchBody(t *testing.T) {
	stub := &configCommandsStub{}
	handler, bearer := newConfigHandlerForTest(t, stub)
	req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts/path-id/activate", strings.NewReader(`{"commandId":"cmd-1","draftId":"body-id","expectedConfigRevision":"","expectedDraftVersion":1}`))
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || stub.activated != 0 {
		t.Fatalf("mismatched activation IDs must be rejected: status=%d activations=%d", rec.Code, stub.activated)
	}
}

func TestConfigHandlerReadUsesActiveConfigProjection(t *testing.T) {
	stub := &configCommandsStub{got: domain.ConfigDraft{BaseRevision: "revision-1", Version: 4, Definition: json.RawMessage(`{"version":"1"}`)}}
	handler, bearer := newConfigHandlerForTest(t, stub)
	req := httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var projection ConfigProjection
	if err := json.Unmarshal(rec.Body.Bytes(), &projection); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("active config response: status=%d body=%s err=%v", rec.Code, rec.Body.String(), err)
	}
	if projection.Revision != "revision-1" || projection.Version != 4 || string(projection.Document) != `{"version":"1"}` {
		t.Fatalf("wrong active-config wire projection: %+v", projection)
	}
}

func TestConfigValidateRequiresCurrentStrongVersion(t *testing.T) {
	stub := &configCommandsStub{got: domain.ConfigDraft{DraftID: "draft-1", Version: 3}}
	handler, bearer := newConfigHandlerForTest(t, stub)
	for _, tc := range []struct {
		name   string
		etag   string
		status int
		calls  int
	}{
		{name: "matching strong tag", etag: `"3"`, status: http.StatusOK, calls: 1},
		{name: "weak tag", etag: `W/"3"`, status: http.StatusPreconditionFailed, calls: 0},
		{name: "stale tag", etag: `"2"`, status: http.StatusPreconditionFailed, calls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub.validated = 0
			req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts/draft-1/validate", nil)
			req.Header.Set("Authorization", "Bearer "+bearer)
			req.Header.Set("If-Match", tc.etag)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status || stub.validated != tc.calls {
				t.Fatalf("status=%d validate calls=%d; want status=%d calls=%d", rec.Code, stub.validated, tc.status, tc.calls)
			}
		})
	}
}
