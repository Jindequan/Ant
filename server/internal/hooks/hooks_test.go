package hooks

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func deadlineContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

type registryLockDuringDeadlineContext struct {
	context.Context
	registry *Registry
	once     sync.Once
}

func (ctx *registryLockDuringDeadlineContext) Err() error {
	ctx.once.Do(func() {
		locked := make(chan struct{})
		go func() {
			ctx.registry.mu.Lock()
			close(locked)
			<-ctx.Context.Done()
			ctx.registry.mu.Unlock()
		}()
		<-locked
	})
	return ctx.Context.Err()
}

func TestRegistrationValidationAndSeal(t *testing.T) {
	r := NewRegistry()
	if err := r.Check(deadlineContext(t), Event{Point: PointRequestValidated}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("Check before Seal error = %v, want ErrInvalidRegistration", err)
	}
	if err := r.Register(Registration{Version: "v1", Name: "", Point: PointRequestValidated, Guard: func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} }}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("empty name error = %v, want ErrInvalidRegistration", err)
	}
	if err := r.Register(Registration{Name: "missing-version", Point: PointRequestValidated, Guard: func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} }}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("empty version error = %v, want ErrInvalidRegistration", err)
	}
	if err := r.Register(Registration{Version: "v1", Name: "order-at-wrong-point", Point: PointRequestValidated, Orderer: func(context.Context, Event, []string) ([]string, error) { return nil, nil }}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("wrong hook type error = %v, want ErrInvalidRegistration", err)
	}
	if err := r.Register(Registration{Version: "v1", Name: "unknown-point", Point: Point("operation.created"), Guard: func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} }}); !errors.Is(err, ErrUnknownPoint) {
		t.Fatalf("unknown point error = %v, want ErrUnknownPoint", err)
	}
	guard := func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} }
	reg := Registration{Version: "v1", Name: "guard", Point: PointRequestValidated, Guard: guard}
	if err := r.Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(reg); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name error = %v, want ErrDuplicateName", err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1", Name: "late", Point: PointRequestValidated, Guard: guard}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("registration after Seal error = %v, want ErrInvalidRegistration", err)
	}
}

func TestRegistryManifestIsStableAndRevisionReachesCallbacks(t *testing.T) {
	newRegistry := func(version string, observedRevision *string) *Registry {
		t.Helper()
		r := NewRegistry()
		point := Point("router.auditable_stage")
		if err := r.DefinePoint(point, CapabilityGuard); err != nil {
			t.Fatal(err)
		}
		if err := r.Register(Registration{
			Name: "audit-guard", Version: version, Point: point, Priority: 4,
			Guard: func(_ context.Context, event Event) GuardDecision {
				*observedRevision = event.RegistryRevision
				return GuardDecision{Allowed: true}
			},
		}); err != nil {
			t.Fatal(err)
		}
		if err := r.Seal(); err != nil {
			t.Fatal(err)
		}
		return r
	}

	var observedRevision string
	r := newRegistry("v1", &observedRevision)
	if _, err := NewRegistry().Manifest(); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("unsealed manifest error = %v, want ErrInvalidRegistration", err)
	}
	manifest, err := r.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Points) != len(builtInPoints)+1 || len(manifest.Registrations) != 1 {
		t.Fatalf("manifest points=%d registrations=%d", len(manifest.Points), len(manifest.Registrations))
	}
	if manifest.APIVersion != RegistryAPIVersion {
		t.Fatalf("manifest API version = %q, want %q", manifest.APIVersion, RegistryAPIVersion)
	}
	if manifest.Limits.MaxCustomPoints != DefaultMaxCustomPoints ||
		manifest.Limits.MaxRegistrationsPerPoint != DefaultMaxRegistrationsPerPoint ||
		manifest.Limits.MaxTransformBytes != DefaultMaxTransformBytes ||
		manifest.Limits.MaxEventPayloadBytes != DefaultMaxEventPayloadBytes {
		t.Fatalf("manifest limits = %+v", manifest.Limits)
	}
	entry := manifest.Registrations[0]
	if entry.Name != "audit-guard" || entry.Version != "v1" || entry.Point != "router.auditable_stage" ||
		entry.Priority != 4 || entry.Capability != CapabilityGuard {
		t.Fatalf("manifest registration = %+v", entry)
	}
	manifest.Registrations[0].Version = "tampered"
	secondManifest, err := r.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if secondManifest.Registrations[0].Version != "v1" {
		t.Fatalf("manifest mutation escaped defensive copy: %+v", secondManifest.Registrations[0])
	}
	revision, err := r.Revision()
	if err != nil {
		t.Fatal(err)
	}
	if len(revision) != 64 || observedRevision != "" {
		t.Fatalf("revision=%q callback revision before invocation=%q", revision, observedRevision)
	}
	if err := r.Check(deadlineContext(t), Event{Point: "router.auditable_stage", RegistryRevision: "forged"}); err != nil {
		t.Fatal(err)
	}
	if observedRevision != revision {
		t.Fatalf("callback registry revision = %q, want sealed revision %q", observedRevision, revision)
	}

	var secondObserved string
	r2 := newRegistry("v2", &secondObserved)
	revision2, err := r2.Revision()
	if err != nil {
		t.Fatal(err)
	}
	if revision2 == revision {
		t.Fatalf("changing Hook version did not change registry revision %q", revision)
	}
}

