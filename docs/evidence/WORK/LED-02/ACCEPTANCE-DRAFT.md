# LED-02 local transaction candidate

2026-10-03. State: **reviewable candidate, not independently accepted and not done**. G1-DEV only: SQLite, synthetic identities/resources/evidence, no credentials or network calls. The implementer previously reviewed LED-01 but is not an independent reviewer of this implementation.

## Changed

- `store/prepare.go`: additive PreparationReaderTx/PreparationWriterTx projections and rows, leaving frozen Store/WriterTx/ReaderTx method sets unchanged.
- `store/sqlite/prepare_statements.go`: consistent read bundle, typed resource/guard/observation/rolling-event projections, prepared attempt/plan/reservation writes, versioned resource deltas/events, executing/budget/permit writes, granted→consumed CAS and decisions. All SQL remains in store/sqlite.
- `ledger/commands/prepare.go`: runtime-private PreparationService, read-only Quote and all-resource TryPrepare; plan references/dimensions, current admission, required affinity, registered persisted offer coverage, quantities, bound evidence shape/expiry, counter/occupancy and provider-order waterfall basis checked before mutation. Receipt+plan+attempt+reservations+events+decision commit together.
- `ledger/commands/authorize.go`: persisted owner/generation/deadline/cancel/lease/pool/control/credential/guard checks, pinned allocations and spend policy checks, occupancy reserved→active, counter migration to an already initialized canonical current-window state, one persisted permit and budget increment. Consume marks its private local registry first, then rechecks current state and durably CASes exactly once.
- `ledger/validate_change.go`: preparation-specific I01–I06 mutation gate wraps the existing structural gate; this phase cannot modify committed/adjustment/uncertainty, mint capacity or perform settlement. I07 events/summary/receipts are atomic; I09 one attempt/permit and granted-budget conservation are enforced. I08 terminal resolution and I10 settlement/overrun remain LED-03/04.
- `tests/integration/prepare_test.go`: real-file SQLite tests and synthetic rows. Narrow Store/test/evidence additions were approved and registered by root. No shared permit/model/Store signature changes, no LED-03/04 implementation.

Authorization handles are issued inside the validated write callback and retained privately if SQL fails. Failed commands return an empty handle. Retry reuses the pending private handle, so the unchanged PermitRegistry.byAttempt rule does not permanently strand the attempt. No failed/ambiguous consume can regain local sending authority. Lifecycle cleanup remains LED-04.

## Review-driven corrections

The independent review identified and the integrator corrected four issues before final review: (1) authorization now checks fresh provider observations against the total outstanding reservation and rejects expired resources before window migration; (2) all preparation/authorization/consume timestamps are read from the injected authoritative clock inside the serialized writer callback, and production no longer mutates that clock from caller arguments; (3) private permit-handle map access is synchronized inside writer callbacks, including when the caller is cancelled; (4) admission-blocked outcomes now persist a bounded rejected decision record and durable replayable `blocked` response. The fake test clock is advanced explicitly by test fixture wrappers.

Directed regression tests cover the fresh-zero observation and expiry cases, writer-queue expiry, cancelled authorization retry, and persisted blocked decisions. The independent reviewer accepted the source for the constrained G1-DEV slice. One earlier independent race run exited 1, but its failure details were irretrievably truncated; it was followed by two independent passes, one complete verbose pass preserved at `/tmp/ant-led02-final-race.log`, and five fresh outbound-denied passes. No failure cause is inferred. Final findings and hashes are recorded in [independent review](INDEPENDENT-REVIEW.md).

## Evidence and scope

All commands ran under `sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' env GOPROXY=off GOSUMDB=off`.

