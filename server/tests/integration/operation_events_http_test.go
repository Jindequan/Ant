package integration

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/api"
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/security"
)

func TestOperationEventsSSEReadsCommittedSQLiteRowsAndResumesCursor(t *testing.T) {
	f := preparationFixture(t, 2)
	db := openRawDB(t, f.path)
	seedOperation(t, db, "ns-1", "sse-op", "pool-1")
	execOK(t, db, `INSERT INTO operation_events(namespace,operation_id,sequence,event_type,payload_json,created_at) VALUES('ns-1','sse-op',1,'attempt_prepared','{"attemptId":"a1"}',20)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	queries, err := application.NewOperationQueries(f.st)
	if err != nil {
		t.Fatal(err)
	}
	tokens := security.NewMemoryTokenStore()
	token := "integration-sse-token"
	if err := tokens.Put(security.HashToken(token), security.TokenRecord{
		PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermRead}, Kind: security.TokenKindAPIToken,
	}); err != nil {
		t.Fatal(err)
	}
	authProvider := security.NewAuthProvider(tokens)
	mux := http.NewServeMux()
	mux.Handle("GET /v1/operations/{operationId}/events", api.OperationEventHandler(queries, authProvider))
	server := httptest.NewServer(api.AuthMiddleware(authProvider, mux))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/operations/sse-op/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("SSE status=%d body=%s", response.StatusCode, body)
	}
	reader := bufio.NewReader(response.Body)
	var streamed strings.Builder
	for !strings.Contains(streamed.String(), "id: 1\nevent: attempt_prepared") {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read initial persisted event: %v; body=%q", err, streamed.String())
		}
		streamed.WriteString(line)
	}

	// Commit an event after the stream has emitted its first database snapshot.
	db = openRawDB(t, f.path)
	execOK(t, db, `INSERT INTO operation_events(namespace,operation_id,sequence,event_type,payload_json,created_at) VALUES('ns-1','sse-op',2,'operation_terminal','{"state":"failed"}',21)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	terminal, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read terminal SSE event: %v", err)
	}
	streamed.Write(terminal)
	if !strings.Contains(streamed.String(), "id: 2\nevent: operation_terminal") {
		t.Fatalf("SSE stream body=%q", streamed.String())
	}

	reconnectRequest, err := http.NewRequest(http.MethodGet, server.URL+"/v1/operations/sse-op/events?afterSequence=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	reconnectRequest.Header.Set("Authorization", "Bearer "+token)
	reconnect, err := http.DefaultClient.Do(reconnectRequest)
	if err != nil {
		t.Fatalf("reconnect SSE: %v", err)
	}
	defer reconnect.Body.Close()
	reconnectBody, err := io.ReadAll(reconnect.Body)
	if err != nil {
		t.Fatalf("read SSE reconnect: %v", err)
	}
	if reconnect.StatusCode != http.StatusOK || !strings.Contains(string(reconnectBody), "id: 2\nevent: operation_terminal") || strings.Contains(string(reconnectBody), "id: 1\nevent:") {
		t.Fatalf("SSE reconnect status=%d body=%q", reconnect.StatusCode, reconnectBody)
	}
	if response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("unexpected SSE content type %q", response.Header.Get("Content-Type"))
	}
}
