# Configuration and Security Boundary

Date: 2026-10-03. Parent plan: [Task Tree](../TASK-TREE.md). Original phase cards: [01-CONTRACTS.md](../01-CONTRACTS.md) / [02-VERTICAL-SLICE.md](../02-VERTICAL-SLICE.md). Actual status lives only in [STATUS](../STATUS.md).

The current focus CFG-01–07 corresponds to D1–D4; CFG-02 and CFG-03 write different files; the current owner implements them in dependency order. CFG-04/05 both modify compile.go and must be sequenced. CFG-01 is design plus rule examples only; common type changes are merged once by the contract integrator before dependent implementations are dispatched.

Paths in the tables are repository-relative paths; directories/files that do not exist are planned new locations, and `<selected-provider>`/`<second-provider>` are replaced with real adapter names after IN-03. Paths are the upper bound of the writable scope; before starting work they must be narrowed to the item's actual file list. Shared files remain subject to the single-writer lock.

## CFG-01 · Bounded design of window identity and resource roles

- **Responsibility/scope:** design integration; T1.2; bounded design with examples. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `docs/design/07-POOLS-AND-CONFIG.md`, `docs/design/04-RESOURCE-ALGORITHMS.md`, `docs/evidence/WORK/CFG-01/`.
- **Deliverables:** D1 canonical rules/timezone alias algorithm; D2 trusted kind/role fields and mapping; runnable positive and counterexamples.
- **Acceptance criteria:** date sampling is not used to prove equivalence; rolling is reflexive; reservation types are distinguished from commercial roles; shared fields have a single source of truth.
- **Verification:** CFG-04/05 may only open after the algorithm and examples pass review.

## CFG-02 · Mandatory evidence fields and initial-state consistency

- **Responsibility/scope:** config Go; T1.2; current defect fix. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/config/compile.go`, `server/internal/config/*evidence*_test.go`.
- **Deliverables:** D3 typed mandatory declarations with exact non-negative quantity validation.
- **Acceptance criteria:** missing bound/remaining, negative values, or conflicts with the fixed snapshot all produce issues with Compiled=nil; legitimate zero-sum and conservative capacity pass.
- **Verification:** Go full-Compile controls/counterexamples; latest independent review D3 probe.

## CFG-03 · Real scope metadata for facts

- **Responsibility/scope:** catalogue Go; T1.2; current defect fix. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/providers/localtest/`.
- **Deliverables:** D4 parent scope/provider inheritance and conflict rejection.
- **Acceptance criteria:** account facts output account scope; shared aliases output shared scope; parent-child contradictions fail to load.
- **Verification:** catalogue tests + full-Compile field assertions.

## CFG-04 · Trusted resource types and complete bundles

- **Responsibility/scope:** config Go; T1.2; current defect fix. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `CFG-01, CFG-02, CFG-03`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/config/compile.go`, `server/internal/config/document.go`, `server/internal/config/*role*_test.go`.
- **Deliverables:** D2 validation of configuration against trusted types, and bundle checks by metric and role.
- **Acceptance criteria:** occupancy disguised as counter, or entitlement disguised as limit, is rejected; legitimate Compiled retains correct types.
- **Verification:** Go full entry point; inspect actual Compiled fields.

## CFG-05 · Window identity canonicalization wiring

- **Responsibility/scope:** config Go; T1.2; current defect fix. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `CFG-01, CFG-04`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `server/internal/config/compile.go`, `server/internal/config/*window*_test.go`.
- **Deliverables:** D1 canonical identity used for fact resolution and constraint checking.
- **Acceptance criteria:** rolling is reflexive; Casablanca/Lagos are not equal; legitimate defaults/phase/supported aliases do not regress; WindowAt behavior is unchanged.
- **Verification:** equality properties/witnesses + full-Compile entry point.

## CFG-06 · Web compile-feedback mirror

- **Responsibility/scope:** Web contracts; T1.2; current defect fix. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `CFG-02, CFG-03, CFG-04, CFG-05`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `web/src/api/config-*.ts`, `web/src/api/validate.ts`, `web/tests/contract.test.ts`.
- **Deliverables:** the frontend consumes the same rules and business corpus, with error locations that are fixable.
- **Acceptance criteria:** both sides judge legitimate/illegal identically with correct business grounds; non-nil compile output fields are valid; no self-granted capacity.
- **Verification:** make test-web; typecheck; original full entry-point probe.

## CFG-07 · Current T1 configuration confluence acceptance

- **Responsibility/scope:** independent verification; T1.2; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `CFG-05, CFG-06, BASE-03, BASE-04, BASE-05`; phase gate `no phase exit required; requires its leaf inputs`.
- **Writes:** `docs/evidence/WORK/CFG-07/`.
- **Deliverables:** final-tree closure of D1–D4 and a regression report of accepted capabilities.
- **Acceptance criteria:** original controls/counterexamples and new independent counterexamples pass; the manifest is bound to final inputs; a successful compile is not called a successful activation.
- **Verification:** make check/test-contract/test-integration/test-web/build; independent make verify-manifest.

## AUTH-01 · Identity, namespace, and secret boundary

- **Responsibility/scope:** security/API; T2.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02`; phase gate `G1-DEV`.
- **Writes:** `server/internal/security/`, `server/internal/api/middleware.go`.
- **Deliverables:** trusted identity/permission context, credentials written only as mask/ref/revision, and secret resolution.
- **Acceptance criteria:** the body cannot self-authorize; cross-namespace read/write/cancel is rejected; logs/GET/frontend leak no secrets.
- **Verification:** local fake identities/fake credential source/API authorization counterexamples + run with the external network disabled; must not load real credentials or place real outbound calls.

## CFG-08 · Draft validation and configuration activation service

- **Responsibility/scope:** config application; T2.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `AUTH-01, CFG-07, BASE-05`; phase gate `G1-DEV`.
- **Writes:** `server/internal/application/config_service.go`, `server/internal/application/config_service_test.go`, `server/internal/store/store.go`, `server/internal/store/sqlite/statements.go`.
- **Deliverables:** SaveDraft/Validate/Activate, the GET read model, atomic versions, and immutable snapshots.
- **Acceptance criteria:** erroneous drafts can be saved; a failed activation keeps the old version; evidence/revision is re-checked within the transaction; 409 leaves no partial save.
- **Verification:** local SQLite integration + localtest facts/fake credential source; covers A26/A16; external network disabled; this alone does not justify accepting real provider activation.
