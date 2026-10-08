# Implementation and Acceptance Traceability Matrix

Baseline: P1.3 / PRD V0.12, DESIGN V0.9. This file assigns verification responsibility and does not replace the PRD's Given/When/Then. Actual progress lives in STATUS; actual results live in the index under `docs/evidence/`. Existing evidence and unverified scope are governed by STATUS and each stage's index. Every leaf of the P1.3 task tree is annotated with its original Tn.m; that mapping keeps consuming all applicable acceptance items in this table. The split does not remove any R/N/A/I.

## Functional Requirement Coverage

| Requirement | Implementation work items | Primary acceptance |
|---|---|---|
| R01 Presets / onboarding | T0.3/T1.4/T2.1/T5.1 | A10/A17/A18 |
| R02 Configuration relations | T1.2/T2.1/T3.1/T3.3 | A17/A18; email A21 |
| R03 Identity and deduplication | T1.2/T2.2/T3.2 | A01/A02 |
| R04 Selection and designated paths | T4.2/T4.3 | A09/A19 |
| R05 Atomic admission | T2.2/T3.1 | A03/A07/A17/A22 |
| R06 Single outbound call | T1.2/T2.3/T3.2 | A20; Transport single counting |
| R07 Truthful settlement | T2.2/T3.1/T3.2 | A06/A08/A15/A23 |
| R08 Safe reallocation | T1.2/T4.3 | A05/A09; no reallocation for unknown / stream-visible |
| R09 Results and failures | T1.2/T2.3/T3.2/T3.3 | A15/A16/A24 |
| R10 Lost-contact recovery | T2.2/T3.2 | A04/A05/A06/A20 |
| R11 AI streams and cancellation | T1.4/T2.3/T3.2/T5.3 | A11 for the AI stream portion; common cancellation A15 always applies |
| R12 Historical explanation | T2.4/T4.2/T5.2 | A12/A19/A25 |
| R13 Operational disposition | T3.3/T5.2 | A08/A12/A25 |
| R14 Permission protection | T1.2/T3.3/T5.3 | A13 |
| R15 Configuration evolution | T1.3/T2.1/T3.3 | A07/A16 |
| R16 Save and retrieval | T1.2/T2.4/T5.3 | A02/A11 |
| R17 Storage / restore | T1.3/T3.3/T5.3 | A14 |
| R18 Constrained extension | T1.1–T1.4/T3.1/T4.1; new requirements T7 | A23; optional extensions are not required to be implemented first |
| R19 Consumption / reservation | T1.3/T2.2/T3.1 | A22 |
| R20 Real-time authorization | T2.2/T2.3/T3.3 | A16/A20; email A21 |
| R25 ANT internal Hooks lifecycle extension | Hooks foundation layer / RUN-04/T2.3/T3.2 | A30; may declare named business lifecycle points and configure versioned Guard / ordering / Observer / core-validated data transforms; a stable manifest/revision (including API version and effective budget) is auditable and does not change core invariants |

Responsibility for first-release new requirements: R21→T1.2/T2.1/T2.4/T5.1–T5.4→A26; R22→T1.2/T1.3/T3.1/T4.2→A27; R23→T2.2/T3.3/T4.2→A28; R24→T1.1/T2.3/T3.2/T5.4→A29; R25→Hooks / RUN-04/T2.3/T3.2→A30.

## A01–A30 Verification Responsibility

"The final gate" means the latest stage by which complete evidence must be obtained; earlier same-numbered cases may form partial mechanism evidence. Evidence paths follow the execution standard and must state the real-database / controlled-network / real-provider nature.

