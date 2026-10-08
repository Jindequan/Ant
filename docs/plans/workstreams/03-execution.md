# Ledger, Execution, and Protocol

Date: 2026-10-03. Parent plan: [Task Tree](../TASK-TREE.md). Original phase card: [02-VERTICAL-SLICE.md](../02-VERTICAL-SLICE.md). Actual status lives only in [STATUS](../STATUS.md).

LED commands proceed in dependency order and are integrated by the ledger owner; SQL still appears only in `store/sqlite`, and LED-01 chains real SQLite transactions by adding a compatible typed claim extension without modifying the WriterTx/Store signatures frozen in T1. The Store surface only exposes the minimal methods required by these commands. RUN-01/02 may first deliver controlled artifacts; real sending must wait for LED-05 and the full authorization chain. API route registration, main, and shared interfaces are wired uniformly by the integrator.

Paths in the tables are repository-relative paths; directories/files that do not exist are planned new locations, and `<selected-provider>`/`<second-provider>` are replaced with real adapter names after IN-03. Paths are the upper bound of the writable scope; before starting work they must be narrowed to the item's actual file list. Shared files remain subject to the single-writer lock.

## LED-01 · Operation identity and execution-authority transaction

- **Responsibility/scope:** ledger (including the narrowed Store transaction primitives); T2.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-04, BASE-05`; phase gate `G1-DEV`.
- **Writes:** `server/internal/store/claim.go`, `server/internal/store/sqlite/claim_statements.go`, `server/internal/ledger/commands/claim.go`, `server/tests/integration/claim_test.go`, `docs/evidence/WORK/LED-01/`.
- **Deliverables:** claim/receipt/same-or-different summary/owner/single active attempt; only a compatible typed Store claim extension is added, and all SQL remains confined to store/sqlite.
- **Acceptance criteria:** same ID with same semantics is reused; different semantics conflict; across multiple processes only one execution authority is granted; no half-commit.
- **Verification:** concurrency on a local SQLite real database; A01/A02.

## LED-02 · Full-resource preparation and send authorization

- **Responsibility/scope:** ledger; T2.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `LED-01, CFG-07`; phase gate `G1-DEV`.
- **Writes:** `server/internal/ledger/commands/prepare.go`, `server/internal/ledger/commands/authorize.go`, `server/internal/ledger/validate_change.go`, `server/internal/store/prepare.go`, `server/internal/store/sqlite/prepare_statements.go`, `server/tests/integration/prepare_test.go`, `docs/evidence/WORK/LED-02/`.
- **Deliverables:** quote/tryPrepare/authorize/consume persistent CAS and I01–I10 validation.
- **Acceptance criteria:** failure of any resource/version/policy rolls back everything; permits are consistent with budget; the network never enters the transaction.
- **Verification:** capacity/version/permit races on a local SQLite real database + run with the external network disabled; A03/A16/A20.

## LED-03 · Settlement and result event persistence

- **Responsibility/scope:** ledger; T2.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `LED-02`; phase gate `G1-DEV`.
- **Writes:** `server/internal/ledger/commands/settle.go`, `server/internal/ledger/commands/settle_test.go`, `server/internal/domain/planning.go`, `server/internal/domain/planning_report_test.go`, `server/internal/domain/quantity.go`, `server/internal/domain/quantity_test.go`, `server/internal/domain/types.go`, `server/internal/domain/result_resolver.go`, `server/internal/domain/result_resolver_test.go`, `server/internal/ledger/commands/abort.go`, `server/internal/ledger/commands/sweep.go`, `server/internal/store/settle.go`, `server/internal/store/sqlite/settle_statements.go`, `server/internal/contracttest/contract_test.go`, `contracts/fixtures/api/manifest.json`, `contracts/openapi.yaml`, `web/src/api/schema.d.ts`, `web/src/api/types.ts`, `web/tests/contract.test.ts`, `docs/design/contracts.go`, `docs/design/02-STORAGE.md`, `docs/design/03-EXECUTION.md`, `docs/plans/03-CORRECTNESS.md`, `docs/plans/execution/T2.md`, `docs/plans/STATUS.md`, `docs/plans/workstreams/03-execution.md`, `docs/evidence/WORK/LED-03/`.
- **Deliverables:** single settlement, real overage, consumption/reservation separation, results and events committed together.
- **Acceptance criteria:** repeated reports never double-deduct; conflicts do not overwrite; unknown holds the slot; values beyond int64 are kept exactly, reservations are released, resources are isolated, results are explicit with no fictitious accounting; atomic commit within the same transaction.
- **Verification:** report/resource/event/hold/idempotency checks on a local SQLite real database; full race and contract validation; A06/A08/A22/QA1-OVERAGE.

## LED-04 · Cancellation, renewal, sweep, and unsent convergence

- **Responsibility/scope:** ledger; T2.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `LED-03`; phase gate `G1-DEV`.
- **Writes:** `server/internal/ledger/commands/cancel.go`, `server/internal/ledger/commands/renew.go`, `server/internal/ledger/commands/sweep.go`, `server/internal/ledger/commands/abort.go`, `server/internal/ledger/commands/recovery_test.go`, `server/internal/ledger/commands/settle.go`, `server/internal/ledger/commands/settle_test.go`, `server/internal/ledger/commands/prepare.go`, `server/internal/ledger/permit.go`, `server/internal/ledger/permit_test.go`, `server/internal/store/recovery.go`, `server/internal/store/sqlite/recovery_statements.go`, `docs/evidence/WORK/LED-04/`.
- **Deliverables:** the AbortBeforeDispatch transaction, bounded recovery, and stale-owner rejection.
- **Acceptance criteria:** all six race classes have stable outcomes; a lease is not an external termination; only reliably-unsent work is released; the permit registry has a lifecycle.
- **Verification:** fault/race scenarios on a local SQLite real database + run with the external network disabled; A04/A05/A15/A20.

## LED-05 · Ledger and activated-configuration integration acceptance

- **Responsibility/scope:** independent verification; T2.2; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `LED-04, CFG-08, API-01, WEB-02`; phase gate `G1`.
- **Writes:** `server/tests/integration/ledger_slice_test.go`, `docs/evidence/WORK/LED-05/`.
- **Deliverables:** the minimal closed loop of real activated configuration → prepare → permit → report → settle/recovery.
- **Acceptance criteria:** rows/events/summary/occupancy are checked together; acceptance does not rely on a bare 200 response; only after passing is the real send path connected.
- **Verification:** make test-integration; the relevant race contract tests.

## RUN-01 · First-provider Adapter and domain policy

- **Responsibility/scope:** providers; T2.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `IN-05, BASE-02`; phase gate `G1`.
- **Writes:** `server/internal/adapters/<selected-provider>/`, `server/internal/domain/policy/`.
- **Deliverables:** pure encode/decode, enforced upper bounds, report/error mapping, and domain guards.
- **Acceptance criteria:** only verified capabilities are handled; no HTTP/SQL/secret reads; no billed item is undercounted; mail/AI accepted per applicable items.
- **Verification:** real redacted material + the shared adapter contract; no actual sending.

## RUN-02 · Controlled single-shot HTTP Transport

- **Responsibility/scope:** transport; T2.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-04, RUN-01`; phase gate `G1`.
- **Writes:** `server/internal/transport/`.
- **Deliverables:** prepare fixed bytes/credential digest, consume port wiring, and no hidden replays.
- **Acceptance criteria:** the controlled server actually receives the wire at most once; redirects/stale connections do not replay; revocation/digest mismatch is rejected.
- **Verification:** httptest/actual socket counting + race; A20.

