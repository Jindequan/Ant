# T2 · Single-Binding Real Closed Loop

Goal: one real request goes through the complete safety core, obtains a result, settles truthfully, and is queryable. Prerequisite: T1 exit. Primary owner: integration; ledger / adapter collaboration. Related: DESIGN §3–9, PRD R01–R03/R05–R11/R16–R20.

Source files and methods follow the [technical mapping](../design/06-IMPLEMENTATION-MAP.md); execute the [complete transaction algorithm](../design/03-EXECUTION.md) step by step, and do not invent a separate permit / settlement flow.

P1.3 leaf dispatch: after G1, the configuration service, Ledger commands, pure Adapter / controlled Transport, and Web foundations may be prepared ahead of time by dependency; the full ledger integration LED-05 still waits for CFG-08/API-01/WEB-02, and the execution chain is wired per the original safety exit. See TASK-TREE for execution order; entering T2 before G1 under the name of module splitting is not allowed.

## T2.1 Minimal Web Configuration, API, and Real-DB Assembly

1. Wire up the minimal Web provider / resource / one-member pool forms, and the Go ConfigService draft / validate / activate + GET read-back; assemble one verified binding, account, resource, DomainPolicy/Guard, Store, Clock, and SecretResolver; only validated configuration activates, and failure keeps the old configuration.
2. Implement the pool whole-group priority API (PUT /pools/{id}/order, the K4 authoritative write path: entries carry an explicit numeric priority for every member, exactly-once validation, optimistic version, full rollback on 409; reuses the ReplacePoolMembersCAS transaction primitive already delivered in T1.3), plus basic candidate selection (domain / capability / enabled / required affinity / allowMetered hard filtering, priority ASC / offerId ASC stable sort). K5 ruling: these two are first implemented in this item; T4 only does the second-provider differences and the complete ordering UI.
3. Run migrations and establish account / binding shared references; wire up the authorization context and read projections; output availability / rejection reasons, never keys.
4. Compile the fixed plan and live admission, checking per-dimension upper bounds, windows, spending policies, and capabilities; reject when a required rule is unsupported.

Deliverable: a repeatable-start composition root and the configuration validation path, the pool whole-group order API, and basic selection. Acceptance: the chosen real DB reads and writes normally; wrong configuration / missing credentials / missing required guards can state the reason before sending; test-environment persistence is queryable after process restart; whole-group order saved is read back verbatim via GET (including equal priorities).

## T2.2 Atomic Ledger and Basic Safe Recovery Paths

1. Implement minimal complete semantics for claim, quote, tryPrepare, authorizeDispatch, requestCancel, renewLease, settle, and sweep, plus the ResourceSemantics required by the first binding; constrain concurrent ownership, budget, resources, and cancellation. Required rolling-window / entitlement rules are implemented from this slice onward — they cannot wait to be patched in after real outbound calls.
2. Implement the Ledger.AbortBeforeDispatch full transaction (K3 protocol matrix in 03-EXECUTION.md §9; the port and decision table were frozen in T1 at internal/ledger/abort.go), and complete its acceptance: reorder after prepare, reorder after authorize, lost consume reply, crash after consume, and prepared cancellation — checking in each case the actual network counts, resource sets, and whether the next prepare is possible.
3. All resource reservations share the same transaction with no residue on failure; the dispatch permit writes executing, version, and decision in the same transaction. Settle separately per reservation / consumption / occupancy; real overages are still booked.
4. Terminal states and result references are persisted first; unknown counts conservatively and retains the unconfirmed occupancy; a prepared record's previous owner cannot start again.
5. Wire up bounded sweeping and lease renewal; a lease is not treated as external completion; duplicate settlement, storage unavailability, and late results can only follow the prescribed paths.

Deliverable: the Ledger's basic transaction paths on the real Store, state / resource read projections, and conservative recovery. Acceptance: real-DB basic dedup / all-resource atomicity / single settlement / release on non-execution / unknown-occupancy retention pass. T3 will add the fault coverage, but recovery must not be left blank here before sending to the network.

## T2.3 Sending, Reporting, and Execution Host

1. Implement the first provider's encode/decode and encoded-request-validator; Transport.prepare parses credentials early and returns an opaque handle; encoding, evidence gathering, and requestDigest binding all complete before the permit.
2. Transport performs one local marker plus Ledger.consumeDispatchPermit final verification / CAS; the HTTP send happens exactly once, only when digest and credential version match; hidden retries / redirects / binding switches are forbidden.
3. The Router takes over task lifecycles, separating the submission reply from the factual result; disconnects do not depend on consumer settlement, and storage failure never fabricates terminal states.
4. For the AI choice, wire up streaming, bounded buffering / backpressure, cancellation, and partial-output rules; for the email choice, verify suppression / sender identity, and interpret success only per the agreed business semantics.
5. First use the controlled test server to verify permit call counts, known reports, disconnects, cancellation, and convergence with no consumer, proving there is no outbound-call entry point that bypasses the ledger.

Deliverable: ProviderAdapter, Transport, Router, the chosen domain's report projection, and the host lifecycle. Acceptance: single-permit counting is really observable; results / events are persisted before SSE delivery; when authorization is uncertain, no safe retry is reported and no unknown slot is released.

## T2.4 Full Real-Provider Path

1. Before outbound calls, recheck that D03 evidence and the configuration version still apply, using the test accounts, content, and consumption scope made explicit in T0.
2. From the minimal Web, execute config → authorized Go API → pool candidates → claim → quote / prepare → permit → real provider → settlement → persisted events → UI getOperation / inspect.
3. Verify request IDs, the business success definition, per-resource actual / conservative consumption, available results, and occupancy-ending evidence; when sources are incomplete, record unconfirmed truthfully.
4. Resubmit the same ID / query the terminal state, verifying no new outbound calls occur; after restarting the host, retrieve persisted records and explain the body-retention capability. After storage failure, retry the ledger, not the business send.

Deliverable: a reproducible single-binding example, real-DB data and redacted real-provider evidence, and the T2 exit record. Acceptance: the end-to-end path actually runs through, with one failure / blockage and the next step explained; passing on the test server alone does not count as completing this item.

## Phase Exit

T2.1–T2.4 complete; the minimal safe closed loop and queryable results are usable. The minimal real Web slice exists; T3–T6 have not yet been passed, so do not call it production-ready. Applicable A-item evidence is registered first, with full coverage exiting in later phases.

Next item: [T3 Correctness](03-CORRECTNESS.md); [T5 Operations and Onboarding](05-OPERATIONS-AND-ONBOARDING.md) may be prepared. First establish this phase's minimal real Web slice, then roll out the remaining pages; no additional services are built first.
