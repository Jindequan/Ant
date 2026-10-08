# CFG-06 Web D1–D4 mirror

2026-10-03. Finite compiler/catalog mirror only; no UI, schema/wire redesign, T2 or external calls. Exclusive implementation files: `web/src/api/config-catalog.ts`, `web/src/api/config-helpers.ts`; durable controls in `web/tests/compiled-config-final.test.ts` and sanctioned positive-input completion in `web/tests/contract.test.ts`. `config-graph.ts` was not changed. Original review corpus and fourth-audit file remain unchanged.

D1 now compares kind-specific structural identities, not sampled WindowAt keys. It reuses existing anchor parsing, phase/week/month interpretation, accepts the exact UTC alias list, keeps other IANA names distinct, rejects Local/invalid rules, and accepts trailing fractional zeros representing exact milliseconds. Rolling duration is a positive represented integer bounded by int64. The independent complete-entry control exposed an initial unnecessary safe-integer cap; that was removed without changing schema/wire precision policy. Server remains authoritative over raw JSON precision. Old Web approximate boundary-probe implementation was removed.

D2 registration matches trusted fact semantics/revision exactly once and validates registered kind/role enums. Every resolved resource checks kind, commercialRole and provider identity, including resources with no offers. Success retains values validated against the trusted registration. Profile roles separately check Kind for occupancy and CommercialRole for limit/entitlement; occupancy/limit may cover both requirements. Profile domain mismatch mirrors Go.

D3 bound/hard_stop require canonical nonnegative bound equal to verified capacityBound; initial_state requires canonical nonnegative remaining equal to verified initialRemaining. Conservative configured capacity remains legal; actual zero snapshots/claims pass. Missing, invalid and conflicting claims preserve Go issue paths and message keys.

D4 catalog construction copies and inherits empty child scope/provider IDs from parents, rejects contradictions and missing/unregistered/duplicate/invalid parent metadata. Account and shared aliases retain actual scope/provider identity; the caller's snapshot is not mutated.

Historical positive `valid-catalog-two-facts` is completed only in test-local clones: prepaid configuredCapacity stays 500; own bound/initial evidence describe verified 1000. Corpus bytes, expected matrix and negative assertions are retained. Old rolling expectation was updated to the accepted reflexive identity contract.

§4.8 quick gate: configuration validation/compiler output contract only; no page/IA/schema changes. §1.6 boundaries retained. §3.8/§4.1 checks cover full-entry compiled output, unoffered type conflicts, malformed/duplicate semantic registration, profile roles, quantity malformed/negative/missing/conflict cases, conservative/zero values, shared scope, metadata conflicts, rolling and conservative IANA window identity.

§1.3 evidence: `typecheck-final.log` records `cd web && npm run typecheck`, exit 0. `web-tests-final.log` records `cd web && npm test`, exit 0: 6 files, 235 tests passed, including 49 new mirror controls and root-owned independent checks. `web-tests-initial.log` preserves initial test-surgery failure (unexpected poolMembers field added to the new unoffered control); corrected to existing offers/pools only. Initial typecheck diagnostic is preserved in `typecheck.log`; the root owner fixed its independent-test narrowing. Model identity was not exposed to this 独立审查者.

§4.6 release: implementation ownership released to root for CFG-07 final-tree review. UI journeys, activation/storage/ledger behavior, real supplier verification and overall T1 release are not claimed here.