| Case | Responsible work items / final gate | Required verification and evidence |
|---|---|---|
| A01 | T3.2 / T3 | Multi-process concurrency with the same ID; execution authority, attempt, permit, and send counting |
| A02 | T3.2, T5.3 / T5 | Semantic conflict / terminal-state replay; persistent result and missing-body explanation, no new outbound call |
| A03 | T3.1 / T3 | Real database 100 requests, capacity 50, retained reservations; 50 admitted, failures leave no residue |
| A04 | T3.2 / T3 | claim without attempt / prepared crash and cancellation; stable reason, release, and old owner forbidden to send |
| A05 | T3.2 / T3 | executing lost contact / late report; unknown, conservative consumption, verify, no automatic resend |
| A06 | T3.2 / T3 | Renewal / sweep / settlement races and duplicate conflict reports; events consistent with the summary |
| A07 | T3.1, T3.3 / T3 | reset, cross-window start / completion, and configuration failover; window attribution and balance migration |
| A08 | T3.1, T3.3 / T3 | Stale observations, negative adjustments, over-cap; no wrongful relaxation, actual facts recorded |
| A09 | T4.2/T4.3 / T4 | Two accounts, hold/affinity, known failure / unknown; default selection and safe reallocation |
| A10 | T5.4 / T5 | Non-participating implementers onboard against the real prerequisites; elapsed time, blockers, and output evidence |
| A11 | T3.2, T5.3/T5.4 / T5 | Caller interruption / host exit, cancellation / nobody consuming, original-ID query; for AI, additionally long streams / backpressure |
| A12 | T5.2/T5.4 / T5 | Repair / verification of authorization / quota / unknown; history retained and follow-up queries |
| A13 | T3.3, T5.3 / T5 | Business and operational identity privilege escalation; query / cancel / adjustment rejections and log redaction |
| A14 | T3.3, T5.3 / T5 | Real storage failure, old-backup restore gap; stop sending, verify, then resume |
| A15 | T3.2 / T3 | Six result classes racing with submission ordering; facts first, late arrivals do not change terminal state, consumption retained |
| A16 | T3.3 / T3 | Disable after prepared / spending tightened; current authorization rejects, old plans cannot relax |
| A17 | T3.1/T3.3, T4.1 / T4 | Dual-binding shared quota, credential error scope, and billing hold; real-difference regression |
| A18 | T1.4, T3.1/T3.3 / T3 | Missing dimension / expired / unenforced upper-bound rejections; passes once full enforcement provenance is available |
| A19 | T4.2/T5.2 / T5 | Configuration / balance changes after successful selection; the original DecisionRecord is not recomputed |
| A20 | T3.2 / T3 | Cross-lease pause before / after final verification; the former means zero sends, the latter conservative reservation, no exactly-once claim |
| A21 | T3.3, T5.2/T5.4 / T5 | Email only: cross-provider suppression / sender identity, guard failure, actual operational responsibility |
| A22 | T3.1 / T3 | With concurrency 1, unknown external still executes; B rejected, one release after evidence, reset does not clear unknown isolation |
| A23 | T3.1 / T3; new extensions T7.2 | Built-in I01–I10 reject applicable illegal plans; real over-cap recorded; new semantics reuse the common contract |
| A24 | T3.3 / T3 | Rate limit 2 seconds, deadline 60 seconds; fast stable failure, same-ID replay, no sleep / new outbound calls |
| A25 | T3.3/T5.2 / T5 | Adjustments invalidating observations, collection failure / cross-window / version changes; no Ledger network or business resend, no fabricated capacity |
| A26 | T2.1/T2.4, T5.1/T5.4 / T5 | Real UI provider / resource / pool draft → validation → activation, erroneous old versions retained, secrets never echoed back, GET tested in practice |
| A27 | T1.2/T1.3/T3.1, T4.2 / T4 | Heterogeneous offer capability / unit grouping, multiple pools on one resource do not increase quota, unknown shared blocking |
| A28 | T3.3/T4.2, T5.4 / T5 | Server's actual selection after UI reordering, whole-group atomicity / 409 draft, prepared-version independent review, executing does not migrate |
| A29 | T2.3/T3.2/T3.3, T5.4 / T5 | HTTP / SSE real protocols, persistent sequence numbers, disconnect / reconnect / permissions, no duplicate outbound calls, cancellation produces no fake results |
| A30 | RUN-04/T2.3, QA-02/T3.2 | Built-in and custom checkpoints wired into real core call sites; verify stage type constraints, stable ordering, fail-closed and deadline; ordering preserves the original candidate set; only explicitly enabled Transformers may execute, with initial values and each step's output checked by the core validator, and over-budget or failed runs return no partial results; Observers are read-only over post-commit redacted events, failures do not alter committed results and do not block the main path; registration must carry a version, sealed manifest/revision (including API version and effective budget) is stable and enters the persistent DecisionRecord, recoverable via the Hook's ID/version; Hooks do not bypass identity / permits / ledger / single send / terminal state |