## RUN-03 · Bounded execution host and persistent output

- **Responsibility/scope:** runtime; T2.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `LED-05`; phase gate `G1`.
- **Writes:** `server/internal/runtime/host.go`, `server/internal/runtime/supervisor.go`, `server/internal/runtime/stream_hub.go`.
- **Deliverables:** bounded tasks/buffers, renewal sweep, output saving, shutdown recovery, and completion.
- **Acceptance criteria:** settlement proceeds even if the consumer disconnects; overload stays bounded; server exit does not fabricate provider termination; on storage failure output is retrievable by the original ID.
- **Verification:** bounded/zero-residue/storage-failure cases; AI plus backpressure long stream.

## RUN-04 · Router basic candidates and execution assembly

- **Responsibility/scope:** execution integration; T2.1/T2.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `CFG-08, LED-05, RUN-01, RUN-02, RUN-03`; phase gate `G1`.
- **Writes:** `server/internal/application/router.go`, `server/internal/application/router_test.go`, `server/internal/hooks/`, `server/cmd/ant-server/`.
- **Deliverables:** a single filter/priority/prepare/permit/send/settle chain, the persistent DecisionRecord, and the wiring of built-in and custom lifecycle Hooks; the sealed registry revision and per-phase decision digests are recorded.
- **Acceptance criteria:** hard constraints take precedence; the named Hook points declared by built-ins and the composition root are capability-restricted and connected to real boundaries; ordering is stable, Guards only reject, ordering preserves the candidate set, Observers only read committed events; only explicitly-opened Transformers may rewrite phase data and every step passes the core validator; the manifest/revision is stable and persisted in the DecisionRecord, from which the actual Hook IDs/versions can be restored; per-phase decision digests are auditable. Hook failure policy is explicit; no capacity means fail fast; quote makes no reservation; no send entry point bypasses the Ledger.
- **Verification:** controlled full chain + built-in/custom Hook-point failures and permission boundaries / A30 / actual send versus accounting cross-check.

