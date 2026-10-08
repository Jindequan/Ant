# Inputs and Engineering Foundation

Date: 2026-10-03. Parent plan: [Task Tree](../TASK-TREE.md). Original phase cards: [00-SCOPE-AND-ADMISSION.md](../00-SCOPE-AND-ADMISSION.md) / [01-CONTRACTS.md](../01-CONTRACTS.md). Actual status lives only in [STATUS](../STATUS.md).

IN is driven by real-world material; BASE reuses already-delivered engineering; absent new failures or contract changes, accepted code is not redone. When IN-02/03 both write shared documents such as BASELINE, the integrator merges the sections.

Paths in the tables are repository-relative paths; directories/files that do not exist are planned new locations, and `<selected-provider>`/`<second-provider>` are replaced with real adapter names after IN-03. Paths are the upper bound of the writable scope; before starting work they must be narrowed to the item's actual file list. Shared files remain subject to the single-writer lock.

## IN-01 · First domain, callers, and success boundary

- **Responsibility/scope:** business/integration; T0.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `no leaf prerequisites`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `docs/implementation/BASELINE.md`.
- **Deliverables:** the real request, the definition of success, failure handling, allowed testing actions, and first-domain applicability.
- **Acceptance criteria:** real sources are traceable; accepting a commit is not equated with final delivery; no credential plaintext.
- **Verification:** material cross-check; T0.1 manifest.

## IN-02 · Deployment, identity source, and tooling baseline

- **Responsibility/scope:** integration; T0.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `IN-01`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `docs/implementation/BASELINE.md`.
- **Deliverables:** the single deployment/Store choice, host liveness and permission source, D02/D07 records.
- **Acceptance criteria:** browser disconnect and server exit semantics are explicit; the tooling is actually compatible; cross-machine SQLite sharing is not assumed.
- **Verification:** environment verification; existing build/real-database evidence.

## IN-03 · Two-provider facts and admission

- **Responsibility/scope:** providers; T0.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `IN-01`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `docs/implementation/PROVIDER-EVIDENCE.md`.
- **Deliverables:** two real accounts/bindings/resources, sharing relationships, enforced upper bounds, and termination/observation grounds.
- **Acceptance criteria:** every billed dimension, window, and required guard has grounds or is explicitly inadmissible; D01/D03 closed.
- **Verification:** per-fact source/date/plan/maintainer and authorized-action cross-check.

## IN-04 · Load and applicability-scope freeze

- **Responsibility/scope:** integration/verification; T0.4; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `IN-02, IN-03`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `docs/implementation/BASELINE.md`.
- **Deliverables:** D04 load, metrics, environment, and A/N applicability.
- **Acceptance criteria:** metrics are frozen before measurement; unapproved numbers remain candidates; no fabricated timelines.
- **Verification:** T0 exit cross-check.

## IN-05 · Real presets and runtime parameter freeze

- **Responsibility/scope:** integration/providers; T1.4; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `CFG-07`; phase gate `G0`.
- **Writes:** `server/internal/runtime/`, `contracts/providers/`, `docs/implementation/`.
- **Deliverables:** real facts mapped onto the verified compiler; D04 closed, D05/D06 specs frozen.
- **Acceptance criteria:** a real bundle compiles successfully or a concrete blocker is named; Clock/lease/save/backpressure parameters have defined behavior.
- **Verification:** full Compile on a real redacted sample; T1.4 exit cross-check.

## BASE-01 · Build, dependency boundaries, and process skeleton

- **Responsibility/scope:** integration; T1.1; reuse existing artifacts, verify against evidence. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `no leaf prerequisites`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/cmd/ant-server/`, `server/go.mod`, `server/go.sum`, `web/package.json`, `web/package-lock.json`, `Makefile`, `scripts/`.
- **Deliverables:** reuse of the established Go/Web project and boundary checks; necessary difference fixes.
- **Acceptance criteria:** check/build run; startup makes no outbound calls; empty gates do not pass by assumption.
- **Verification:** make check; make build; startup/shutdown smoke test.

## BASE-02 · Shared domain, wire, and generated contracts

- **Responsibility/scope:** contract integration; T1.2; reuse existing artifacts, verify against evidence. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-01`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/domain/types.go`, `server/internal/domain/planning.go`, `server/internal/api/dto.go`, `server/internal/api/validate.go`, `contracts/`, `web/src/api/types.ts`, `web/src/api/*.gen.d.ts`.
- **Deliverables:** stable inputs and outputs, error/missing semantics, generated artifacts, and shared fixtures.
- **Acceptance criteria:** quantity/union status/unknown fields are consistent across the full entry point; accepted results are preserved; changes to common contracts notify consumers first.
- **Verification:** make test-contract; make test-web; make check.

## BASE-03 · Resource, summary, result, and ordering pure rules

- **Responsibility/scope:** domain; T1.2; reuse existing artifacts, verify against evidence. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/domain/`.
- **Deliverables:** reuse of the quantity/fingerprint/result/resources/pools rules.
- **Acceptance criteria:** boundaries are precise; unknown reservations are not released; priority is stable; a correct WindowAt must not be rewritten for identity fixes.
- **Verification:** targeted Go rule tests; make test-contract.

## BASE-04 · Permit and cancellation protocol seams

- **Responsibility/scope:** contract integration; T1.2; reuse existing artifacts, verify against evidence. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/ledger/permit.go`, `server/internal/ledger/abort.go`, `server/internal/transport/transport.go`.
- **Deliverables:** reuse of handle pairing, single-consumption protocol, and the cancellation decision table.
- **Acceptance criteria:** non-empty real pairing; cross-runtime/reuse rejection; protocol versus persistence CAS boundary documented.
- **Verification:** targeted permit/abort tests; make test-contract.

## BASE-05 · Migrations, writer, and Store transaction primitives

- **Responsibility/scope:** storage; T1.3; reuse existing artifacts, verify against evidence. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/store/`, `server/tests/integration/`.
- **Deliverables:** reuse of real migrations, the write queue, receipts, and whole-group priority CAS.
- **Acceptance criteria:** evidence for cancellation/commit-response-drop/budget/migration/atomic rollback is valid; no impersonation of a finished business Ledger.
- **Verification:** make test-integration.
