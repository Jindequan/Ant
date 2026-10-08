package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/application"
	"ant/server/internal/domain"
)

type operationEventsStub struct {
	page         application.OperationEventPage
	err          error
	gotAfter     int64
	gotNamespace domain.ID
	gotOperation domain.ID
}

func (s *operationEventsStub) Events(_ context.Context, auth domain.AuthContext, operationID domain.ID, after int64, _ int) (application.OperationEventPage, error) {
	s.gotAfter, s.gotNamespace, s.gotOperation = after, auth.Namespace, operationID
	return s.page, s.err
}

func TestOperationEventsSSEUsesPersistedSequenceAndAuth(t *testing.T) {
	env := newEnv(t, domain.PermRead)
	stub := &operationEventsStub{page: application.OperationEventPage{
		Events: []domain.OperationEvent{
			{OperationID: "op-1", Sequence: 2, Type: "operation_terminal", CreatedAt: 20, Payload: json.RawMessage(`{"state":"succeeded"}`)},
		},
		LastSequence: 2,
		State:        domain.Succeeded,
	}}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/operations/{operationId}/events", OperationEventHandler(stub, env.auth))
	server := httptest.NewServer(AuthMiddleware(env.auth, mux))
	t.Cleanup(server.Close)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/operations/op-1/events?afterSequence=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d content-type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	text := string(body)
	if !strings.Contains(text, "id: 2\nevent: operation_terminal\ndata: {\"operationId\":\"op-1\",\"sequence\":2") || stub.gotAfter != 1 || stub.gotNamespace != nsA || stub.gotOperation != "op-1" {
		t.Fatalf("SSE body=%s query after=%d namespace=%q operation=%q", text, stub.gotAfter, stub.gotNamespace, stub.gotOperation)
	}
	stub.page.Events = nil
	reconnectReq, err := http.NewRequest(http.MethodGet, server.URL+"/v1/operations/op-1/events?afterSequence=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	reconnectReq.Header.Set("Authorization", "Bearer "+env.token)
	reconnectReq.Header.Set("Last-Event-ID", "2")
	reconnected, err := server.Client().Do(reconnectReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, reconnected.Body)
	_ = reconnected.Body.Close()
	if reconnected.StatusCode != http.StatusOK || stub.gotAfter != 2 {
		t.Fatalf("reconnect status=%d after=%d", reconnected.StatusCode, stub.gotAfter)
	}

	invalidReq, err := http.NewRequest(http.MethodGet, server.URL+"/v1/operations/op-1/events?afterSequence=1&afterSequence=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	invalidReq.Header.Set("Authorization", "Bearer "+env.token)
	invalid, err := server.Client().Do(invalidReq)
	if err != nil {
		t.Fatal(err)
	}
	invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate cursor status=%d", invalid.StatusCode)
	}

	readOnly := newEnv(t, domain.PermExecute)
	deniedServer := httptest.NewServer(AuthMiddleware(readOnly.auth, mux))
	t.Cleanup(deniedServer.Close)
	deniedReq, _ := http.NewRequest(http.MethodGet, deniedServer.URL+"/v1/operations/op-1/events", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+readOnly.token)
	denied, err := deniedServer.Client().Do(deniedReq)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("execute-only event stream status=%d", denied.StatusCode)
	}
}

func TestOperationEventsQueryFailureStaysHTTPError(t *testing.T) {
	env := newEnv(t, domain.PermRead)
	stub := &operationEventsStub{err: application.ErrOperationQueryNotFound}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/operations/{operationId}/events", OperationEventHandler(stub, env.auth))
	response := env.do(t, AuthMiddleware(env.auth, mux), http.MethodGet, "/v1/operations/missing/events", env.token, "")
	if response.Code != http.StatusNotFound || strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("missing event stream status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	stub.err = errors.New("storage unavailable")
	failed := env.do(t, AuthMiddleware(env.auth, mux), http.MethodGet, "/v1/operations/missing/events", env.token, "")
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("storage failure status=%d body=%s", failed.Code, failed.Body.String())
	}
}
