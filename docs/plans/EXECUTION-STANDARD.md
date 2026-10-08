# Execution and Delivery Standard

Baseline: P1.3 / PRD, DESIGN V0.8. Applies to all work items; specific task cards may add verification but must not weaken core requirements.

## How Each Work Item Is Executed

The implementation unit of P1.3 is the leaves of TASK-TREE. Task metadata lives in TASK-INDEX and actual state only in STATUS; each leaf must have an explicit write set, and a parent task must not modify that write set at the same time as the leaf's owner. Codex is responsible for project implementation, integration, and acceptance.

1. Read STATUS, confirm prerequisites, inputs, applicable domains, and the actual owner; mark active and record the baseline version.
2. Implement this item's source / configuration / contracts against the task card; complete the smallest runnable increment first, keeping the existing complete path usable.
3. Perform the corresponding verification: correctness, identity and permissions, cancellation / recovery, and onboarding experience are checked per this item's applicable scope. On failure, fix and rerun the affected cases; never pass by lowering targets.
4. Save artifacts and evidence and set the state to verifying. Check this item's exit and the upstream/downstream contracts; explicitly retain any items that have not passed.
5. After the batch executor's self-check fully passes, set to verifying, and the executor may advance its dependent leaves within the same continuous batch; this must not close phase gates or mark tasks done. Only after Codex's independent acceptance of the final tree passes is it set to done, updating the acceptance mapping and the states of necessary decisions.

States: pending (prerequisites not yet met or not started), ready (prerequisites and inputs complete), active, verifying, done, blocked (specific required inputs missing), deferred (optional scope). STATUS is the single state table. done must have actual artifacts and verification findable through links; blocked must state what is missing, who is affected, and the unblocking condition, without blocking independent work.

## Work Package Kickoff and Early Acceptance

Phase cards represent capability scope and cannot replace executable work items. Before each item starts, state in the existing task card / design: one business outcome, input/output and error semantics, the files / ports involved, at least one legal control and its key counterexample, and acceptance commands. When a technical algorithm is undecided, first complete a small-scope design validation and do not re-implement it in multiple languages; design gaps are filled by the current owner and do not become an obligation for the user to explain technical details.

For key semantics involving identity, metering, reservation release, and permissions, accept one runnable example plus compiled / executed artifacts before dependent implementation spreads out. This check is done by the current design and acceptance owners and adds no per-item user approvals. Shared semantics are proven at the server boundary before the Web mirror; independent test designs may be validated first, but the same business contract must stay consistent.

When each package completes, retain the final source and self-check evidence; Codex reviews the final tree at batch end. A single module's failure does not erase other modules' evidence; tasks whose executor self-check is complete and that await Codex's final acceptance stay verifying. A phase that has not yet exited honestly stays verifying. Report progress separately as "specific capabilities accepted, self-checked and pending acceptance, remaining defects, blocked on external input".

Rework causes must be distinguished: (1) violating an existing explicit contract — fix the implementation; (2) missing or mutually conflicting specifications — the design owner first repairs the current authoritative design; (3) new requirements — register the change and scope, then schedule. Counterexample expectations must have business grounds; a single probe must never itself become a new business rule. When the original design, task cards, and independent review conflict, fix the current file; historical reports stay historical, and the executor is never required to guess current rules from all old versions.

## Code and Architecture Standard

- Dependencies follow DESIGN §2.4; types / pure rules must not import process environment, database, or network implementations; the Web must not send provider requests or hold raw credentials. The composition root injects configuration, Clock, Store, Adapter, and credential resolution.
- State, result priority, and retry safety each have a single implementation source; provider differences stay in the adapter / domain rules / presets and are never copied into Router branches.
- The Ledger exclusively owns persistent changes; SQL and database retries stay in the Store, with no network inside resource transactions. Transport exclusively owns outbound calls and never hides retries, redirects, credential swaps, or binding swaps.
- Use concrete resource identities, precise units, and explicit semantics for missing fields; adjustment, reporting, cancellation, and sweeping all have permissions, idempotent IDs, and version / state guards.
- Implement rules for the necessary real resources; non-admitted capabilities are explicitly rejected. The first version has one real Store and the first domain's real slice; Web multi-type configuration / pools / manual priority must not be deferred, and no registration platform is built for speculative extensions.
- Any over-consumption is recorded faithfully; unknown is never auto-resent, and unconfirmed reservations are never deleted by lease/reset. Result persistence precedes the completion reply.

