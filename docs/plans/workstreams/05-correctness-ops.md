# Correctness and Operations Disposition

Date: 2026-10-03. Parent plan: [Task Tree](../TASK-TREE.md). Original phase cards: [03-CORRECTNESS.md](../03-CORRECTNESS.md) / [05-OPERATIONS-AND-ONBOARDING.md](../05-OPERATIONS-AND-ONBOARDING.md). Actual status lives only in [STATUS](../STATUS.md).

The QA owner writes independent counterexamples/fault tooling; module owners fix production code. OPS-01 implements the accounting mechanism first, and OPS-02 and WEB-04 wire it up for reuse; the same administrative transaction is not rewritten in T5. T3 can be verified early against already-completed backend leaves; phase exit still requires G2.

Paths in the tables are repository-relative paths; directories/files that do not exist are planned new locations, and `<selected-provider>`/`<second-provider>` are replaced with real adapter names after IN-03. Paths are the upper bound of the writable scope; before starting work they must be narrowed to the item's actual file list. Shared files remain subject to the single-writer lock.

## QA-01 · Resource atomicity and invariant verification

- **Responsibility/scope:** independent correctness verification; T3.1; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `LED-05, CFG-07`; phase gate `G1`.
- **Writes:** `server/tests/integration/resource_invariants_test.go`, `server/tests/fault/resource_test.go`.
- **Deliverables:** I01–I10, illegal and legal cases for capacity/window/unknown reservations.
- **Acceptance criteria:** 50 admitted out of 100 requests with capacity 50, exactly; slot1 unknown still blocks; overage is accounted truthfully; no cross-pool capacity gain.
- **Verification:** multi-process/race on the real database; A03/A07/A08/A22/A23.

## QA-02 · Crash, cancellation, and send race verification

- **Responsibility/scope:** independent fault verification; T3.2; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `RUN-04, API-03`; phase gate `G1`.
- **Writes:** `server/tests/fault/lifecycle_test.go`, `server/tests/fault/transport_test.go`.
- **Deliverables:** faults at each of claim/prepared/executing/report/settle and the six cancellation race classes.
- **Acceptance criteria:** a stale owner never revives; zero sends across leases before validation, conservative handling after; stream disconnects are bounded; no double deduction.
- **Verification:** make test-fault; real processes/sockets/resource sets; Hooks deadline, panic, fail-closed, step-by-step core validation of Transformers with no partial results, and post-commit isolation of Observers (A30).

## OPS-01 · Observation, verification, and authorization accounting management mechanism

- **Responsibility/scope:** runtime accounting; T3.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `LED-05, AUTH-01, API-02`; phase gate `G1`.
- **Writes:** `server/internal/application/observation_service.go`, `server/internal/ledger/commands/admin.go`, `server/internal/api/admin.go`.
- **Deliverables:** refresh/adjust/reconcile/clearHold core transactions, collection, and audit.
- **Acceptance criteria:** collection happens outside the transaction; subject/version/watermark checks; observation only tightens; clearing a hold releases neither unknown nor resends; permissions are idempotent.
- **Verification:** real database + controlled collection; A08/A13/A25.

## QA-03 · Configuration, permission, and storage fault verification

- **Responsibility/scope:** independent fault verification; T3.3; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `OPS-01, API-01, API-03`; phase gate `G1`.
- **Writes:** `server/tests/fault/config_security_test.go`, `server/tests/fault/storage_test.go`.
- **Deliverables:** counterexamples for revoked permissions/reordering/metering upgrades/expired observations/commit-response-drop/backup gaps.
- **Acceptance criteria:** an old prepared plan cannot be relaxed; over-privileged actions fail; store retries never resend; no capacity means fail fast; mail guards per applicable items.
- **Verification:** make test-fault; A13/A14/A16/A18/A21/A24/A25.

## QA-04 · Correctness integration and D06 runtime closure

- **Responsibility/scope:** independent verification; T3.4; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `QA-01, QA-02, QA-03`; phase gate `G2`.
- **Writes:** `docs/evidence/WORK/QA-04/`.
- **Deliverables:** the complete applicable I/A mapping, real deployment fault conclusions, and D06 verification.
- **Acceptance criteria:** no fakes substitute for multi-process real database; every applicable T3 mechanism has evidence; unverified business journeys get explicit follow-ups.
- **Verification:** final relevant race/fault/integration runs and matrix cross-check.

## OPS-02 · Runtime query and repair service wiring

- **Responsibility/scope:** runtime API; T5.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `OPS-01, API-02`; phase gate `G2`.
- **Writes:** `server/internal/application/query_admin.go`, `server/internal/api/admin.go`.
- **Deliverables:** historical/current projections, verification evidence, and complete wiring of the administrative runtime interface.
- **Acceptance criteria:** the OPS-01 accounting transactions are reused; cancel/adjust are not rebuilt; authentication/billing/unknown have actionable recovery paths.
- **Verification:** real HTTP/permissions/idempotency and the runbook; A12/A19/A25.

## OPS-03 · Output retention and backup recovery

- **Responsibility/scope:** storage operations; T5.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `OPS-02, RUN-03, IN-05`; phase gate `G3`.
- **Writes:** `server/internal/runtime/output_store.go`, `server/internal/store/sqlite/backup.go`, `server/internal/runtime/restore_guard.go`, `docs/operations/`.
- **Deliverables:** D05 body/metadata retention, access control, and the backup/recovery send gate.
- **Acceptance criteria:** old backup gaps close the send gate; dedup and unknown evidence are never deleted; missing bodies are stated honestly; secrets never leak.
- **Verification:** real-database backup/fault recovery + query after restart; A11/A13/A14.

## OPS-04 · Runtime journeys and D05 closure

- **Responsibility/scope:** independent runtime verification; T5.4; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `OPS-03, WEB-05, MULTI-03`; phase gate `G4`.
- **Writes:** `docs/evidence/WORK/OPS-04/`, `docs/operations/`.
- **Deliverables:** forward/cancel/failure-verification/repair journeys and retention-recovery evidence.
- **Acceptance criteria:** D05 holds in the two-account final form; no unknown resends; the manual is reproducible; known-unverified items are listed explicitly.
- **Verification:** real-stack runtime journeys + T5 exit cross-check.
