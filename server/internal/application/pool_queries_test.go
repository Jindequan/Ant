package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"ant/server/internal/config"
	"ant/server/internal/domain"
)

func TestPoolQueriesUseAuthorizedConsistentProjection(t *testing.T) {
	svc, clock, auth, _ := newServiceFixtureWithDBPath(t)
	ctx := context.Background()
	queries, err := NewPoolQueries(svc.store, svc.catalog, clock)
	if err != nil {
		t.Fatal(err)
	}
	reader := domain.AuthContext{PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Permissions: []domain.Permission{domain.PermRead}}
	empty, err := queries.List(ctx, reader)
	if err != nil || len(empty) != 0 || empty == nil {
		t.Fatalf("unconfigured pool list must return an honest empty array: pools=%v err=%v", empty, err)
	}
	draft, err := svc.SaveDraft(ctx, auth, ConfigDraftInput{DraftID: "pool-view-draft", Definition: validDefinition(t)})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := svc.ValidateDraft(ctx, auth, draft.DraftID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActivateDraft(ctx, auth, domain.ActivationInput{CommandID: "pool-view-activate", DraftID: draft.DraftID, ExpectedDraftVersion: validated.Version}); err != nil {
		t.Fatal(err)
	}
	view, err := queries.Get(ctx, reader, "pool-main")
	if err != nil {
		t.Fatalf("pool view: %v", err)
	}
	if view.Pool.PoolID != "pool-main" || view.Pool.Version != 1 || len(view.Pool.Members) != 2 || len(view.Groups) != 1 || len(view.Groups[0].Members) != 2 {
		t.Fatalf("unexpected grouped pool projection: %+v", view)
	}
	if view.Pool.Members[0].OfferID != "offer-alpha-chat-a" || view.Pool.Members[1].OfferID != "offer-alpha-chat-b" {
		t.Fatalf("same-priority members lack stable offerId ordering: %+v", view.Pool.Members)
	}
	if view.Groups[0].Members[0].Status != "available" || len(view.Groups[0].Members[0].Reasons) != 0 || len(view.Groups[0].Members[0].Resources) != 3 {
		t.Fatalf("activated config should project the verified resource bundle: %+v", view.Groups[0].Members[0])
	}
	for _, resource := range view.Groups[0].Members[0].Resources {
		if resource.EvidenceStatus != "verified" || resource.Available == nil || resource.HoldIDs == nil || resource.EncumbranceIDs == nil {
			t.Fatalf("resource projection omitted verified state: %+v", resource)
		}
		want := domain.Units(0)
		switch resource.ResourceID {
		case "res-alpha-daily-token":
			want = 1_000_000
		case "res-alpha-usd-entitlement":
			want = 500
		case "res-alpha-concurrency":
			want = 1
		}
		if *resource.Available != want {
			t.Errorf("resource %s available=%d, want %d", resource.ResourceID, *resource.Available, want)
		}
	}
	if _, err := queries.Get(ctx, domain.AuthContext{PrincipalID: auth.PrincipalID, Namespace: "ns-other", Permissions: []domain.Permission{domain.PermRead}}, "pool-main"); !errors.Is(err, ErrPoolQueryNotFound) {
		t.Fatalf("cross-namespace pool read = %v, want not found", err)
	}
	if _, err := queries.List(ctx, domain.AuthContext{PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Permissions: []domain.Permission{domain.PermExecute}}); !errors.Is(err, ErrPoolQueryForbidden) {
		t.Fatalf("execute-only principal pool listing = %v, want forbidden", err)
	}
	var doc config.ConfigDocument
	if err := json.Unmarshal(validDefinition(t), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Pools = []config.PoolDef{}
	grants := doc.Grants[:0]
	for _, grant := range doc.Grants {
		if grant.ScopeKind != "pool" {
			grants = append(grants, grant)
		}
	}
	doc.Grants = grants
	definition, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	active, err := svc.Read(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := svc.SaveDraft(ctx, auth, ConfigDraftInput{DraftID: "pool-view-remove", BaseRevision: active.BaseRevision, Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	validated, err = svc.ValidateDraft(ctx, auth, removed.DraftID, removed.Version)
	if err != nil {
		t.Fatalf("validate config without pool: %v", err)
	}
	if _, err := svc.ActivateDraft(ctx, auth, domain.ActivationInput{CommandID: "pool-view-remove-activate", DraftID: removed.DraftID, ExpectedDraftVersion: validated.Version, ExpectedConfigRevision: active.BaseRevision}); err != nil {
		t.Fatalf("activate config without pool: %v; draft=%+v active=%+v", err, validated, active)
	}
	list, err := queries.List(ctx, reader)
	if err != nil || len(list) != 0 {
		t.Fatalf("removed pool remained in the active list: pools=%+v err=%v", list, err)
	}
	if _, err := queries.Get(ctx, reader, "pool-main"); !errors.Is(err, ErrPoolQueryNotFound) {
		t.Fatalf("removed pool detail = %v, want not found", err)
	}
}
