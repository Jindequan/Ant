# Web Experience and Multi-Provider Dispatch

Date: 2026-10-03. Parent plan: [Task Tree](../TASK-TREE.md). Original phase cards: [02-VERTICAL-SLICE.md](../02-VERTICAL-SLICE.md) / [04-MULTI-ACCOUNT.md](../04-MULTI-ACCOUNT.md) / [05-OPERATIONS-AND-ONBOARDING.md](../05-OPERATIONS-AND-ONBOARDING.md). Actual status lives only in [STATUS](../STATUS.md).

First WEB-02/06 as the thinnest real chain, then split into the providers/resources, pools, and operations pages. All pages include the four states, permissions, recovery, zh/en, keyboard, and 390; shared changes to styles/routing/client/locales belong to the Web foundation owner, and every page building its own variant is forbidden.

Paths in the tables are repository-relative paths; directories/files that do not exist are planned new locations, and `<selected-provider>`/`<second-provider>` are replaced with real adapter names after IN-03. Paths are the upper bound of the writable scope; before starting work they must be narrowed to the item's actual file list. Shared files remain subject to the single-writer lock.

## WEB-01 · Web shared interactions and API access

- **Responsibility/scope:** Web foundation; T2.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `BASE-02, AUTH-01`; phase gate `G1`.
- **Writes:** `web/src/api/client.ts`, `web/src/api/events.ts`, `web/src/app/`, `web/src/components/`, `web/src/styles/tokens.css`, `web/src/i18n.ts`, `web/src/locales/`.
- **Deliverables:** real session/client, error/four states, schema controls, and the tokens/i18n/routing foundation.
- **Acceptance criteria:** no business-layer automatic resending; field errors preserve input; keyboard/390 usable; secrets are never stored in localStorage.
- **Verification:** component/protocol tests; acceptance only after the real API is connected.

## WEB-02 · Minimal configuration draft-to-activation real UI slice

- **Responsibility/scope:** Web slice; T2.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `WEB-01, API-01`; phase gate `G1`.
- **Writes:** `web/src/features/providers/`, `web/src/features/resources/`, `web/src/features/pools/`, `web/e2e/config-slice.spec.ts`.
- **Deliverables:** one verified offer: provider → resource → pool → draft/validate/activate → GET read-back.
- **Acceptance criteria:** real API/real database; on error the old version is kept and a 409 does not lose the draft; a thin slice only, not expanded into the full seven pages.
- **Verification:** make test-web; make test-e2e on the real stack; A26.

## WEB-06 · Minimal request-to-result-and-cancel real UI slice

- **Responsibility/scope:** Web slice; T2.4; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `WEB-02, API-02, API-03`; phase gate `G1`.
- **Writes:** `web/src/features/pools/`, `web/src/features/operations/`, `web/e2e/execution-slice.spec.ts`.
- **Deliverables:** submit one request on an activated pool, observe the stream/results, and query after cancel or disconnect.
- **Acceptance criteria:** real API/real database/controlled Transport; a cancel ACK is not treated as terminal state; on disconnect recovery uses the original ID; real-provider proof lives in RUN-05.
- **Verification:** make test-web; make test-e2e on the real stack; A11/A29.

## WEB-03 · Full provider and resource configuration experience

- **Responsibility/scope:** Web configuration; T5.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `WEB-02`; phase gate `G2`.
- **Writes:** `web/src/features/providers/`, `web/src/features/resources/`.
- **Deliverables:** catalogue-driven multi-type configuration, sharing relationships, complete bundles, archive/repair, and availability.
- **Acceptance criteria:** users never fill in internal ledger types; unsupported capabilities can save a draft but cannot be activated; unknown entitlements are non-zero; four states/permissions/zh-en.
- **Verification:** forward and reverse e2e on the real stack + desktop/390 screenshots.

## WEB-04 · Operation list, detail, and disposition experience

- **Responsibility/scope:** Web operations; T5.1/T5.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `WEB-02, OPS-02`; phase gate `G2`.
- **Writes:** `web/src/features/operations/`.
- **Deliverables:** results/output/past decisions/consumption reservations/cancel and verification, with a clear next step.
- **Acceptance criteria:** no one-click resend for unknown; current configuration never recomputes history; missing bodies are stated honestly; disposition matches permissions.
- **Verification:** real API journey + both viewports/keyboard; A12/A19/A25.

## WEB-05 · Full seven-page set and first-use acceptance

- **Responsibility/scope:** independent experience verification; T5.4; independent review. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `WEB-03, WEB-04, MULTI-02, OPS-03`; phase gate `G4`.
- **Writes:** `web/e2e/`, `docs/evidence/WORK/WEB-05/`.
- **Deliverables:** seven page types with four states/both viewports/permissions, main paths and reverse paths, and hands-on testing by an independent onboarder.
- **Acceptance criteria:** with N02 prerequisites in place, 60 seconds to a real minimal action / self-assessed usability; without an independent onboarder it is unverified, and self-assessment is no substitute; D05 runtime verification.
- **Verification:** make test-e2e; screenshots; independent usage record.

## MULTI-01 · Second-provider onboarding and sharing differences

- **Responsibility/scope:** providers; T4.1; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `RUN-01, IN-03`; phase gate `G3`.
- **Writes:** `server/internal/adapters/<second-provider>/`, `contracts/providers/`.
- **Deliverables:** the second Adapter/presets and the independent/shared instance mapping.
- **Acceptance criteria:** shared ports with no duplicated Ledger/Router; multiple keys do not increase entitlement; credential-error scope is correct.
- **Verification:** shared contracts + two-account fact verification; A17.

## MULTI-02 · Full pool ordering and real selection explanation

- **Responsibility/scope:** Web pools/execution integration; T4.2; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `MULTI-01, API-01, WEB-02`; phase gate `G3`.
- **Writes:** `web/src/features/pools/`, `server/internal/application/router.go`.
- **Deliverables:** move up/move down/numeric/drag whole-group saving, heterogeneous-unit grouping, and past selections.
- **Acceptance criteria:** server-side new selections follow priority; equal values are stable; 409 keeps the draft; prepared re-checks the version; executing does not migrate; sharing adds no capacity.
- **Verification:** browser → real Go → two-account selection; A09/A19/A27/A28.

## MULTI-03 · Safe reallocation and two-account acceptance

- **Responsibility/scope:** execution integration/verification; T4.3; new implementation. Inputs = prerequisite work-item outputs + original phase cards/technical design; prerequisites `MULTI-01, MULTI-02`; phase gate `G3`.
- **Writes:** `server/internal/application/router.go`, `server/tests/live/`, `server/tests/fault/`.
- **Deliverables:** settlement-time reallocation after known-safe failures; the two-account real chain.
- **Acceptance criteria:** unknown/visible output/budget/cancel do not switch; every send obtains a fresh permit; real evidence and fault-server uses are kept distinct.
- **Verification:** controlled fault matrix + two-account real evidence.
