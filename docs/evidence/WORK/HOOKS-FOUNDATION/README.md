# Hooks foundation verification snapshot

Date: 2026-10-04. Scope: ANT internal `server/internal/hooks` registration and callback framework, the V0.18 contract, and PRD R25/A30 alignment.

## Results

- `cd server && go test -race -count=1 ./internal/hooks`: exit 0; fresh focused registry race run (`ant/server/internal/hooks`, 2.324s). Raw output: `hooks-focused-race.log` (SHA-256 `67b0122cb976fa87fbfcb1281d5ee5ca34f652ba0fe5298b32492ba4314fae00`).
- `make test-contract`: exit 0; fresh `-race -count=1` across all `server/internal/...` packages. Raw output: `make-test-contract.log`.
- `make check`: exit 0; formatting, vet, build, dependency boundaries, Web typecheck and generated guard checks. Raw output: `make-check.log`.
- `python3 docs/plans/check_tree.py`: exit 0; 55 task nodes and 8 gates. Raw output: `task-tree.log`.
- Source digests for the reviewed implementation and contracts: `source-sha256.txt`.

## Review finding

Registry registrations now require an explicit semantic version. Seal builds a deterministic, callback-free manifest with a SHA-256 revision; callbacks receive that sealed revision in their Event, and callers cannot forge it. The manifest includes the sealed Hook API version and all effective registry budgets, so a contract or budget change changes the revision. Registry-owned API version and revision values override caller-supplied values for all four callback types. Tests also confirm every Hook type receives the sealed revision, version changes affect it, registration order does not, and manifest mutation cannot escape its defensive copy. At this foundation-review stage, business DecisionRecord integration was not yet present; the later production connection is recorded below.

Transformer event-budget rejection previously failed to check whether the context deadline had expired while the registry snapshot was being read. The implementation now gives the context error precedence, matching Guard, Orderer and Observer. `TestTransformDeadlinePrecedesOversizedEventError` proves the deadline path skips both the validator and Transformer callback.

## Production candidate-order integration (2026-10-05)

The sealed registry is now passed from the server composition root into `CandidateOperationExecutor`. After the planner's core filtering, `PointCandidatesReady` orderers can reorder the eligible offer IDs. The event payload contains only offer and provider-type IDs; it omits request payload, endpoint, and credential revision. The registry enforces an exact permutation, and Hook errors, cooperative deadline expiry, or invalid candidate identity fail the operation before quote, prepare, or send. The empty production registry preserves the planner's existing order until trusted registrations are added at composition time.

`TestCandidateOperationExecutorUsesHookOrderWithoutExposingRequestOrSecrets` and `TestCandidateOperationExecutorFailsClosedOnInvalidHookOrder` cover selection, payload redaction, and no-send failure behavior. The selected order, registry manifest/revision, candidate metadata, and selected offer are passed into the preparation transaction and stored in the separate `decision_records.trace_json` column. Schema v3 adds the column and tests the v2-to-v3 upgrade. The namespace-scoped `GET /v1/operations/{operationId}/decisions` projection reads only persisted trace data and exposes it in the operation detail UI; history is not recomputed against current configuration. Application/API tests, SQLite integration coverage, and a Web regression cover the read-back. Lifecycle Guards at the other pre-commit boundaries, post-commit event submission, and a durable outbox remain unimplemented; this does not complete A30/RUN-04.

## Acceptance boundary

The foundation checks cover the bounded registry, named pre-commit and post-commit points, capability phase isolation, versioned registrations, effective budget/API version capture, deterministic manifest/revision generation and event injection, Guard/Orderer/Transformer/Observer behavior, transformation and event payload budgets, failure/deadline handling, race-safe registry reads, production candidate ordering, atomic trace persistence with attempt preparation, and namespace-scoped read-back of historical traces through the API and operation UI. Other lifecycle points and durable post-commit delivery are not included, so A30/RUN-04 remains incomplete.

## Follow-up foundation review: bounded Observer Dispatcher

The synchronous `Registry.Notify` had no in-package bounded worker implementation. Added `ObserverDispatcher` with fixed workers and queue capacity (maximum 64 workers and 16,384 queued events), nonblocking post-commit `Submit`, immediate payload-budget rejection, defensive event snapshots, per-event cooperative deadlines, graceful queue drain, close-time cancellation, worker result reporting, and queue/failure/panic statistics. Queue saturation returns `ErrObserverQueueFull`; it is not silently treated as success. Handler panics are isolated so the worker survives. The process-local queue is volatile and does not replace a persistent outbox.

- `cd server && go test -race -count=1 ./internal/hooks`: PASS. The focused run was executed before the package-wide run; the package-wide run below is the final source state.
- `make test-contract`: PASS; fresh race tests across all `server/internal/...`. Raw output: `make-test-contract-observer-dispatcher.log` (SHA-256 `50e717b95744afd940f737483fc2ca5141e6fb75422f406a299390d459f05357`).
- `make check`: PASS; raw output: `make-check-observer-dispatcher.log` (SHA-256 `964f78b194dbb23318dc7a96941cacc1bdd157895706c5a8355825a12b3796a7`).
- `python3 docs/plans/check_tree.py`: PASS; 55 tasks and 8 gates.
- Updated source digests are in `source-sha256.txt`.

This closes only the reusable in-process scheduling foundation. Fresh source search still finds no production import/callsite, durable audit record, or outbox integration; RUN-04/A30 remains incomplete.

## Follow-up foundation review: zero Transformer budget

The production-readiness review found that a registry with `MaxTransformBytes == 0` could still declare or enable `CapabilityTransformer`, even though registration/execution could never succeed. `DefinePoint` and `EnableCapabilities` now reject that unusable configuration up front and leave the registry unchanged. Regression coverage passes under focused Hooks race and full `make test-contract` (`-race` across all internal packages); `make check` also passes. Raw outputs and source digests for this follow-up are recorded in [the production-readiness audit](PRODUCTION-READINESS-AUDIT-2026-10-04.md).

This improves foundation configuration consistency only. It does not add a production call site, DecisionRecord persistence, observer worker/outbox, or close A30/RUN-04.
