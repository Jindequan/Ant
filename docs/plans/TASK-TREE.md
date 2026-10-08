# ANT Project Task Tree

Version: P1.3 · Date: 2026-10-04. T0–T6 are phase exits; the leaves below are executable work items. Business rules follow the current design, node metadata is governed by [TASK-INDEX.json](TASK-INDEX.json), and the six workflow files are its human-readable projections; [STATUS](STATUS.md) is the single source of actual progress.

2026-10-04 latest ruling and results: Codex has full authority over project implementation, verification, and acceptance. CFG-01–07 and the T1.1–T1.3 technical packages have been independently accepted; the G1-DEV six-leaf batch was once independently accepted, but LED-03 was reopened because actual usage reports exceeding int64 cannot retain their original values under the storage design; see `docs/evidence/WORK/LED-03/REOPENING-REVIEW.md`. Acceptance evidence for the other leaves is in `docs/evidence/WORK/G1-DEV/INDEPENDENT-REVIEW.md`. T0/IN-05 is not yet complete and G1 has not exited. Already-accepted results are retained and are not re-executed.

## 1. The Tree Owns Ownership; Dependencies Own Order

```text
ANT first version
├─ 01 Inputs and engineering foundation
│  ├─ IN-01 scenario → IN-02 deployment / IN-03 two-provider facts → IN-04 load
│  ├─ BASE-01 engineering → BASE-02 contracts → BASE-03 pure rules
│  │                              ├─ BASE-04 permit protocol
│  │                              └─ BASE-05 real-database primitives
│  └─ IN-05 real presets and run parameters (depends on T0 and configuration-compile acceptance)
├─ 02 Configuration and security
│  ├─ CFG-01 D1/D2 limited design
│  ├─ CFG-02 D3 evidence / CFG-03 D4 scope → CFG-04 D2 types → CFG-05 D1 window
│  ├─ CFG-06 Web mirror → CFG-07 current T1 merge acceptance
│  └─ AUTH-01 identity secrets → CFG-08 draft/verify/activate service (after T1 exit)
├─ 03 Ledger, execution, and protocol
│  ├─ LED-01 claim → LED-02 prepare/permit → LED-03 settlement → LED-04 recovery → LED-05 real-database integration
│  ├─ RUN-01 Adapter → RUN-02 Transport
│  ├─ RUN-03 Host/output → RUN-04 Router assembly → RUN-05 first-binding real acceptance
│  └─ API-01 configuration/ordering; API-02 operations; API-03 persistent SSE
├─ 04 Web and resource pools
│  ├─ WEB-01 shared interactions → WEB-02 config-activation thin slice → WEB-06 execution-results thin slice
│  ├─ WEB-03 full provider/resource; WEB-04 operations/disposition; WEB-05 experience acceptance
│  └─ MULTI-01 second provider → MULTI-02 manual ordering/explanation → MULTI-03 safe failover
├─ 05 Correctness and operations
│  ├─ QA-01 resource invariants; QA-02 crash/send races; QA-03 permissions/storage → QA-04 D06 acceptance
│  └─ OPS-01 observation/management core → OPS-02 operations wiring → OPS-03 retention/backup → OPS-04 D05 acceptance
└─ 06 Delivery
   └─ SHIP-01 load → SHIP-02 performance durability → SHIP-03 installation real journey → SHIP-04 first-version delivery

Beyond the first version: EXT-01 new-requirement design → EXT-02 extension implementation (started only with real new requirements)
```

The arrows in the tree are a summary of the main relationships; see the node index for complete multi-prerequisites and phase gates. Tasks must never be started based on tree indentation alone. Parent nodes have no coding work of their own and complete only when all required subtasks and integration gates pass.

| Workflow | Task card | Most important module boundary |
|---|---|---|
| Inputs/Foundation | [01](workstreams/01-foundation.md) | Real material is separated from controlled samples; reuse accepted code |
| Configuration/Security | [02](workstreams/02-config.md) | User intent → trusted fact constraints → Compiled; the server holds final authorization |
| Ledger/Execution/API | [03](workstreams/03-execution.md) | Store owns SQL exclusively, Ledger owns atomic state, Transport owns single outbound calls |
| Web/Multi-provider | [04](workstreams/04-web-pools.md) | The UI configures and observes; the server filters, orders, and fails over safely |
| Correctness/Operations | [05](workstreams/05-correctness-ops.md) | Independent counterexamples; observation/verification must not manufacture balance or resend |
| Delivery/Extensions | [06](workstreams/06-delivery.md) | Frozen load and real usage evidence; in-scope problems are not hidden in extensions |

