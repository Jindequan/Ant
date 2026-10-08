# ANT Runtime Hooks

Version: V0.18 · Date: 2026-10-04 · Status: the registration framework explicitly distinguishes pre-commit extension points from post-commit Observer points, supports core-validated custom data transformation, sealed manifest/revision, bounded event snapshots, and an in-process bounded Observer Dispatcher; Router/Ledger/Transport lifecycle wiring and the durable outbox are still not accepted.

## 1. Goals and users

The audience is ANT's adapter and runtime extension developers. Hooks let a deployment add controlled checking, ordering, data transformation, and observation behavior without copying the Router, Ledger, or Transport. Business requests remain the responsibility of the ANT core for authorization, state transitions, resource allocation, single send, accounting, and recovery.

Given a set of candidates that have already passed core authorization and admission filtering, When a candidate-ordering Hook is configured, Then it may only return a permutation of those candidates; missing, duplicate, or added candidates cause a rejection.

Given an execution about to enter a rejectable checkpoint, When the Guard allows, Then the core continues with all of its own original checks; When the Guard rejects or times out, Then the core stops safely and records a stable reason, releasing or granting no additional permissions.

Given a ledger transaction that has committed, When an Observer receives the event, Then the Observer can read the redacted event and produce external notifications; its failure cannot roll back, rewrite, or falsify the committed business result.

## 2. Hook types and injection points

Hooks are internal Go interfaces, registered explicitly by the composition root; no dynamic loading of unknown code. At startup the registry may declare named checkpoints beyond the built-in points, or append type capabilities to existing points; each point chooses one of pre-commit decision/transformation or post-commit notification, after which the registry is frozen. Pre-commit points may select Guard, CandidateOrderer, Transformer; Observers can only be registered at built-in post-commit points or custom post-commit points declared via `DefinePostCommitPoint`. New points or capabilities take effect only when the core invokes the corresponding lifecycle boundary; they cannot automatically insert themselves into code paths not yet wired. All calls have a context deadline, stable ordering, and clear error semantics.

| Checkpoint | Registrable types | What the Hook can influence | What the core still owns |
|---|---|---|---|
| `request.validated` | Guard | reject the request | identity, permissions, normalization, fingerprint, and deduplication |
| `candidates.ready` | CandidateOrderer | reorder the candidates the core filtered out | hard admission, pool permission, resource identity, priority lower bounds, and final transaction validation |
| `candidate.before_prepare` | Guard | reject a candidate | atomic reservation, version and unit validation |
| `attempt.before_authorize` | Guard | reject the request for a send permit | permit, budget, real-time authorization, and resource re-check |
| `dispatch.before_send` | Guard | reject this send | Transport single-send permit consumption, request fingerprint, credentials, and the single network send |
| `report.before_settle` | Guard | reject untrusted reports from entering settlement; route them into the existing error/reconciliation convergence | result priority, metering projection, terminal state, and atomic ledger commit |
| `operation.claimed`, `decision.committed`, `attempt.prepared.committed`, `attempt.settled.committed`, `operation.committed`, `config.committed` | Observer | produce notifications/metrics/external side effects | the original transaction result; Observers cannot modify or revoke facts |

Executing user Hooks inside a transaction, while holding locks, during secret resolution, or inside the send entry point is forbidden. Hooks must not obtain references to SQL, Store, Ledger, Transport, SecretResolver, or mutable domain objects. The composition root can add pre-commit named points via `Registry.DefinePoint`, read-only notification points via `Registry.DefinePostCommitPoint`, or append same-phase type capabilities to existing points via `Registry.EnableCapabilities`; the registry rejects cross-phase additions of Observer, Guard, Orderer, or Transformer. The business core must invoke the point at the exact lifecycle boundary in the implementation. Built-in points do not enable the Transformer by default; only after the invoking points and domain validators have passed A30 acceptance may the composition root explicitly enable this capability. Arbitrary string points must not be used to bypass admission/permit/ledger boundaries.

### 2.1 Production wiring today (verified 2026-10-08, ENG-08)

The table above is the contract. What the shipped binary actually invokes is narrower; do not infer end-to-end lifecycle extension from it:

| Point / capability | Invoked in production | Where |
|---|---|---|
| `candidates.ready` (CandidateOrderer) | yes | `application/operation_executor.go` (`OrderCandidates`) |
| `candidate.before_prepare`, `attempt.before_authorize`, `dispatch.before_send`, `report.before_settle` (Guard) | yes | `application/operation_executor.go` (`checkLifecycleGuard`) |
| `request.validated` (Guard) | **no** call site | — |
| Transformer capability | **no** call site | — |
| Six post-commit Observer points | **no**: `ObserverDispatcher` is constructed only in tests; there is no persistent outbox | — |
| Permit consumption | not a hook point: a direct ledger/transport callback | `ledger` permit status |

