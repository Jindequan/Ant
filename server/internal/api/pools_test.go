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
)

type poolQueriesStub struct {
	view application.PoolProjection
	gets int
	list int
	err  error
}

func (s *poolQueriesStub) Get(context.Context, domain.AuthContext, domain.ID) (application.PoolProjection, error) {
	s.gets++
	return s.view, s.err
}
func (s *poolQueriesStub) List(context.Context, domain.AuthContext) ([]application.PoolProjection, error) {
	s.list++
	return []application.PoolProjection{s.view}, s.err
}

type poolOrderStub struct {
	called bool
	auth   domain.AuthContext
	poolID domain.ID
	input  domain.ReorderPoolInput
	err    error
}

func (s *poolOrderStub) ReorderPool(_ context.Context, auth domain.AuthContext, poolID domain.ID, input domain.ReorderPoolInput) (domain.ResourcePool, error) {
	s.called, s.auth, s.poolID, s.input = true, auth, poolID, input
	return domain.ResourcePool{PoolID: poolID, Version: input.ExpectedPoolVersion + 1}, s.err
}

func TestPoolsHandlerListsReadsAndReordersWithVersionBinding(t *testing.T) {
	env := newEnv(t, domain.PermRead, domain.PermConfigure)
	projection := application.PoolProjection{
		Pool: domain.ResourcePool{PoolID: "pool-1", Namespace: nsA, Version: 4, Members: []domain.PoolMember{{OfferID: "offer-1", Priority: 9, Enabled: true, Version: 1}}},
		Groups: []application.PoolGroupProjection{{
			Domain: "ai/text", CapabilityProfile: "chat",
			Members: []application.PoolMemberProjection{{
				Member: domain.PoolMember{OfferID: "offer-1", Priority: 9, Enabled: true, Version: 1},
				Offer:  domain.ResourceOffer{OfferID: "offer-1", Domain: "ai/text", CapabilityProfile: "chat", Enabled: true},
				Status: "blocked", Reasons: []domain.Reason{{Group: "resource", Code: "resource_state_unavailable"}},
				Resources: []application.PoolResourceProjection{{ResourceID: "res-1", WindowKey: "window-1", MetricID: "requests", Unit: "request", Committed: 17, AdjustmentVersion: 4}},
			}},
		}},
	}
	queries := &poolQueriesStub{view: projection}
	orders := &poolOrderStub{}
	handler := AuthMiddleware(env.auth, PoolsHandler(queries, orders))

	listed := env.do(t, handler, http.MethodGet, "/v1/pools", env.token, "")
	if listed.Code != http.StatusOK || queries.list != 1 {
		t.Fatalf("pool list status=%d list calls=%d body=%s", listed.Code, queries.list, listed.Body.String())
	}
	var list []PoolView
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Pool.PoolID != "pool-1" || list[0].Groups[0].Members[0].Status != "blocked" || list[0].Groups[0].Members[0].Resources[0].Committed != 17 || list[0].Groups[0].Members[0].Resources[0].AdjustmentVersion != 4 {
		t.Fatalf("pool list projection=%+v err=%v", list, err)
	}
	got := env.do(t, handler, http.MethodGet, "/v1/pools/pool-1", env.token, "")
	if got.Code != http.StatusOK || queries.gets != 1 {
		t.Fatalf("pool detail status=%d gets=%d body=%s", got.Code, queries.gets, got.Body.String())
	}

	body := `{"commandId":"order-1","expectedPoolVersion":4,"expectedConfigRevision":"revision-1","entries":[{"offerId":"offer-1","priority":3}]}`
	weak := env.do(t, handler, http.MethodPut, "/v1/pools/pool-1/order", env.token, body)
	if weak.Code != http.StatusPreconditionFailed || orders.called {
		t.Fatalf("write without matching If-Match status=%d called=%v", weak.Code, orders.called)
	}
	valid := poolsRequest(t, env, handler, http.MethodPut, "/v1/pools/pool-1/order", body, `"4"`)
	if valid.Code != http.StatusOK || !orders.called || orders.poolID != "pool-1" || orders.auth.Namespace != nsA || orders.input.CommandID != "order-1" || orders.input.Entries[0].Priority != 3 {
		t.Fatalf("order write status=%d command=%+v body=%s", valid.Code, orders, valid.Body.String())
	}
	var updated PoolView
	if err := json.Unmarshal(valid.Body.Bytes(), &updated); err != nil || updated.Pool.PoolID != "pool-1" {
		t.Fatalf("order response is not a PoolView: %+v err=%v", updated, err)
	}

	readOnly := newEnv(t, domain.PermRead)
	denied := poolsRequest(t, readOnly, AuthMiddleware(readOnly.auth, PoolsHandler(queries, orders)), http.MethodPut, "/v1/pools/pool-1/order", body, `"4"`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only reorder status=%d body=%s", denied.Code, denied.Body.String())
	}
	unknown := env.do(t, handler, http.MethodPut, "/v1/pools/pool-1/order", env.token, `{"commandId":"x","expectedPoolVersion":4,"expectedConfigRevision":"r","entries":[],"namespace":"other"}`)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown/self-authorizing field status=%d", unknown.Code)
	}
}

func TestPoolsHandlerMountsOperationSubmitAtPoolRoute(t *testing.T) {
	env := newEnv(t, domain.PermExecute)
	commands := &operationSubmitStub{reply: domain.SubmitReply{
		Kind: "accepted", State: domain.Running, OperationID: "op-in-pool-route",
		OperationURL: "/v1/operations/op-in-pool-route", EventsURL: "/v1/operations/op-in-pool-route/events",
	}}
	handler := AuthMiddleware(env.auth, PoolsHandler(nil, nil, commands))
	body := `{"operationId":"op-in-pool-route","poolId":"pool-1","request":{"domain":"ai/text","capabilityProfile":"chat","payload":{"prompt":"hello"}},"deadlineAt":9999999999999,"maxAttempts":2,"allowMetered":false}`
	response := env.do(t, handler, http.MethodPost, "/v1/pools/pool-1/operations", env.token, body)
	if response.Code != http.StatusAccepted || !commands.called || commands.input.PoolID != "pool-1" || commands.auth.Namespace != nsA {
		t.Fatalf("pool submit status=%d called=%v input=%+v auth=%+v body=%s", response.Code, commands.called, commands.input, commands.auth, response.Body.String())
	}
}

func poolsRequest(t *testing.T, env *env, handler http.Handler, method, path, body, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+env.token)
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
