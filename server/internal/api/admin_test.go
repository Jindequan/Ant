package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/security"
)

type adminCommandStub struct {
	observation domain.ObservationEvidence
	commandID   domain.ID
	adjustment  domain.AdjustmentInput
	clear       domain.ClearHoldInput
}

func (s *adminCommandStub) RefreshObservation(_ context.Context, _ domain.AuthContext, commandID domain.ID, evidence domain.ObservationEvidence) (domain.RefreshResult, error) {
	s.commandID, s.observation = commandID, evidence
	return domain.RefreshResult{Status: "stored", Reasons: []domain.Reason{}}, nil
}
func (s *adminCommandStub) Adjust(_ context.Context, _ domain.AuthContext, input domain.AdjustmentInput) (domain.CommandAck, error) {
	s.adjustment = input
	return domain.CommandAck{CommandID: input.CommandID, Status: "applied"}, nil
}
func (s *adminCommandStub) ClearHold(_ context.Context, _ domain.AuthContext, input domain.ClearHoldInput) (domain.CommandAck, error) {
	s.clear = input
	return domain.CommandAck{CommandID: input.CommandID, Status: "applied"}, nil
}

func newAdminHandlerForTest(t *testing.T, permissions []domain.Permission, commands AdminCommands) (http.Handler, string) {
	t.Helper()
	tokens := security.NewMemoryTokenStore()
	const bearer = "admin-handler-test-token"
	if err := tokens.Put(security.HashToken(bearer), security.TokenRecord{
		PrincipalID: "operator", Namespace: "ns-1", Permissions: permissions, Kind: security.TokenKindAPIToken,
	}); err != nil {
		t.Fatal(err)
	}
	return AuthMiddleware(security.NewAuthProvider(tokens), AdminHandler(commands)), bearer
}

func TestAdminHandlerBindsPathAndEvidenceAndRequiresManage(t *testing.T) {
	stub := &adminCommandStub{}
	handler, bearer := newAdminHandlerForTest(t, []domain.Permission{domain.PermManage}, stub)
	req := httptest.NewRequest(http.MethodPost, "/v1/resources/resource-1/observations", strings.NewReader(`{"commandId":"observe-1","evidence":{"sourceRef":"provider-snapshot","observedAtMillis":100,"expiresAtMillis":500,"remaining":"7","committedAtStart":"2","adjustmentVersionAtStart":3}}`))
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || stub.commandID != "observe-1" || stub.observation.ResourceID != "resource-1" || stub.observation.Remaining != 7 || stub.observation.CommittedAtStart != 2 || stub.observation.AdjustmentVersionAtStart != 3 {
		t.Fatalf("observation route status=%d input=%+v command=%s body=%s", rec.Code, stub.observation, stub.commandID, rec.Body.String())
	}

	limited, limitedBearer := newAdminHandlerForTest(t, []domain.Permission{domain.PermRead}, stub)
	denied := httptest.NewRequest(http.MethodPost, "/v1/resources/resource-1/adjustments", strings.NewReader(`{"commandId":"adjust-1","windowKey":"w","delta":"1","evidenceRefs":["invoice"]}`))
	denied.Header.Set("Authorization", "Bearer "+limitedBearer)
	deniedRec := httptest.NewRecorder()
	limited.ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("read-only principal admin status=%d body=%s", deniedRec.Code, deniedRec.Body.String())
	}
}

func TestAdminHandlerAdjustmentAndClearHoldUsePathIdentity(t *testing.T) {
	stub := &adminCommandStub{}
	handler, bearer := newAdminHandlerForTest(t, []domain.Permission{domain.PermManage}, stub)
	adjust := httptest.NewRequest(http.MethodPost, "/v1/resources/resource-2/adjustments", strings.NewReader(`{"commandId":"adjust-2","windowKey":"window-1","delta":"-1","originalEventId":"event-1","evidenceRefs":["invoice-1"]}`))
	adjust.Header.Set("Authorization", "Bearer "+bearer)
	adjustRec := httptest.NewRecorder()
	handler.ServeHTTP(adjustRec, adjust)
	if adjustRec.Code != http.StatusOK || stub.adjustment.ResourceID != "resource-2" || stub.adjustment.Delta != -1 || stub.adjustment.OriginalEventID != "event-1" {
		t.Fatalf("adjust status=%d input=%+v body=%s", adjustRec.Code, stub.adjustment, adjustRec.Body.String())
	}
	clear := httptest.NewRequest(http.MethodPost, "/v1/holds/hold-3/clear", strings.NewReader(`{"commandId":"clear-3","evidenceIds":["review-1"]}`))
	clear.Header.Set("Authorization", "Bearer "+bearer)
	clearRec := httptest.NewRecorder()
	handler.ServeHTTP(clearRec, clear)
	if clearRec.Code != http.StatusOK || stub.clear.HoldID != "hold-3" || stub.clear.CommandID != "clear-3" || len(stub.clear.EvidenceIDs) != 1 {
		t.Fatalf("clear status=%d input=%+v body=%s", clearRec.Code, stub.clear, clearRec.Body.String())
	}
}