- Final rerun after the review-driven corrections: `GOPROXY=off GOSUMDB=off go test -race -count=1 -timeout 300s ./tests/integration -run '^(TestPreparation|TestClaim)'`, exit 0; `go test -race -count=1 ./internal/...`, exit 0; root `make check`, exit 0. The focused tests also cover the new observation/expiry, queue-time expiry, cancellation retry, and bounded blocked-decision cases. Command output is in the current task transcript; the earlier named logs remain historical runs, not logs for this final source hash.
- Reproducibility after independent review reported one truncated exit-1 run: one complete verbose outbound-denied run and five additional fresh outbound-denied race runs passed against this stable source tree. The verbose transcript is `/tmp/ant-led02-final-race.log`; each repeat summary was `run=1..5 exit=0` with logs `/tmp/ant-led02-race-repeat-1.log` through `-5.log`. The first failure’s detailed output was not retained and cannot be diagnosed retrospectively.
- `expanded-race.log` and `initial-race.log` preserve prior successful directed runs. An earlier console-only run failed because duplicate automatic `#NN` subtest names became SQLite URI fragments in temporary paths; unique numbered subtest names fixed fixture isolation. Assertions were retained.
- `final-vet.log`: `go vet ./internal/ledger/... ./internal/store/... ./tests/integration/...`, exit 0.
- `final-deps.log`: `python3 scripts/check-deps.py`, exit 0, dependency boundaries OK (19 packages).
- `final-build.log`: `go build ./...`, exit 0.

§1.3/§1.4/§3.7/§3.8/§4.1/§4.6 evidence supports the local transaction candidate only. No API/Router/Transport wiring, supplier behavior, true activated configuration lifecycle, UI, deployment, full T2 closure or performance is claimed. Actual model identity was not exposed; inherited tool model settings were not changed.

## Explicit G1-DEV scope and integration obligations

This is the accepted local transaction slice, with explicit integration gaps rather than an all-boundaries completion claim:

1. Missing canonical window state is rejected without minting capacity. Authorization migration is implemented only when the target state already exists. Creation of reset/refill state from authoritative compiled rules needs integration ownership/contract confirmation; arbitrary preparation cannot invent a balance.
2. `WindowRuleRevision` currently must equal the persisted resource `SemanticsRevision` as a conservative local fixture convention. The model has no separate persisted rule-revision field. Root must confirm an authoritative mapping or replace this local convention before accepting plans with independent rule revisions; it is not a new frozen business rule.
3. Activated resource rows are treated as trusted configuration projections. BoundEvidence is supplied by the trusted compiler/runtime and checked for dimensions, quantity, enforcement fields and expiry; this package does not itself prove supplier enforcement. Complete verified Compiled→persisted-definition/plan provenance and live policy projection must be checked at integration, without exposing arbitrary plans as public API input.
4. Admission failures that map to business blocking conditions now persist bounded rejected DecisionRecords and replayable `blocked` replies. Internal failures and access violations remain typed errors. Terminal/cancel resolution is intentionally not implemented here because it belongs to LED-03/04.
5. Pending handle lifecycle cleanup, failed-consume recovery/abort, settlement/overrun and lease recovery remain their declared dependent leaves; current private handles are not returned after SQL failure and cannot enable real sending.

These constraints bound the local G1-DEV slice and are not evidence of real-provider or end-to-end integration acceptance. Preserve them as explicit obligations for the dependent ledger integration work. Files are released to root; no further edits will occur without assignment.

## Released source SHA-256

| File | SHA-256 |
|---|---|
| `server/internal/store/prepare.go` | `986ee63136155d76977e8844134ea744311c274e1ea5f136af07d3a65f1119a6` |
| `server/internal/store/sqlite/prepare_statements.go` | `939c8cc02684011dd89d33ffc2f18d13a90aa7a8cbb8e519041b1952e6d99aec` |
| `server/internal/ledger/commands/prepare.go` | `86d6f4f89c1f623f3c9044439461e9dfe228db671efdbfa749ca82e1449b9096` |
| `server/internal/ledger/commands/authorize.go` | `16f90cb8145053a002cf8b6ad145fb8ab025bbded1e8465d0868bc07046c88fb` |
| `server/internal/ledger/validate_change.go` | `6d3ccfac3ec3df9ec7e680b8d0048c27fdd8f5c004012a61c98013952cd6e176` |
| `server/tests/integration/prepare_test.go` | `c6dd9fc20ca6124928e9e50af0a53016fa78e64141fe12b60e8b2fb49fff03ac` |