## 2. Implementation and Acceptance Responsibilities

Codex is responsible, for each leaf, for design completion, implementation, integration, verification, and state maintenance. Before executing, check prerequisites, inputs, write sets, acceptance criteria, and real outbound-call authorization; when technical semantics are undecided, first supply contracts and decidable positive/negative examples. After the self-check completes, the state stays `verifying`; `done` is marked only once independent acceptance evidence is complete. Phase gates, real-account authorization, and safety invariants must not be skipped for implementation convenience.

A work item must deliver observable results, legal controls, key counterexamples, concrete verification commands, and the open scope. Configuration / ledger acceptance must inspect actual persisted rows, events, and resources, not just return values. Shared interfaces and the composition root are maintained solely by Codex, avoiding contract forks caused by concurrent modification.

## 3. File and Integration Boundaries

| Scope | Core constraint |
|---|---|
| Contracts, composition root, and routing | Public types, schemas, dependency graphs, migrations, and registration are maintained centrally; freeze inputs and outputs before implementing consumers |
| Storage and ledger | The Store owns SQL exclusively; the Ledger owns persistent state transitions exclusively; atomic transactions, idempotency, and state guards cannot be bypassed |
| Execution and adapters | Router plans; Adapter encodes/decodes; Transport exclusively owns single outbound calls; Hooks may participate only through the [Hooks contract](../design/10-HOOKS.md), and phase transitions must be verified stepwise by the core |
| API, security, and Web | The API enforces identity and DTO boundaries; the Web holds no keys and never decides authoritative quota or dispatch |
| Independent verification | Evidence is formed from the final source digest, raw logs, positive/negative examples, and phase acceptance matrices; implementer self-checks are no substitute |

Exact file scopes, phase prerequisites, and acceptance methods are governed by TASK-INDEX, the task cards, and STATUS. When independent write sets are insufficient, implement sequentially; shared state machines are never split for the sake of parallelism.

## 4. Batches Executable Now

First compare the current source with the most recent review digest: as of 2026-10-03, 11 implementation-input changes had been observed, so old reviews cannot represent that the new tree has all passed or still all failed. For newly added implementations, re-verify first and map them to the corresponding CFG leaves; accept if fixed, and dispatch fixes only if not; never overwrite or duplicate modifications others have in progress. For existing results, check the original evidence against the BASE nodes and do not rebuild the engineering setup. See STATUS for the actual current task state; what follows is the working order, not a statement of completion.

| Batch | Work | Merge/blocking conditions |
|---|---|---|
| C0 | Converge CFG-01 in sequence, then implement CFG-02/03 and independently review the counterexamples | CFG-01 only writes design/rule examples and does not seize Go public types; dependencies open only after each of the three is individually accepted; the real-material IN chain can advance independently |
| C1 | Implement CFG-04 and independently review the CFG-02/03 outputs | CFG-04 requires accepted type decisions and a public type baseline |
| C2 | Implement CFG-05 and independently review CFG-04 | CFG-05 reuses the accepted window execution; WindowAt is not rewritten |
| C3 | Implement Web CFG-06 and independently review the four-item Go merge | The Web shared validator is maintained centrally by Codex; if accepted sub-rules are mirrored early, it still belongs to CFG-06, and final acceptance waits for all upstream |
| C4 | Run the CFG-07 full gate set and the final tree review | Only the current T1 technical packages are accepted; while real-parameter IN-05 and G0 have not passed, G1 still does not hold |

Each leaf is first verified in a targeted way and accepted; only at merge time are the full suite, generated artifacts, and manifest checks run uniformly. Never restart all reviews for every changed line, and never wait until the end of an entire phase to test core assumptions for the first time.

## 5. Subsequent Execution and Phase Gates

G0–G6 correspond one-to-one to the original exits of T0–T6, with no added per-item user approvals. The new `G1-DEV` only authorizes designated leaves to implement and verify locally with the external network prohibited; it does not replace G1 and does not let T2 or later phases exit. G1 still requires G0, IN-05, and their real evidence; any real credential onboarding/activation, provider request, RUN-05, or G2 exit must satisfy G1. T3 may prepare/verify the required backend leaves after G1, but G3 still needs G2; T4 needs G3; T5 may prepare after G2 and exit after G4; T6 needs G5.