DESIGN's T3 exit requirements still cover the cases listed there; A02/A13/A14/A17/A25 complete the core mechanism at T3, with actual adaptation and operational journeys completed at T4/T5. Later gates must not become a reason to defer T3 correctness verification.

## N01–N07 Quality Verification

| Requirement | Responsible / final gate | Evidence standard |
|---|---|---|
| N01 Correctness | T3.4, T6.4 / T6 | Real-database concurrency, crashes, out-of-order events, and state / resource invariants; regression of affected items on the final version |
| N02 Onboarding | T5.4 / T5 | Prerequisites complete, non-participating implementers achieve minimal action in 60 seconds or demonstrable real usability; actual time and blockers |
| N03 Usability | T5.1–T5.4 / T5 | Screenshots of 7 page classes on both ends, four states / conflicting drafts / manual reordering with real stacks; walkthroughs of both the happy path and failure / cancellation / verification; reason, next step, result, and consumption understandable |
| N04 Performance | T6.1/T6.2 / T6 | Frozen real load / environment, core p95/p99, throughput, lock waits / rejections, 2-hour endurance |
| N05 Observability | T4.2/T5.2, T6.4 / T6 | operation/attempt/account/binding correlation, historical versions, provenance, redaction, and truncation visible |
| N06 Durability and recovery | T3.2/T3.3, T5.3 / T5 | Real-database intent / result persistence, backup gaps, lost-contact convergence, and unreleased reservations |
| N07 Maintainability | T0.3/T1.1, T4.1/T5.1 / T5 | Preset rationale / verification date / maintainer, extension landing points, common contracts, reproducible onboarding materials |

## I01–I10 Core Verification Responsibility

| Invariant | Implementation / verification work items | Required assertions |
|---|---|---|
| I01 Identity authorization | T1.2/T2.2/T3.1/T3.3 | Uncompiled / unauthorized resources and self-invented windows rejected; sharing relations do not increase capacity |
| I02 Unit values | T1.2/T3.1 | Wrong units / multipliers, NaN/Infinity, illegal reservations rejected; authorization adjustments carry semantics |
| I03 State versions | T2.2/T3.2 | expectedState/owner/generation/version condition races; old owners do not resurrect |
| I04 Atomic admission | T2.2/T3.1/T3.3 | Any resource / budget / gate / guard failure rolls back the whole transaction, no permit issued |
| I05 Reservation conversion | T2.2/T3.1 | Split conservation, one-time consumption / release / conversion; real over-cap recorded separately, never truncated |
| I06 Conservative reservation | T2.2/T3.1/T3.2 | Active / unknown / released are mutually exclusive; lease/reset do not release unknown, closure has evidence |
| I07 Idempotent accounting | T1.3/T2.2/T3.2 | Duplicate events / reports / adjustments / verifications do not double-deduct; conflicts do not overwrite history |
| I08 Result priority | T1.2/T2.3/T3.2 | Unique resolver covers races, cancellation does not erase accounting, late materials go through verification |
| I09 Single outbound call | T2.2/T2.3/T3.2 | One permit per attempt, conservative budget, Transport consumes once / no hidden retries |
| I10 Truthful recording | T2.2/T3.1/T3.3/T5.2 | Over-cap facts accepted and blocking; observations do not fabricate capacity, corrections require authorization / provenance / audit |

Ledger, Store, Transport, domain rules, and external evidence are each responsible for their verifiable parts; whether the upper bound is enforced by the provider and whether closure evidence is trustworthy are verified by D03 and must not be replaced by code unit tests.

## Applicability and Final Evidence

T0.4 records applicability in BASELINE: A21 is email-only; the AI stream details of R11/A11 apply only to AI, but cancellation, host liveness, and result retrieval always apply. The rolling windows / waterfall required by the selected provider must not be excluded on first-release grounds. T7's new extensions apply only after new requirements are initiated; A23's built-in validation is mandatory in the first release.

After each stage completes, associate this table's IDs, actual cases, and results in the corresponding `docs/evidence/Tn/INDEX.md`; T6/DELIVERY summarizes. Missing evidence is marked unverified, never marked passed. STATUS only links these indexes and does not copy test reports.