func TestRegistryRevisionIncludesEffectiveBudgets(t *testing.T) {
	newRegistry := func(options RegistryOptions) *Registry {
		t.Helper()
		r, err := NewRegistryWithOptions(options)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Seal(); err != nil {
			t.Fatal(err)
		}
		return r
	}

	defaultOptions := DefaultRegistryOptions()
	restrictedOptions := defaultOptions
	restrictedOptions.MaxEventPayloadBytes /= 2
	defaultRegistry := newRegistry(defaultOptions)
	restrictedRegistry := newRegistry(restrictedOptions)
	defaultRevision, err := defaultRegistry.Revision()
	if err != nil {
		t.Fatal(err)
	}
	restrictedRevision, err := restrictedRegistry.Revision()
	if err != nil {
		t.Fatal(err)
	}
	if defaultRevision == restrictedRevision {
		t.Fatalf("different effective budgets shared registry revision %q", defaultRevision)
	}
	manifest, err := restrictedRegistry.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Limits.MaxEventPayloadBytes != restrictedOptions.MaxEventPayloadBytes {
		t.Fatalf("manifest event budget = %d, want %d", manifest.Limits.MaxEventPayloadBytes, restrictedOptions.MaxEventPayloadBytes)
	}
}

func TestEveryHookTypeReceivesSealedRegistryRevision(t *testing.T) {
	r := NewRegistry()
	decisionPoint := Point("router.all_hook_revision")
	observerPoint := Point("router.all_hook_revision.committed")
	if err := r.DefinePoint(decisionPoint, CapabilityGuard|CapabilityOrderer|CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	if err := r.DefinePostCommitPoint(observerPoint); err != nil {
		t.Fatal(err)
	}
	observed := make(map[Capability]string)
	observedAPIVersion := make(map[Capability]string)
	if err := r.Register(Registration{
		Name: "revision-guard", Version: "v1", Point: decisionPoint,
		Guard: func(_ context.Context, event Event) GuardDecision {
			observed[CapabilityGuard] = event.RegistryRevision
			observedAPIVersion[CapabilityGuard] = event.APIVersion
			return GuardDecision{Allowed: true}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{
		Name: "revision-orderer", Version: "v1", Point: decisionPoint,
		Orderer: func(_ context.Context, event Event, candidates []string) ([]string, error) {
			observed[CapabilityOrderer] = event.RegistryRevision
			observedAPIVersion[CapabilityOrderer] = event.APIVersion
			return candidates, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{
		Name: "revision-transformer", Version: "v1", Point: decisionPoint,
		Transformer: func(_ context.Context, event Event, input []byte) ([]byte, error) {
			observed[CapabilityTransformer] = event.RegistryRevision
			observedAPIVersion[CapabilityTransformer] = event.APIVersion
			return input, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{
		Name: "revision-observer", Version: "v1", Point: observerPoint,
		Observer: func(_ context.Context, event Event) error {
			observed[CapabilityObserver] = event.RegistryRevision
			observedAPIVersion[CapabilityObserver] = event.APIVersion
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	revision, err := r.Revision()
	if err != nil {
		t.Fatal(err)
	}
	ctx := deadlineContext(t)
	if err := r.Check(ctx, Event{Point: decisionPoint, APIVersion: "forged", RegistryRevision: "forged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.OrderCandidates(ctx, Event{Point: decisionPoint, APIVersion: "forged", RegistryRevision: "forged"}, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Transform(ctx, Event{Point: decisionPoint, APIVersion: "forged", RegistryRevision: "forged"}, []byte("plan"), func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if failures := r.Notify(ctx, Event{Point: observerPoint, APIVersion: "forged", RegistryRevision: "forged"}); len(failures) != 0 {
		t.Fatalf("observer failures = %v", failures)
	}
	for _, capability := range []Capability{CapabilityGuard, CapabilityOrderer, CapabilityTransformer, CapabilityObserver} {
		if observed[capability] != revision {
			t.Errorf("capability %d saw revision %q, want %q", capability, observed[capability], revision)
		}
		if observedAPIVersion[capability] != RegistryAPIVersion {
			t.Errorf("capability %d saw API version %q, want %q", capability, observedAPIVersion[capability], RegistryAPIVersion)
		}
	}
}

func TestRegistryRevisionDoesNotDependOnRegistrationOrder(t *testing.T) {
	newRegistry := func(reverse bool) *Registry {
		t.Helper()
		r := NewRegistry()
		point := Point("router.order_independent")
		if err := r.DefinePoint(point, CapabilityGuard); err != nil {
			t.Fatal(err)
		}
		registrations := []Registration{
			{Name: "guard-a", Version: "v1", Point: point, Priority: 2, Guard: func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} }},
			{Name: "guard-b", Version: "v3", Point: point, Priority: 1, Guard: func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} }},
		}
		if reverse {
			registrations[0], registrations[1] = registrations[1], registrations[0]
		}
		for _, registration := range registrations {
			if err := r.Register(registration); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Seal(); err != nil {
			t.Fatal(err)
		}
		return r
	}

	forwardRevision, err := newRegistry(false).Revision()
	if err != nil {
		t.Fatal(err)
	}
	reverseRevision, err := newRegistry(true).Revision()
	if err != nil {
		t.Fatal(err)
	}
	if forwardRevision != reverseRevision {
		t.Fatalf("registration order changed revision: forward=%q reverse=%q", forwardRevision, reverseRevision)
	}
}

func TestCompositionRootCanDefineCustomLifecyclePoints(t *testing.T) {
	r := NewRegistry()
	point := Point("router.after_resource_plan")
	if err := r.DefinePoint(point, CapabilityGuard|CapabilityOrderer|CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	committedPoint := Point("router.resource_plan.committed")
	if err := r.DefinePostCommitPoint(committedPoint); err != nil {
		t.Fatal(err)
	}
	if err := r.DefinePoint(point, CapabilityOrderer); !errors.Is(err, ErrDuplicatePoint) {
		t.Fatalf("duplicate point error = %v, want ErrDuplicatePoint", err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:  "custom-guard",
		Point: point,
		Guard: func(_ context.Context, event Event) GuardDecision {
			return GuardDecision{Allowed: event.OperationID == "op-custom"}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:  "custom-ranker",
		Point: point,
		Orderer: func(_ context.Context, _ Event, ids []string) ([]string, error) {
			return []string{ids[1], ids[0]}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	var observed bool
	if err := r.Register(Registration{Version: "v1",
		Name:  "custom-observer",
		Point: committedPoint,
		Observer: func(_ context.Context, event Event) error {
			observed = event.OperationID == "op-custom"
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:  "unsupported-guard",
		Point: committedPoint,
		Guard: func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} },
	}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("unsupported capability error = %v, want ErrInvalidRegistration", err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(deadlineContext(t), Event{Point: point, OperationID: "op-custom"}); err != nil {
		t.Fatalf("custom point guard error = %v, want allow", err)
	}
	if err := r.Check(deadlineContext(t), Event{Point: point, OperationID: "op-denied"}); !errors.Is(err, ErrGuardDenied) {
		t.Fatalf("custom point guard denial = %v, want ErrGuardDenied", err)
	}
	ordered, err := r.OrderCandidates(deadlineContext(t), Event{Point: point}, []string{"first", "second"})
	if err != nil || !reflect.DeepEqual(ordered, []string{"second", "first"}) {
		t.Fatalf("custom point order = %v, %v; want [second first], nil", ordered, err)
	}
	if failures := r.Notify(deadlineContext(t), Event{Point: committedPoint, OperationID: "op-custom"}); len(failures) != 0 || !observed {
		t.Fatalf("custom point Notify failures=%v observed=%v; want no failures and event delivery", failures, observed)
	}
	if err := r.DefinePoint("router.after_send", CapabilityGuard); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("DefinePoint after Seal error = %v, want ErrInvalidRegistration", err)
	}
}

func TestObserverPointsAreExplicitlyPostCommit(t *testing.T) {
	r := NewRegistry()
	if err := r.DefinePoint("router.before_send", CapabilityGuard); err != nil {
		t.Fatal(err)
	}
	if err := r.DefinePoint("router.invalid_observer", CapabilityObserver); !errors.Is(err, ErrObserverRequiresPostCommit) {
		t.Fatalf("pre-commit observer point error = %v, want ErrObserverRequiresPostCommit", err)
	}
	if err := r.EnableCapabilities("router.before_send", CapabilityObserver); !errors.Is(err, ErrObserverRequiresPostCommit) {
		t.Fatalf("enable pre-commit observer error = %v, want ErrObserverRequiresPostCommit", err)
	}
	if err := r.DefinePostCommitPoint("router.resource.committed"); err != nil {
		t.Fatal(err)
	}
	if err := r.EnableCapabilities("router.resource.committed", CapabilityTransformer); !errors.Is(err, ErrPostCommitObserverOnly) {
		t.Fatalf("enable post-commit transformer error = %v, want ErrPostCommitObserverOnly", err)
	}
	if err := r.EnableCapabilities(PointOperationCommitted, CapabilityGuard); !errors.Is(err, ErrPostCommitObserverOnly) {
		t.Fatalf("enable built-in post-commit guard error = %v, want ErrPostCommitObserverOnly", err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name: "committed-observer", Point: "router.resource.committed",
		Observer: func(context.Context, Event) error { return nil },
	}); err != nil {
		t.Fatalf("register post-commit observer: %v", err)
	}
}

func TestCompositionRootCanExtendBuiltInPointCapabilities(t *testing.T) {
	defaults := NewRegistry()
	if err := defaults.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := defaults.Transform(deadlineContext(t), Event{Point: PointRequestValidated}, []byte("request"), func([]byte) error { return nil }); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("default built-in transform error = %v, want ErrUnsupportedCapability", err)
	}

	r := NewRegistry()
	if err := r.EnableCapabilities(PointRequestValidated, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:  "request-normalizer",
		Point: PointRequestValidated,
		Transformer: func(_ context.Context, _ Event, input []byte) ([]byte, error) {
			return []byte(strings.ToUpper(string(input))), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	validated := 0
	output, err := r.Transform(deadlineContext(t), Event{Point: PointRequestValidated}, []byte("request"), func(value []byte) error {
		validated++
		if len(value) != len("request") {
			return errors.New("invalid request length")
		}
		return nil
	})
	if err != nil || string(output) != "REQUEST" || validated != 2 {
		t.Fatalf("built-in point transform output=%q err=%v validations=%d; want REQUEST and two validations", output, err, validated)
	}
	if err := r.EnableCapabilities(PointRequestValidated, CapabilityGuard); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("capability change after Seal error = %v, want ErrInvalidRegistration", err)
	}
	if err := NewRegistry().EnableCapabilities("unknown.point", CapabilityTransformer); !errors.Is(err, ErrUnknownPoint) {
		t.Fatalf("unknown capability point error = %v, want ErrUnknownPoint", err)
	}
}

func TestRegistryOptionsBoundCustomPointsAndRegistrationChains(t *testing.T) {
	r, err := NewRegistryWithOptions(RegistryOptions{MaxCustomPoints: 1, MaxRegistrationsPerPoint: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, second := Point("router.after_plan"), Point("router.before_settle")
	if err := r.DefinePostCommitPoint(first); err != nil {
		t.Fatal(err)
	}
	if err := r.DefinePostCommitPoint(second); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("second custom point error = %v, want ErrLimitExceeded", err)
	}
	observer := func(context.Context, Event) error { return nil }
	if err := r.Register(Registration{Version: "v1", Name: "observer-a", Point: first, Observer: observer}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1", Name: "observer-b", Point: first, Observer: observer}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("second registration error = %v, want ErrLimitExceeded", err)
	}
	if _, err := NewRegistryWithOptions(RegistryOptions{MaxCustomPoints: -1}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("negative option error = %v, want ErrInvalidOptions", err)
	}
	if _, err := NewRegistryWithOptions(RegistryOptions{MaxEventPayloadBytes: -1}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("negative event payload budget error = %v, want ErrInvalidOptions", err)
	}
}

func TestExplicitZeroRegistryBudgetsDisableExtensions(t *testing.T) {
	r, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.DefinePostCommitPoint("router.extra"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("zero custom-point budget error = %v, want ErrLimitExceeded", err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:  "disabled-hook",
		Point: PointOperationCommitted,
		Observer: func(context.Context, Event) error {
			return nil
		},
	}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("zero registration budget error = %v, want ErrLimitExceeded", err)
	}
	if err := r.DefinePoint("router.transform_disabled", CapabilityTransformer); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("zero transform budget point error = %v, want ErrLimitExceeded", err)
	}
	if err := r.EnableCapabilities(PointRequestValidated, CapabilityTransformer); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("zero transform budget capability error = %v, want ErrLimitExceeded", err)
	}
	if r.capabilities[PointRequestValidated]&CapabilityTransformer != 0 {
		t.Fatal("failed capability enable unexpectedly changed the registry")
	}
	if got := DefaultRegistryOptions(); got.MaxCustomPoints != DefaultMaxCustomPoints ||
		got.MaxRegistrationsPerPoint != DefaultMaxRegistrationsPerPoint || got.MaxTransformBytes != DefaultMaxTransformBytes ||
		got.MaxEventPayloadBytes != DefaultMaxEventPayloadBytes {
		t.Fatalf("default registry options = %+v, unexpected defaults", got)
	}
}

func TestEventPayloadBudgetFailsClosedAcrossHookTypes(t *testing.T) {
	options := DefaultRegistryOptions()
	options.MaxEventPayloadBytes = 1
	r, err := NewRegistryWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	transformPoint := Point("router.event_budget_transform")
	if err := r.DefinePoint(transformPoint, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	called := map[string]bool{}
	registrations := []Registration{
		{Version: "v1", Name: "budget-guard", Point: PointRequestValidated, Guard: func(context.Context, Event) GuardDecision {
			called["guard"] = true
			return GuardDecision{Allowed: true}
		}},
		{Version: "v1", Name: "budget-orderer", Point: PointCandidatesReady, Orderer: func(_ context.Context, _ Event, ids []string) ([]string, error) {
			called["orderer"] = true
			return ids, nil
		}},
		{Version: "v1", Name: "budget-transformer", Point: transformPoint, Transformer: func(_ context.Context, _ Event, input []byte) ([]byte, error) {
			called["transformer"] = true
			return input, nil
		}},
		{Version: "v1", Name: "budget-observer", Point: PointOperationCommitted, Observer: func(context.Context, Event) error {
			called["observer"] = true
			return nil
		}},
	}
	for _, registration := range registrations {
		if err := r.Register(registration); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx := deadlineContext(t)
	largePayload := []byte("12")
	if err := r.Check(ctx, Event{Point: PointRequestValidated, Payload: largePayload}); !errors.Is(err, ErrGuardDenied) || !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversized guard event error = %v, want ErrGuardDenied and ErrEventTooLarge", err)
	}
	if _, err := r.OrderCandidates(ctx, Event{Point: PointCandidatesReady, Payload: largePayload}, []string{"a"}); !errors.Is(err, ErrInvalidPermutation) || !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversized orderer event error = %v, want ErrInvalidPermutation and ErrEventTooLarge", err)
	}
	validated := false
	if _, err := r.Transform(ctx, Event{Point: transformPoint, Payload: largePayload}, []byte("input"), func([]byte) error {
		validated = true
		return nil
	}); !errors.Is(err, ErrInvalidTransformation) || !errors.Is(err, ErrEventTooLarge) || validated {
		t.Fatalf("oversized transformer event error=%v validator_called=%v; want reject before validation", err, validated)
	}
	if failures := r.Notify(ctx, Event{Point: PointOperationCommitted, Payload: largePayload}); len(failures) != 1 || !errors.Is(failures[0], ErrEventTooLarge) {
		t.Fatalf("oversized observer event failures = %v, want ErrEventTooLarge", failures)
	}
	if len(called) != 0 {
		t.Fatalf("oversized event reached callbacks: %v", called)
	}

	boundary := []byte("1")
	if err := r.Check(ctx, Event{Point: PointRequestValidated, Payload: boundary}); err != nil {
		t.Fatalf("payload at configured limit rejected: %v", err)
	}
	if _, err := r.OrderCandidates(ctx, Event{Point: PointCandidatesReady, Payload: boundary}, []string{"a"}); err != nil {
		t.Fatalf("orderer payload at configured limit rejected: %v", err)
	}
	if _, err := r.Transform(ctx, Event{Point: transformPoint, Payload: boundary}, []byte("input"), func([]byte) error { return nil }); err != nil {
		t.Fatalf("transformer payload at configured limit rejected: %v", err)
	}
	if failures := r.Notify(ctx, Event{Point: PointOperationCommitted, Payload: boundary}); len(failures) != 0 {
		t.Fatalf("observer payload at configured limit failed: %v", failures)
	}
	if len(called) != 4 {
		t.Fatalf("callbacks called under-limit = %v, want all four", called)
	}
}

func TestTransformDeadlinePrecedesOversizedEventError(t *testing.T) {
	r := NewRegistry()
	point := Point("router.transform_budget_deadline")
	if err := r.DefinePoint(point, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := r.Register(Registration{Version: "v1",
		Name:  "budget-deadline-transformer",
		Point: point,
		Transformer: func(context.Context, Event, []byte) ([]byte, error) {
			called = true
			return []byte("changed"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)
	ctx := &registryLockDuringDeadlineContext{Context: base, registry: r}
	validated := false
	payload := make([]byte, DefaultMaxEventPayloadBytes+1)
	output, err := r.Transform(ctx, Event{Point: point, Payload: payload}, []byte("original"), func([]byte) error {
		validated = true
		return nil
	})
	if output != nil || !errors.Is(err, ErrInvalidTransformation) || !errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrEventTooLarge) || validated || called {
		t.Fatalf("Transform output=%q error=%v validator_called=%v transformer_called=%v; want deadline precedence before validation/callback", output, err, validated, called)
	}
}

func TestEventPayloadBudgetDoesNotRejectWithoutMatchingCallbacks(t *testing.T) {
	options := DefaultRegistryOptions()
	options.MaxEventPayloadBytes = 0
	r, err := NewRegistryWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnableCapabilities(PointRequestValidated, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1", Name: "only-transformer", Point: PointRequestValidated, Transformer: func(_ context.Context, _ Event, input []byte) ([]byte, error) {
		return input, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(deadlineContext(t), Event{Point: PointRequestValidated, Payload: []byte("large")}); err != nil {
		t.Fatalf("empty guard chain should not inspect an unused event payload: %v", err)
	}
}

func TestTransformerPayloadBudgetIsEnforced(t *testing.T) {
	r, err := NewRegistryWithOptions(RegistryOptions{
		MaxCustomPoints: 1, MaxRegistrationsPerPoint: 1, MaxTransformBytes: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	point := Point("router.bounded_transform")
	if err := r.DefinePoint(point, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := r.Register(Registration{Version: "v1",
		Name:  "expander",
		Point: point,
		Transformer: func(context.Context, Event, []byte) ([]byte, error) {
			called = true
			return []byte("123"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Transform(deadlineContext(t), Event{Point: point}, []byte("123"), func([]byte) error { return nil }); !errors.Is(err, ErrTransformTooLarge) || called {
		t.Fatalf("oversized input error=%v callback_called=%v; want reject before hook call", err, called)
	}
	if _, err := r.Transform(deadlineContext(t), Event{Point: point}, []byte("1"), func([]byte) error { return nil }); !errors.Is(err, ErrTransformTooLarge) {
		t.Fatalf("oversized transformed result error = %v, want ErrTransformTooLarge", err)
	}
}

func TestZeroTransformBudgetDisablesTransformerCapability(t *testing.T) {
	r, err := NewRegistryWithOptions(RegistryOptions{
		MaxCustomPoints: 1, MaxRegistrationsPerPoint: 1, MaxTransformBytes: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	point := Point("router.disabled_transform")
	if err := r.DefinePoint(point, CapabilityTransformer); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("disabled transformer point error = %v, want ErrLimitExceeded", err)
	}
}

func TestCustomTransformerChainRevalidatesEveryValue(t *testing.T) {
	r := NewRegistry()
	point := Point("router.after_plan")
	if err := r.DefinePoint(point, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	for _, reg := range []Registration{
		{
			Version: "v1",
			Name:    "first",
			Point:   point,
			Transformer: func(_ context.Context, _ Event, input []byte) ([]byte, error) {
				input[0] = '2'
				return input, nil
			},
		},
		{
			Version: "v1",
			Name:    "second",
			Point:   point,
			Transformer: func(_ context.Context, _ Event, input []byte) ([]byte, error) {
				if string(input) != "2" {
					return nil, errors.New("transformers did not run in order")
				}
				return []byte("3"), nil
			},
		},
	} {
		if err := r.Register(reg); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	input := []byte("1")
	validated := 0
	output, err := r.Transform(deadlineContext(t), Event{Point: point}, input, func(value []byte) error {
		validated++
		if len(value) != 1 || value[0] < '1' || value[0] > '3' {
			return errors.New("secret validator detail")
		}
		return nil
	})
	if err != nil || string(output) != "3" || string(input) != "1" || validated != 3 {
		t.Fatalf("Transform output=%q err=%v input=%q validations=%d; want output 3, original 1, three validations", output, err, input, validated)
	}
}

func TestTransformerFailuresAreRejectedAndSanitized(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   Transformer
		want error
	}{
		{
			name: "callback error",
			fn: func(context.Context, Event, []byte) ([]byte, error) {
				return nil, errors.New("Authorization: private-secret")
			},
			want: ErrHookFailed,
		},
		{
			name: "panic",
			fn: func(context.Context, Event, []byte) ([]byte, error) {
				panic("Authorization: private-secret")
			},
			want: ErrHookFailed,
		},
		{
			name: "invalid output",
			fn: func(context.Context, Event, []byte) ([]byte, error) {
				return []byte("unsafe"), nil
			},
			want: ErrInvalidTransformation,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			point := Point("router.transform")
			if err := r.DefinePoint(point, CapabilityTransformer); err != nil {
				t.Fatal(err)
			}
			if err := r.Register(Registration{Version: "v1", Name: "transformer", Point: point, Transformer: tc.fn}); err != nil {
				t.Fatal(err)
			}
			if err := r.Seal(); err != nil {
				t.Fatal(err)
			}
			output, err := r.Transform(deadlineContext(t), Event{Point: point}, []byte("valid"), func(value []byte) error {
				if string(value) != "valid" {
					return errors.New("private-secret validator detail")
				}
				return nil
			})
			if output != nil || !errors.Is(err, ErrInvalidTransformation) || !errors.Is(err, tc.want) ||
				strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("Transform output=%q error=%v; want rejected and sanitized %v", output, err, tc.want)
			}
		})
	}
}

func TestTransformDeadlineTakesPrecedenceOverHookError(t *testing.T) {
	r := NewRegistry()
	point := Point("router.deadline_transform")
	if err := r.DefinePoint(point, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:  "late-transformer",
		Point: point,
		Transformer: func(ctx context.Context, _ Event, input []byte) ([]byte, error) {
			<-ctx.Done()
			return input, errors.New("Authorization: private-secret")
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := r.Transform(ctx, Event{Point: point}, []byte("valid"), func([]byte) error { return nil })
	if !errors.Is(err, ErrInvalidTransformation) || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("late transformer error = %v, want rejected deadline without callback detail", err)
	}
}

func TestExpiredTransformContextSkipsValidator(t *testing.T) {
	r := NewRegistry()
	point := Point("router.expired_transform")
	if err := r.DefinePoint(point, CapabilityTransformer); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	validated := false
	_, err := r.Transform(ctx, Event{Point: point}, []byte("input"), func([]byte) error {
		validated = true
		return nil
	})
	if !errors.Is(err, ErrInvalidTransformation) || !errors.Is(err, context.DeadlineExceeded) || validated {
		t.Fatalf("expired Transform error=%v validator_called=%v; want deadline failure before validation", err, validated)
	}
}

func TestTransformRequiresDeclaredCapabilityAndValidator(t *testing.T) {
	r := NewRegistry()
	point := Point("router.audit")
	if err := r.DefinePostCommitPoint(point); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx := deadlineContext(t)
	if _, err := r.Transform(ctx, Event{Point: point}, []byte("input"), func([]byte) error { return nil }); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("unsupported transform error = %v, want ErrUnsupportedCapability", err)
	}
	if _, err := r.Transform(ctx, Event{Point: point}, []byte("input"), nil); !errors.Is(err, ErrInvalidValidator) {
		t.Fatalf("nil validator error = %v, want ErrInvalidValidator", err)
	}
}

func TestStablePriorityThenNameOrder(t *testing.T) {
	r := NewRegistry()
	var got []string
	for _, reg := range []Registration{
		{Version: "v1", Name: "z", Priority: 5},
		{Version: "v1", Name: "b", Priority: 1},
		{Version: "v1", Name: "a", Priority: 1},
	} {
		reg.Point = PointRequestValidated
		reg.Guard = func(_ context.Context, _ Event) GuardDecision {
			got = append(got, reg.Name)
			return GuardDecision{Allowed: true}
		}
		if err := r.Register(reg); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(deadlineContext(t), Event{Point: PointRequestValidated}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
}

func TestGuardFailsClosedAndReceivesDefensiveSnapshot(t *testing.T) {
	r := NewRegistry()
	var observed []byte
	if err := r.Register(Registration{Version: "v1",
		Name:  "mutator",
		Point: PointRequestValidated,
		Guard: func(_ context.Context, event Event) GuardDecision {
			event.Payload[0] = 'x'
			return GuardDecision{Allowed: true}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:     "reader",
		Point:    PointRequestValidated,
		Priority: 1,
		Guard: func(_ context.Context, event Event) GuardDecision {
			observed = append([]byte(nil), event.Payload...)
			return GuardDecision{Allowed: false, Code: "policy_blocked"}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	payload := []byte("safe")
	err := r.Check(deadlineContext(t), Event{Point: PointRequestValidated, Payload: payload})
	if !errors.Is(err, ErrGuardDenied) || !reflect.DeepEqual(observed, payload) {
		t.Fatalf("Check error=%v, observer payload=%q; want denial and original snapshot %q", err, observed, payload)
	}
	if string(payload) != "safe" {
		t.Fatalf("caller payload mutated to %q", payload)
	}
}

func TestGuardPanicAndMissingDeadlineFailClosed(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Registration{Version: "v1",
		Name:  "panic",
		Point: PointRequestValidated,
		Guard: func(context.Context, Event) GuardDecision { panic("secret detail") },
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(context.Background(), Event{Point: PointRequestValidated}); !errors.Is(err, ErrDeadlineRequired) {
		t.Fatalf("missing deadline error = %v, want ErrDeadlineRequired", err)
	}
	err := r.Check(deadlineContext(t), Event{Point: PointRequestValidated})
	if !errors.Is(err, ErrGuardDenied) || strings.Contains(err.Error(), "secret detail") {
		t.Fatalf("panic result should be generic fail-closed denial, got %v", err)
	}
}

func TestGuardDeadlineExpirationFailsClosed(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Registration{Version: "v1",
		Name:  "cooperative-timeout",
		Point: PointRequestValidated,
		Guard: func(ctx context.Context, _ Event) GuardDecision {
			<-ctx.Done()
			return GuardDecision{Allowed: true}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := r.Check(ctx, Event{Point: PointRequestValidated}); !errors.Is(err, ErrGuardDenied) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired guard error = %v, want ErrGuardDenied and context.DeadlineExceeded", err)
	}
}

func TestDeadlineClassificationTakesPrecedenceOverHookFailure(t *testing.T) {
	t.Run("guard panic", func(t *testing.T) {
		r := NewRegistry()
		if err := r.Register(Registration{Version: "v1",
			Name:  "late-panic",
			Point: PointRequestValidated,
			Guard: func(ctx context.Context, _ Event) GuardDecision {
				<-ctx.Done()
				panic("Authorization: private-secret")
			},
		}); err != nil {
			t.Fatal(err)
		}
		if err := r.Seal(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		err := r.Check(ctx, Event{Point: PointRequestValidated})
		if !errors.Is(err, ErrGuardDenied) || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "private-secret") {
			t.Fatalf("late guard panic error = %v, want denial + deadline without panic detail", err)
		}
	})

	t.Run("orderer error", func(t *testing.T) {
		r := NewRegistry()
		if err := r.Register(Registration{Version: "v1",
			Name:  "late-error",
			Point: PointCandidatesReady,
			Orderer: func(ctx context.Context, _ Event, _ []string) ([]string, error) {
				<-ctx.Done()
				return nil, errors.New("Authorization: private-secret")
			},
		}); err != nil {
			t.Fatal(err)
		}
		if err := r.Seal(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		_, err := r.OrderCandidates(ctx, Event{Point: PointCandidatesReady}, []string{"a"})
		if !errors.Is(err, ErrInvalidPermutation) || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "private-secret") {
			t.Fatalf("late orderer error = %v, want invalid permutation + deadline without callback detail", err)
		}
	})
}

func TestOrderCandidatesRequiresExactUniqueSet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Registration{Version: "v1",
		Name:  "reverse",
		Point: PointCandidatesReady,
		Orderer: func(_ context.Context, _ Event, ids []string) ([]string, error) {
			return []string{ids[1], ids[0]}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx := deadlineContext(t)
	ordered, err := r.OrderCandidates(ctx, Event{Point: PointCandidatesReady}, []string{"a", "b"})
	if err != nil || !reflect.DeepEqual(ordered, []string{"b", "a"}) {
		t.Fatalf("OrderCandidates = %v, %v; want [b a], nil", ordered, err)
	}
	if _, err := r.OrderCandidates(ctx, Event{Point: PointCandidatesReady}, []string{"a", "a"}); !errors.Is(err, ErrDuplicateCandidateID) {
		t.Fatalf("duplicate eligible IDs error = %v, want ErrDuplicateCandidateID", err)
	}

	invalid := NewRegistry()
	if err := invalid.Register(Registration{Version: "v1",
		Name:    "inject",
		Point:   PointCandidatesReady,
		Orderer: func(context.Context, Event, []string) ([]string, error) { return []string{"a", "extra"}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := invalid.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, err := invalid.OrderCandidates(ctx, Event{Point: PointCandidatesReady}, []string{"a", "b"}); !errors.Is(err, ErrInvalidPermutation) {
		t.Fatalf("injected candidate error = %v, want ErrInvalidPermutation", err)
	}
}

func TestOrdererErrorAndPanicFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   CandidateOrderer
	}{
		{name: "error", fn: func(context.Context, Event, []string) ([]string, error) {
			return nil, errors.New("Authorization: private-secret")
		}},
		{name: "panic", fn: func(context.Context, Event, []string) ([]string, error) { panic("private detail") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			if err := r.Register(Registration{Version: "v1", Name: "orderer", Point: PointCandidatesReady, Orderer: tc.fn}); err != nil {
				t.Fatal(err)
			}
			if err := r.Seal(); err != nil {
				t.Fatal(err)
			}
			_, err := r.OrderCandidates(deadlineContext(t), Event{Point: PointCandidatesReady}, []string{"a", "b"})
			if !errors.Is(err, ErrInvalidPermutation) || (tc.name == "error" && !errors.Is(err, ErrHookFailed)) ||
				strings.Contains(err.Error(), "private detail") || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("OrderCandidates error = %v, want generic fail-closed ErrInvalidPermutation", err)
			}
		})
	}
}

func TestOrdererDeadlineIsClassified(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Registration{Version: "v1",
		Name:  "cooperative-timeout",
		Point: PointCandidatesReady,
		Orderer: func(ctx context.Context, _ Event, ids []string) ([]string, error) {
			<-ctx.Done()
			return ids, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := r.OrderCandidates(ctx, Event{Point: PointCandidatesReady}, []string{"a"}); !errors.Is(err, ErrInvalidPermutation) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired orderer error = %v, want ErrInvalidPermutation and context.DeadlineExceeded", err)
	}
}

func TestObserverFailureIsReportedAfterCommitWithoutChangingEvent(t *testing.T) {
	r := NewRegistry()
	committed := Event{Point: PointOperationCommitted, OperationID: "op-1", Payload: []byte("committed")}
	if err := r.Register(Registration{Version: "v1",
		Name:  "observer",
		Point: PointOperationCommitted,
		Observer: func(_ context.Context, event Event) error {
			event.Payload[0] = 'x'
			return errors.New("Authorization: private-secret")
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Registration{Version: "v1",
		Name:     "panic-observer",
		Point:    PointOperationCommitted,
		Observer: func(context.Context, Event) error { panic("observer detail") },
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	failures := r.Notify(deadlineContext(t), committed)
	if len(failures) != 2 || failures[0] == nil || failures[1] == nil ||
		!errors.Is(failures[0], ErrObserverFailed) || !errors.Is(failures[1], ErrObserverFailed) ||
		strings.Contains(failures[0].Error(), "private-secret") || strings.Contains(failures[1].Error(), "observer detail") {
		t.Fatalf("Notify failures = %v, want two isolated observer failures", failures)
	}
	if string(committed.Payload) != "committed" || committed.OperationID != "op-1" {
		t.Fatalf("observer mutated committed event: %+v", committed)
	}
}

func TestObserverDeadlineExpirationIsReported(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Registration{Version: "v1",
		Name:  "cooperative-timeout",
		Point: PointOperationCommitted,
		Observer: func(ctx context.Context, _ Event) error {
			<-ctx.Done()
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	failures := r.Notify(ctx, Event{Point: PointOperationCommitted})
	if len(failures) != 1 || !errors.Is(failures[0], context.DeadlineExceeded) {
		t.Fatalf("Notify failures = %v, want deadline failure", failures)
	}
}

func TestSealedRegistrySupportsConcurrentReads(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Registration{Version: "v1", Name: "guard", Point: PointRequestValidated, Guard: func(context.Context, Event) GuardDecision { return GuardDecision{Allowed: true} }}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := r.Check(ctx, Event{Point: PointRequestValidated}); err != nil {
				t.Errorf("concurrent Check: %v", err)
			}
		}()
	}
	wg.Wait()
}
