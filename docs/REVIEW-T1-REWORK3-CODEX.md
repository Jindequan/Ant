# T1 Third-Rework Independent Review: Accept the Progress, Tighten the Remaining Fixes

Date: 2026-10-02. Scope: the T1-REWORK-3 final handoff tree; the digest of the 168 input files is in [this round's evidence](evidence/CODEX-T1-REWORK3-AUDIT/README.md).

**Conclusion: this batch has not yet passed; 3 P1 items and 1 P2 item remain. No new round of overall architecture design will be started.** This round's implementations — trusted-instance parsing, compile ports, context schemas, field pinpointing, and the like — are retained; only the boundaries listed below are fixed. T1.1–T1.3 remain verifying, and the T0/T1.4/T2 prerequisites are unchanged.

## Accepted Results and Verification Boundary

Independently ran make check, Go -race -count=1 internal/integration, make test-web (156 tests), make build, and make verify-manifest (168 files), all exit 0. The amount, window execution, root schema, manifest, and budget improvements accepted in the previous round remain valid. The C4 field pinpointing is fixed; the main structure of C1–C3 has substantive progress, but the handoff conclusion of "fully closed" is still too strong.

Added Go probes: 10 leaf cases, 1 passed and 9 failed; Web: 7 cases, 1 passed and 6 failed. The number of failed assertions does not equal the number of defects. Each configuration counterexample was derived from a legal control case that passed the full compile entry point; the window counterexamples directly exercise the identity equality function and the WindowAt witness. No real provider requests, UI journeys, persistent activation transactions, or performance acceptance were executed. The review did not modify the implementation, and temporary tests have been removed.

## D1 · P1: Equality at two instants cannot prove window rule equivalence

Location: server/internal/config/compile.go:54–66; the Web counterpart is at web/src/api/config-helpers.ts:455.

`windowsExecutableEqual` only compares the WindowAt keys at 2026-01-01 and 2026-07-01. The daily windows of Africa/Casablanca and Africa/Lagos are identical at these two samples, so the function returns true, but the actual windows at 2026-03-01 differ by one hour. Another deterministic counterexample: an identical rolling/60000 rule compared with itself returns false, because WindowAt provides no fixed boundary for rolling.

Impact: identity resolution and verification constraints will mistake different rules for the same and will also wrongly reject supported identical rolling rules. This round directly proved the equality-check error; it did not claim on that basis to have reproduced a persistent ledger overrun. Compiled currently uses windows from verified facts, so wrongful acceptance could also silently change the reset semantics of user configuration.

Fix: establish a canonical rule identity defined per window kind: rolling compares the effective duration, lifetime is handled per its own rules, and fixed compares the canonical period, phase, and supported time-zone identity. Explicitly allow time-zone aliases that may be normalized; do not merge when equivalence cannot be proven. Do not continue approximating a proof by adding sampling dates. Reuse the existing execution-layer interpretation rules; do not rewrite the already-passed WindowAt.

Acceptance: reflexivity holds for identical rolling rules; Casablanca/Lagos rejects the equality check; the existing explicit midnight/natural-boundary, UTC alias, and phase cases keep passing; add full-compile-entry cases proving that parsing and verification actually use the fixed rules.

## D2 · P1: Executable resource types are still user-declared, and bundle coverage misses the role check

Location: server/internal/config/compile.go:116; server/internal/config/document.go:459–480, 515–526.

Trusted facts constrain metering, the charging nature, and semantic references, but not Kind/CommercialRole; CompiledResource directly copies the configuration values. Measured at the full entry point: changing the kind of a concurrency slot from occupancy to counter, or the commercialRole of an entitlement bucket from entitlement to limit, is accepted by Go/Web with zero issues. The profile complete bundle is judged only by metricId; RequiredDimension.Role does not participate in the check.

Impact: the compiler produces mutually contradictory execution inputs. When downstream needs to distinguish reservation from counting, or entitlement from limit, "a Compiled with zero issues" can no longer be trusted. This is not a future dispatch-optimization problem.

Fix: resource type constraints come from trusted facts or registered semantics and validate the user declarations; complete bundles are checked by required dimensions and their roles. Make explicit that in a profile occupancy corresponds to the Kind and limit/entitlement corresponds to the commercial role, avoiding direct mixing of the two enums. Illegal declarations must not enter Compiled; the backend keeps final authority, and the Web mirrors the same contract.

Acceptance: the two counterexamples above are rejected at the full entry point with the fields pinpointed; legal combinations keep passing; directly assert that the Compiled types match the trusted semantics. Patching only the counterexample IDs is not acceptable.

## D3 · P1: Typed evidence still has the "validate only when the field exists" loophole

Location: server/internal/config/compile.go:146–173.

The Q1 freeze requires bound/hard_stop to have a required bound and initial_state a required remaining, but the implementation checks only when the map contains the key. All four counterexamples at the full entry point are accepted by Go/Web: deleting bound, deleting remaining, remaining=-1, and remaining=0 while the verified snapshot is 500. The last one still produces InitialRemaining=500 and keeps that evidence reference.

Impact: mandatory evidence fields can be missing, and illegal or mutually contradictory material is marked compilable. The current values are taken from the server-side snapshot, and this round did not prove these counterexamples directly created additional balance; the problem lies in the admission contract and evidence traceability — invalid material must not be recorded as a valid basis for execution.

Fix: resolve typed required declarations per evidence kind, check canonical amounts and non-negative bounds, then cross-check against the verified instance. This round uses a fixed verified snapshot; differing initial-state values are reported directly as conflicts; choosing among historical observations is out of scope for this round. If support is genuinely needed later, specify the version/point-in-time rules at that time; silently accepting conflicts and then listing them as valid evidence is not allowed. Keep the direction of "trusted snapshot constrains configuration"; it must not become arbitrarily trusting the user's remaining.

Acceptance: the four counterexamples produce explicit issues; a valid zero value is separately used as a legal control case (with the companion verified initial value also zero); the conservative configuredCapacity positive case keeps passing. If historical observations are introduced, the selection rule and the evidence actually adopted must additionally be proven, rather than loosening the existing assertions.

## D4 · P2: ScopeID is empty in legal compile results

Location: server/internal/providers/localtest/catalog.go:103–109; server/internal/config/document.go:462.

The snapshot stores scopeId/providerTypeId on the parent VerifiedScope, and the child facts do not repeat these fields; after loading, the child facts are returned directly, while the compiler reads fact.ScopeID. All three CompiledResource.ScopeID values in the legal control case are empty. physicalKey already exists, and one cannot claim on that basis that different accounts' ledgers have been merged, but the declared compile port did not deliver the scope fields.

Fix and acceptance: at load time, complete and verify the fact metadata from the parent scope, rejecting contradictory parent-child values; verify that account facts output the account scope and that shared alias resolution outputs the real shared scope. Do not use ownerId in place of all ScopeIDs. Verify the ProviderTypeID and similar fields to keep the same defect from spreading further.

## Execution Approach: Shorten the Feedback Chain, Do Not Expand the Design Surface

1. The integrator first handles the shared models and the Go full entry point involved in D1–D4, in small steps, submitting for each item a legal control case, counterexamples, and Compiled output assertions together. Decisions touching contracts are reviewed together with the fixes; "already frozen" does not exempt them from review.
2. Sync the Web after the Go contracts stabilize. Do not have multiple agents simultaneously modify identity, evidence, or shared types; parallelizable work is limited to independent counterexample design, log verification, or mirror files with clear boundaries.
3. The reviewer independently selects counterexamples from business invariants, inspects the compile artifacts, and finally re-runs the modified final tree. Agreement on both sides only proves consistency; it must also be explained why the judgment is correct.
4. Permanently keep this round's failing probes and preserve the original failure evidence; after running the affected cases, execute one full gate run. The final handoff lists only the four fixes, the final tree results, and the unverified scope; long process narratives or total test counts must not substitute for conclusions.

This is a limited fix to the existing compile chain and does not require rebuilding the architecture. Accepted parts are not reworked again. The current judgment is: the executor can take on constrained implementation tasks, but the evidence from these rounds is insufficient to support it independently owning domain contracts and final acceptance; adding parallel workload cannot compensate for shared assumption errors. Earlier plans and reviews should also have turned these boundaries into execution constraints sooner, and the project owner should not keep bearing an endlessly expanding documentation loop.

## Rework Attribution and Remediation Responsibility (2026-10-02 addendum)

This investigation is based on the implementation readiness assessment, the original T1/T2 task cards, the Q1 freeze text, and the actual code; there is no reliable effort breakdown, so responsibility percentages must not be fabricated for any party.

| Layer | Existing Evidence | Responsibility and Correction |
|---|---|---|
| User requirements | Go server dispatch, independent Web configuration, heterogeneous resource pools, and manual priorities were unchanged in this batch; none of the current four defects required new business interpretation from the user | This batch's repeated rework must not be attributed to changes in user requirements; missing real account data only explains the T0/T1.4 blockage |
| Design executability | IMPLEMENTATION-READINESS-V0.7 already made clear that seams such as K1 were not frozen, yet the first batch still handed multiple seams to the executor together with the full engineering implementation; after Q1, limited sampling was still treated as rule equivalence | Codex's early design/task-decomposition responsibility, compounded by the executor's incorrect refinement. Key algorithms should first be proven with small examples before dependent implementation is opened up; having all the concepts does not equal a complete specification |
| Task boundaries | Original items 2/3 of T1.3 read as requiring the full Ledger/activation transactions, which T2.1/T2.2 are responsible for | The original task cards were corrected per the existing K5 division; this was not a new phase or a moved safety gate |
| Implementation quality | Q1 explicitly states that bound/remaining are required and that requiredDimensions includes role, yet the code validates only when fields exist and covers only metricId | This is an existing rule left unimplemented; it should not be handled by further expanding requirement explanations. The executor must verify the inputs and real artifacts item by item |
| Acceptance timing | Multiple rounds implemented Go/Web in bulk first and then reviewed as a whole; the final review found shared assumption errors, and both ends were reworked together | Both Codex's and the implementer's acceptance arrangements need adjusting: verify one package at a time, verify key rules first, then mirror shared semantics. Final acceptance is retained, but it no longer carries the first design validation |

Remediation has landed in the existing T1 cards and EXECUTION-STANDARD: the current four packages have independent exits, D3/D4 can be implemented directly, and the remaining design for D1/D2 will be completed by Codex and then dispatched; accepted results are retained. This section changes the execution arrangement and makes this round's technical boundaries explicit; it does not modify the implementation or declare the defects fixed.
