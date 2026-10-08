package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ant/server/internal/application"
	"ant/server/internal/domain"
)

type restoreGuardStub struct {
	state  application.RestoreGuardState
	input  application.RestoreGuardInput
	auth   domain.AuthContext
	sets   int
	getErr error
	setErr error
}

func (s *restoreGuardStub) Get(_ context.Context, auth domain.AuthContext) (application.RestoreGuardState, error) {
	s.auth = auth
	return s.state, s.getErr
}

func (s *restoreGuardStub) Set(_ context.Context, auth domain.AuthContext, input application.RestoreGuardInput) (application.RestoreGuardState, error) {
	s.auth, s.input = auth, input
	s.sets++
	return s.state, s.setErr
}

func TestRestoreGuardHTTPRequiresManageAndPassesAuthenticatedIdentity(t *testing.T) {
	stub := &restoreGuardStub{state: application.RestoreGuardState{Enabled: true, ControlVersion: 8}}
	manage := newEnv(t, domain.PermManage)
	h := AuthMiddleware(manage.auth, RestoreGuardHandler(stub))
	got := manage.do(t, h, http.MethodGet, "/v1/runtime/restore-guard", manage.token, "")
	var state application.RestoreGuardState
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &state) != nil || state != stub.state {
		t.Fatalf("GET status=%d body=%s state=%+v", got.Code, got.Body.String(), state)
	}
	if stub.auth.PrincipalID != "admin-1" || stub.auth.Namespace != nsA {
		t.Fatalf("handler did not pass server identity: %+v", stub.auth)
	}

	readOnly := newEnv(t, domain.PermRead)
	denied := readOnly.do(t, AuthMiddleware(readOnly.auth, RestoreGuardHandler(stub)), http.MethodGet, "/v1/runtime/restore-guard", readOnly.token, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only GET status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestRestoreGuardHTTPStrictlyDecodesAndPassesCommand(t *testing.T) {
	stub := &restoreGuardStub{state: application.RestoreGuardState{Enabled: true, ControlVersion: 9}}
	e := newEnv(t, domain.PermManage)
	h := AuthMiddleware(e.auth, RestoreGuardHandler(stub))
	body := `{"commandId":"cmd-1","expectedControlVersion":8,"enabled":true,"evidenceRef":"evidence-1"}`
	got := e.do(t, h, http.MethodPut, "/v1/runtime/restore-guard", e.token, body)
	if got.Code != http.StatusOK || stub.sets != 1 || stub.input.CommandID != "cmd-1" || !stub.input.Enabled || stub.input.ExpectedControlVersion != 8 || stub.input.EvidenceRef != "evidence-1" {
		t.Fatalf("PUT status=%d sets=%d input=%+v body=%s", got.Code, stub.sets, stub.input, got.Body.String())
	}

	bad := e.do(t, h, http.MethodPut, "/v1/runtime/restore-guard", e.token, body[:len(body)-1]+`,"namespace":"other"}`)
	if bad.Code != http.StatusBadRequest || stub.sets != 1 {
		t.Fatalf("unknown request field status=%d sets=%d body=%s", bad.Code, stub.sets, bad.Body.String())
	}

	conflict := &restoreGuardStub{setErr: application.ErrRestoreGuardConflict}
	conflictResponse := e.do(t, AuthMiddleware(e.auth, RestoreGuardHandler(conflict)), http.MethodPut, "/v1/runtime/restore-guard", e.token, body)
	if conflictResponse.Code != http.StatusConflict || !strings.Contains(conflictResponse.Body.String(), `"messageKey":"ant.error.conflict"`) {
		t.Fatalf("conflict status=%d body=%s", conflictResponse.Code, conflictResponse.Body.String())
	}
}