The composition root (`cmd/ant-server/main.go`) creates `hooks.NewRegistry()` and seals it **empty**: no hook is registered by default, so the five invoked points are pass-through in every shipped configuration. Wiring the remaining points belongs to RUN-04's gated scope.

## 3. Behavioral contracts

### Guard

- May only return allow or a rejection with a stable classification; it cannot change requests, candidates, resource plans, permits, metering, or results.
- Failure, panic, timeout, or unknown classification always means rejection; core validation outside the checkpoint is still fully executed. If a deadline expiry coincides with a callback error/panic, the error keeps the deadline classification and callback details are not propagated outward.
- The Guard's allow result must not be treated as authorization evidence. Even after `dispatch.before_send` passes, the Ledger's and Transport's original permit/CAS paths must still be followed.
- Input is a redacted, immutable, versioned snapshot. The caller is responsible for writing the Hook version and the decision summary into the corresponding audit/decision records; until that wiring is complete, Guard results must not be used for production sends.

### CandidateOrderer

- May only arrange candidates that the core has already passed through hard filtering, and must return exactly the same set of candidate IDs, each ID exactly once.
- The core re-checks current state at atomic prepare and send authorization; ordering must not create candidates, bypass disabled/hold/affinity, change resource ownership, or skip manual priority hard rules.
- On error, timeout, panic, or an illegal permutation, the planning round is rejected; silently falling back to default ordering is not allowed. The callback's raw error text must not propagate to the caller; a stable error classification is returned to avoid accidentally leaking credentials or request content.
- The same input, in the same input order, must produce reproducible results. Decision records need to save the ordering rule ID/version and the final selection for historical explanation.

### Transformer

- Usable at built-in or custom lifecycle points explicitly enabled by the composition root. A Transformer receives the event snapshot and a copy of the current data, and returns the next version of the data; the registry chains transformers in stable order. The core passes in only the minimal data required by that phase — no secrets, raw prompts, or unredacted request/response bodies.
- The core must supply a validator; the registry validates the initial data first, then validates the result after each transformer. The validator must perform the complete structural, permission, and domain-invariant checks required by that phase, not merely JSON parsing. Built-in points do not open this capability by default.
- Callback errors, panics, deadline expiry, or a validator rejection reject the entire transformation chain; no partial result is returned, and the raw error text of callbacks and validators does not propagate. The caller must hand the final validated data to the core's original flow; Transformer results must not be treated directly as authorization, permits, terminal states, or ledger facts.

The invoking point is responsible for passing the domain validator into the registry; the registry does not provide a validation policy that Hooks can override:

```go
point := hooks.Point("router.after_plan")
if err := registry.DefinePoint(point, hooks.CapabilityTransformer); err != nil {
	return err
}
// Observer extension points are declared separately, and can only be invoked from committed, unlocked core boundaries.
if err := registry.DefinePostCommitPoint("router.plan.committed"); err != nil {
	return err
}
if err := registry.Register(hooks.Registration{
	Name: "deployment-plan-adjustment", Point: point,
	Transformer: adjustPlan,
}); err != nil {
	return err
}
if err := registry.Seal(); err != nil {
	return err
}
adjusted, err := registry.Transform(ctx, event, encodedPlan, validateCorePlan)
if err != nil {
	return err // do not send; keep the core validation and Ledger path
}
```

### Observer

- Receives immutable events only after the transaction commits; changing business results or synchronously blocking the execution main path is forbidden.
- Observers must run after the transaction commits and after core locks are released. The main path must not synchronously wait on Observers; Observer errors must go into the run log/observability channel and be retryable or alertable, never silently swallowed. Callback error text must not propagate; callers may rely only on stable error categories and Hook names, and deadline errors keep the context classification.
- `ObserverDispatcher` provides a fixed worker count, fixed queue capacity, non-blocking `Submit`, event snapshots, per-event deadlines, shutdown draining, and statistics; the worker cap is 64 and the queue cap is 16,384. When the queue is full, `ErrObserverQueueFull` is returned immediately, and the caller must wire that failure into alerting/retry policy and must not treat it as success; a legitimate post-commit point with no corresponding Observer is a no-op. Result handlers run on workers and must return quickly; their panics are isolated and counted.
- This queue lives only in process memory; delivery is not guaranteed across process crashes or parent context cancellation. Reliable delivery must be driven by a durable outbox; in-process callbacks must not masquerade as a reliable event bus. First-version outbox wiring is a T5/T6 decision item.