`G1-DEV` depends on the accepted BASE-01–05 and CFG-07. Only AUTH-01, CFG-08, and LED-01–04 are opened. Execution must use localtest / fake identity / fake credential sources and local SQLite; test processes must have the external network prohibited (not merely no API key configured). Real credentials must never be read, provider domains never accessed, and real costs never incurred. This gate may accept the offline code leaves' own artifacts and tests, but on that basis must never accept real account/provider behavior, the T2 integration exit, or G2. Later API/UI/Adapter/Transport leaves are still opened individually per the task-index gates and private-evidence conditions.

| Phase | Work items | Integrations that must be serial |
|---|---|---|
| After G1-DEV (no external network) | AUTH-01/CFG-08; LED-01–04 (local implementation/verification only) | Single leaves accepted in order; no exit, no real credentials/providers |
| After G1 | API, Provider/Adapter, and the remaining execution/Web leaves | Configuration API + minimal configuration UI → LED-05 integration → Router/Host/operations API/SSE → WEB-06 → RUN-05 real closed loop |
| Existing LED/execution leaves | QA-01 resources; QA-02 lifecycle; OPS-01/QA-03 management and faults | Core fixes return to the owner; only after G2 does QA-04 close T3 |
| After G2 | WEB-03 configuration experience; OPS-02 operations wiring; ready Web operations pages | The T4 real second-provider extension does not start early; the full journey needs G4 |
| After G3 | MULTI-01 second-provider adaptation; OPS-03 retention/backup; independent regressions | MULTI-02 ordering/explanation → MULTI-03 safe failover → G4 |
| After G4 | Independent acceptance of the seven-page journey and operational disposition | WEB-05 and OPS-04 close D05 and complete G5 |
| After G5 | Installation guides and performance materials can be prepared | Freeze load → measure/optimize → final same-version installation/real journey → G6 |

The prerequisites of large items such as T2.1/T2.2 remain the **integration exit order**; AUTH-01, CFG-08, and LED-01–04 can only be implemented locally with no network under G1-DEV. LED-05 still waits for the configuration service, the API, and the minimal configuration UI; it and G2 still need a real G1. This arrangement does not revoke the real outbound-call, T0, or T1 thresholds.

## 6. Acceptance, Failure, and Stopping Points

Leaf states proceed ready→active→verifying→done; when material is missing or upstream is unaccepted, record the specific reason instead of covering independent work with "the whole project is blocked". Codex implements and self-checks leaf by leaf in dependency order; this is only an implementation rhythm — it does not mark upstream as accepted and does not allow crossing unsatisfied entryGates or phase exits. After Codex's self-check, tasks stay in verifying; done requires Codex's independent review of the final source, evidence, and this task's criteria to pass. Phase gates rest on formal evidence; self-check PASS is no substitute. Tasks already done are reopened only when their contracts/inputs/implementation are affected by new changes; failures of other tasks in the same phase do not revoke them.

Design gaps return to the design owner; implementation misses return to the module owner; new requirements are scheduled after their scope is registered. Independent review raises business counterexamples before reading the author's report. Errors in risk boundaries and identity/quantity/permission/release invariants must be fixed; improvements that do not affect the current exit go into later work. Assertions must never be modified to mask failures, and "the model chose it" or "all tests green" must never be treated as proof of business correctness.

`test-fault`, `test-e2e`, and `bench` still have unimplemented entry points; the corresponding leaves must first deliver real tests. make check, build, and verify-manifest are different actions, with logs recorded separately. Phase final evidence continues to use docs/evidence/Tn/INDEX.md; leaf records can go in docs/evidence/WORK/<ID>/, referenced by the phase index, avoiding copying entire reports.

Requirement tracing continues to use [ACCEPTANCE-MATRIX](ACCEPTANCE-MATRIX.md); the new tree links R/N/A/I through each leaf's original phase number. The original card acceptance must not be dropped just because the tree does not restate every Given/When/Then. This tree authorizes no real provider calls or production releases. G1-DEV only authorizes local no-external-network implementation of the designated leaves above and does not constitute a phase exit. See the [execution manifest](execution/README.md) for the per-phase acceptance checklists and final acceptance responsibilities.

Plan maintenance: after modifying node metadata in TASK-INDEX.json, run `python3 docs/plans/check_tree.py --render`, then run the same command without arguments to check dependency cycles, missing references, workflow projections, and STATUS lines. Module descriptions, business specifications, and STATUS are not auto-rewritten by that command. A passing plan structure is not engineering acceptance.
