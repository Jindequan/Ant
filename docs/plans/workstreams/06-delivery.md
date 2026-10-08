# Performance, Delivery, and Optional Extensions

Date: 2026-10-03. Parent plan: [Task Tree](../TASK-TREE.md). Original phase cards: [06-PERFORMANCE-AND-DELIVERY.md](../06-PERFORMANCE-AND-DELIVERY.md) / [07-EXTENSION.md](../07-EXTENSION.md). Actual status lives only in [STATUS](../STATUS.md).

SHIP uses the same final version with the frozen real load; when a bottleneck is found, Codex makes the minimal change in the corresponding module plus a regression. EXT only starts on a genuine new requirement; it does not affect first-version completion and is not implemented early just because it exists in the tree.

Paths in the tables are repository-relative paths; directories/files that do not exist are planned new locations, and `<selected-provider>`/`<second-provider>` are replaced with real adapter names after IN-03. Paths are the upper bound of the writable scope; before starting work they must be narrowed to the item's actual file list. Shared files remain subject to the single-writer lock.

## SHIP-01 · Performance environment, data, and load

- **Responsibility/scope:** performance verification; T6.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `IN-04`; phase gate `G5`.
- **Writes:** `server/tests/bench/`, `docs/evidence/WORK/SHIP-01/`.
- **Deliverables:** the D04 frozen environment/mix/historical volume/raw sampling scripts.
- **Acceptance criteria:** successful admissions, rejections, and hotspots are metered separately; latency spans are explicit; targets are not lowered after the fact.
- **Verification:** make bench and validation of load-script effectiveness.

## SHIP-02 · Tail latency, capacity, and endurance

- **Responsibility/scope:** performance verification/module owners; T6.2; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `SHIP-01`; phase gate `G5`.
- **Writes:** `server/tests/bench/`, `docs/evidence/WORK/SHIP-02/`.
- **Deliverables:** raw p95/p99, pprof/memory/goroutine/queue/locks, and the endurance report.
- **Acceptance criteria:** frozen metrics pass; unknown retention is not a leak; the two-hour representative endurance run follows D04; bottleneck fixes regress against correctness.
- **Verification:** real load + endurance + affected-correctness regression.

## SHIP-03 · Install artifacts and final real journey

- **Responsibility/scope:** release integration; T6.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `SHIP-02`; phase gate `G5`.
- **Writes:** `server/cmd/ant-server/`, `web/dist/`, `docs/operations/`, `README.md`.
- **Deliverables:** the Go binary/Web artifacts, fresh install/migration/restart/onboarding, and the final two-account chain.
- **Acceptance criteria:** no Node business backend, no fakes/keys; the same final version succeeds on the real path; this is not production deployment.
- **Verification:** installation in a fresh environment + real UI/provider evidence.

## SHIP-04 · First-version evidence consolidation and delivery

- **Responsibility/scope:** independent acceptance/integration; T6.4; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `SHIP-03`; phase gate `G5`.
- **Writes:** `docs/evidence/T6/DELIVERY.md`, `docs/plans/STATUS.md`.
- **Deliverables:** applicable evidence for R01–R25/N01–N07/A01–A30/I01–I10 and D01–D07 status.
- **Acceptance criteria:** no in-scope failure is reclassified as an extension; final inputs are consistent; the first version is complete only when every threshold is satisfied.
- **Verification:** matrix/final source summary/installation and result cross-check.

## EXT-01 · New requirements and compatibility design

- **Responsibility/scope:** design integration; T7.1; on-demand extension. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `no leaf prerequisites`; phase gate `G6`.
- **Writes:** `docs/design/`, `docs/plans/`.
- **Deliverables:** when a real new requirement exists, determine the extension points, evidence, and compatible migration.
- **Acceptance criteria:** first-version duplex capabilities not admitted remain rejected; new domains/duplex/Store are not implicitly folded into the current scope.
- **Verification:** review of the new requirement's source and impact.

## EXT-02 · Extension implementation and shared-contract regression

- **Responsibility/scope:** extension owner/verification; T7.2; on-demand extension. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `EXT-01`; phase gate `G6`.
- **Writes:** `dispatched along the exclusive paths of EXT-01`.
- **Deliverables:** a real slice of the new capability and compatible artifacts.
- **Acceptance criteria:** I01–I10/A23 are preserved; the core is not duplicated; the old two-account/recovery regression holds.
- **Verification:** extension contracts + real path + relevant performance.