## 4. Ordering, lifecycle, and versions

Registration keys are globally unique; every registration must have a stable `Name` and `Version`; within one checkpoint, calls are ordered by `priority` ascending, then by stable `name` lexicographically. Hook authors must increment `Version` whenever semantics change. `Seal` produces a deterministic manifest covering the Hook API version, the effective registration budgets, checkpoints, phases, capabilities, registration names/versions/types, and priorities; `Registry.Manifest` returns a tamper-resistant copy without callback code, and `Registry.Revision` returns its SHA-256. The sealed manifest does not hash executable code, so semantic code changes must be accompanied by a version bump. Each event's `APIVersion` and `RegistryRevision` are filled in automatically by the Registry as the sealed API version and the current sealed digest and cannot be forged by the caller; the composition root may persist the same revision together with the execution decision, so that the applicable set of Hook versions can be reconstructed from the manifest.

`NewRegistry` defaults to at most 64 custom points, 32 Hooks per point, Transformer input and per-step output at most 1 MiB, and event snapshot `Payload` at most 1 MiB; the effective budgets are also written into the manifest and counted in the revision; even with an identical registration list, different actual limits yield different digests. The composition root can use `NewRegistryWithOptions` to set budgets, explicitly 0 to forbid non-empty event payloads, or start from `DefaultRegistryOptions` and override individual values. When an event exceeds its budget, the corresponding callback does not run: Guard/Orderer/Transformer reject per their own fail-closed semantics, and an Observer returns a classifiable error; when that phase has no corresponding callback, the core operation is not rejected over an unused payload. If an event budget error and a deadline coincide, all Hook types uniformly prefer the context error classification. Once the registry is frozen, runtime additions and removals are forbidden; updates take effect through a new composition-root version and an explicit recompilation. At startup it also rejects duplicate keys, empty names, missing versions, undeclared points, and Hook types unauthorized at that point.

Guards and ordering Hooks must be deterministic, fast, network-free pure decision logic. All Hooks are trusted in-process code and must respect the deadline/context provided by the caller; the deadline is cooperative — the registry checks before and after the call and fails closed, but cannot forcibly terminate a blocking function that ignores the context. The framework provides no sandbox for untrusted code. Observer points must also be placed by the core after the durable commit and after lock release. Networks, identity catalogues, balances, metering, and provider facts should be obtained through existing ports such as `DomainGuard`, `CapacityReader`, and `ProviderAdapter`, then handed to the core as versioned, time-bounded evidence; Hooks must not become another invisible IO layer.

Each event's Hook API version is injected by the Registry and included in the manifest/revision; events also carry the point, operation/attempt references, configuration and rule revisions, the time of occurrence, and a redacted snapshot. The registry limits the `Payload` to the configured byte budget; the composition root remains responsible for minimizing and redacting fields — the length limit must not be treated as content redaction. Secrets, Authorization headers, raw prompts, response bodies, or arbitrary SQL must not be included. Event JSON is an audit input; Hooks must not write instructions into the snapshot for the core to interpret.

## 5. Go interface and acceptance boundary

Implementation entry point: `server/internal/hooks`. The framework provides registration validation, deterministic ordering, deadline requirements, Guard rejection aggregation, Observer failure reporting, candidate permutation validation, core-validated custom data transformation, and panic isolation; the application layer must not use the unvalidated registration list directly. `Registry.Notify` is a synchronous primitive; `ObserverDispatcher` is the currently provided in-process bounded background worker implementation. The core must still call `Submit` at the real commit-unlock boundary; RUN-04 wiring and reliable delivery are not yet implemented.

Foundation-layer acceptance covers: duplicate registration, unknown checkpoints, custom checkpoint capability constraints, pre-/post-commit phase isolation, stable priority, Guard rejection/panic, Observer errors not changing committed results, illegal candidate permutation rejection, candidate ID set conservation, event payload caps and rejection paths, immutability of redacted snapshots, and concurrent-read safety of the registry. Real Router/Ledger/Transport invocation points are not yet covered. T2 wires the checkpoints into the application chain; T3 verifies failure and ledger invariants; T5 decides whether a reliable Observer needs an outbox. The existence of this document and the framework does not mean these runtime acceptances have passed.