## Verification Levels and the Command Contract

T1 creates the Makefile and Web scripts entry points (make check / make test-contract, etc.; naming is fixed); the specific runtime, package manager, and tools are locked in D07 / lock files. After commands are first defined, subsequent tasks save results under the same script names; reporting the not-yet-established commands below as already run is forbidden.

| Script name | What it verifies | Main usage phases |
|---|---|---|
| check | Go gofmt/vet / Web type build / formatting / static rules, dependency boundaries, build and type checking; non-zero exit on failure | T1–T6 |
| test-contract | Go DTO / OpenAPI / Web protocol / type / schema, summaries, result parsing, rule and adapter report samples | T1–T6 |
| test-integration | Selected real database, transactions, migrations, queries, and composition root | T2–T6 |
| test-fault | Multi-process races, pause / crash at fault points, lease renewal / sweep, storage failures | T3–T6 |
| test-smoke | Explicitly enabled real-provider minimal closed loop and actual usage reconciliation | T2/T4/T6 |
| test-acceptance | The set of applicable A/N cases; outputs not-executed / not-applicable items, never counting skips as passes | T3/T5/T6 |
| bench | Performance and durability records under the frozen environment / data / load | T6 |

Pure functions use decidable boundary cases; transactions must be verified on the selected real database; network races use a local test server with controllable send / disconnect, labeled as a controlled environment. Real outbound calls are enabled separately, using the test accounts, actions, and consumption scopes confirmed in T0. CI never implicitly sends real business requests just because a developer ran static checks.

## Evidence Format

Evidence goes into `docs/evidence/Tn/<work-item-or-case>/`; create it only after the task starts, never filled with fabricated empty results. Each index contains at least:

```text
workItem / requirement / acceptance / invariant
Code commit or source snapshot digest, configuration / preset versions
Runtime, Store, hardware / instance, durability and sync settings
Input, prerequisites, command, expected, actual, exit code, time
Test nature: pure rules / real database / controlled network / real provider
Log or report paths, redacted evidence, failures and unverified items
Conclusion and reproduction steps
```

Without Git, record a source snapshot digest; commit numbers must never be fabricated. Logs contain no keys, no unauthorized bodies, and no sensitive recipients; credentials appear only as references. Real-provider evidence is proven with request IDs, accounting / end-of-call evidence, and redacted summaries; test-server logs cannot stand in for that evidence.

Work-item delivery states four things: changes and purpose, verification and evidence, open items, and the next item with its inputs. Completing a document or scaffold only proves that artifact exists and cannot replace outbound-call, recovery, or performance acceptance.

## Changes and Resumption

Reversible choices such as implementation details and test tooling are decided and recorded by the executor. Actual business identities, deployment boundaries, provider plans, upper bounds, and billing facts must be determined from real material; when missing, register the input and never assume free or independent balances. For new requirements, inexpressible resources, or necessary changes to core invariants, record the specific impact and update the corresponding PRD / DESIGN / task cards before executing dependents.

Adding correctness verification does not restart the full documentation review. Out-of-scope capabilities go to T7; in-scope failures must never be moved to T7. On discovering a core security problem, fix the affected path and re-verify first, then continue per the original plan.

Before interruption, record the current work item, changed files, the last passing and failing commands, the next step, and missing inputs; on restart, resume from these facts without rewriting the overall plan. Final code delivery / deployment is a separate action; plan creation does not mean this round has been released.

## Additional Gates for Go, Web, and Resource Pools

New Go concurrency paths run race; writers / hosts / subscriptions are bounded and closable, goroutine fall-back is recorded, and the absence of hidden HTTP replays must be proven with actual wire faults. The Web follows the page contracts, with four states / permissions / 409 drafts / keyboard order delivered in sync; component checks use make test-web and real-stack walkthroughs use make test-e2e. Screenshots per page at desktop and 390. pool/member changes save atomically and the new server's selection matches the UI order; units are never added together, balances are not copied across pools, and providerOrder is not overridden by priority. Never verify only frontend list ordering.