## API-01 · Configuration catalogue and pool priority API

- **Responsibility/scope:** API; T2.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `AUTH-01, CFG-08`; phase gate `G1`.
- **Writes:** `server/internal/api/config.go`, `server/internal/api/providers.go`, `server/internal/api/pools.go`.
- **Deliverables:** draft/catalogue/activate/GET and whole-group priority routes.
- **Acceptance criteria:** authorization/schema/body limits/version consistency; same priority is saved as-is; 409 keeps the old version; secrets are never echoed back.
- **Verification:** real HTTP + real-database GET; A26/A28.

## API-02 · Operation submit, cancel, and query API

- **Responsibility/scope:** API; T2.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `AUTH-01, RUN-04`; phase gate `G1`.
- **Writes:** `server/internal/api/operations.go`, `server/internal/application/query_admin.go`.
- **Deliverables:** submit/getOperation/inspect/cancel and the persistent result projection.
- **Acceptance criteria:** HTTP acceptance and execution outcome are separated; a cancel ACK is not a fake terminal state; queries never send; replay by original ID has no side effects.
- **Verification:** real HTTP/queries/error branches; A02/A12/A15.

## API-03 · Authorization SSE and reconnect backfill

- **Responsibility/scope:** API/runtime; T2.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `API-02, RUN-03`; phase gate `G1`.
- **Writes:** `server/internal/api/events.go`, `server/internal/runtime/subscriptions.go`.
- **Deliverables:** persistently sequenced events, authorized reconnection, missed-notification backfill, and bounded subscriptions.
- **Acceptance criteria:** persistence precedes push; duplicates/gaps are recoverable; slow consumers do not block settlement; a disconnect does not trigger a new request.
- **Verification:** real HTTP/SSE/sockets cross-checked against persistent events; A11/A29.

## RUN-05 · First-binding real full-chain acceptance

- **Responsibility/scope:** independent verification/integration; T2.4; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `WEB-06, API-03`; phase gate `G1`.
- **Writes:** `server/tests/live/`, `web/e2e/first-binding.spec.ts`, `docs/evidence/WORK/RUN-05/`.
- **Deliverables:** one final version across UI → Go → database → real provider → results, plus replay.
- **Acceptance criteria:** actions stay within the authorized scope; request IDs/actual consumption are cross-checkable; one failure/recovery path; localtest cannot substitute.
- **Verification:** real provider redacted evidence + browser journey.
