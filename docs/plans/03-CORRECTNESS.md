# T3 · Correctness, Concurrency, and Recovery

Goal: use the real DB and controllable fault points to prove the core invariants of the chosen scope. Prerequisites: preparation may begin after T1; real-DB / send verification separately require T2.2/T2.3; the phase can only be completed after T2 exits. Primary owner: ledger / verification; integration and adapters fix the corresponding paths.

## T3.1 Resource Boundaries and Invariants

1. Refine and verify the resource rules already implemented for the first binding, and add the differences required by the second binding: fixed windows, required rolling rules, limits and entitlements, waterfall, concurrent occupancy; precise units / rounding / expiry — never apply a uniform used/capacity to unverified resources.
2. Verify all-resource atomic reservation on the real DB: capacity 50, reservation 1, no release, 100 concurrent requests admit only 50; failures leave no partial residue, and release and booking are never duplicated.
3. Cover prepare/dispatch/finish across window boundaries, month ends / DST / anchors, and unknown ownership across resets; late-starting usage must never be booked back into an old window without justification.
4. For applicable items of I01–I10, construct requests violating identity, units, version, capacity, release evidence, idempotency, terminal states, and permits, verifying the corresponding rejection responsibilities; real overage reports are legally booked and block.
5. Concurrency limit 1: while A is unknown and still executing externally, B is rejected; one release happens once ending evidence arrives; plain reset, lease, and clearHold do not restore unknown slots.

### QA-01 Executable Case Baseline (preparation item, not acceptance evidence)

The task tree allows designing the T3.1 cases before prerequisites complete. The following define the inputs, business expectations, and persisted assertions of the final real-DB cases; with LED-05 and G1 currently missing, QA-01 integration tests must not be started or recorded as passing on this basis. Every failing case also checks operation decisions, reservation, dispatch permit, resource_states, resource_events, and encumbrances — looking only at return values is forbidden.

| Case | Inputs / race arrangement | Business expectations and persisted assertions | Corresponding responsibilities |
|---|---|---|---|
| QA1-ID | Using an uncompiled resource, a resource from another namespace/pool, or a forged / expired window key | Rejected; resource_states, reservations, resource_events, dispatch_permits have no new rows or increments; even a valid sharing relationship must not duplicate capacity | I01, A03/A08 |
| QA1-UNIT | Reservation inputs with negative values, unit / multiplier mismatch, int64 overflow, or illegal reservations | Rejected before writing the ledger; no partial reservation, resource_event, or permit. Handling of an external real report exceeding int64 is covered separately in QA1-OVERAGE and must not reuse the input rejection path | I02, A07/A22 |
| QA1-BUNDLE | Multi-resource preparation where one resource, budget, or guard fails while the others would have passed | Blocked as a whole; every resource_states and reservations entry has no partial change, and there is no dispatch permit | I04, A08 |
| QA1-CAPACITY | 100 independent operations concurrently contending for capacity 50 with reservation 1 each; executed against the real DB by multiple processes | Exactly 50 admitted and 50 rejected; the final total of reserved / active / unknown does not exceed 50, each admitted item has exactly one corresponding reservation, and failed items leave no residue | I04/I05, A03/A17 |
| QA1-TRANSITION | For the same attempt execute prepare→authorize→settle/release, and submit state transitions repeatedly or out of order | Deltas conserved at each stage; consumption / release happens exactly once; illegal transitions do not change state; resource_events and the aggregates are consistent within the same transaction | I05/I07, A07/A08 |
| QA1-UNKNOWN | Capacity 1; A's result is unknown after the permit and the provider may still execute; then lease expiry, reset, or clearHold occurs, and B is submitted | B is still rejected; A's open encumbrance and uncertain_units are retained; only trusted ending evidence releases in one shot, and duplicate evidence does not release twice | I06, A08/A18 |
| QA1-WINDOW | Cover preparation, sending, and settlement across window switches; include month boundaries, DST, and late reports | Every item uses the correct window key computed with frozen rules; when window ownership is unknown, create isolation rather than guessing and backfilling; late amounts must not be booked into the old window without evidence | I01/I05/I06, A07/A17 |
| QA1-OVERAGE | **Given** an authorized, executing attempt with a mapped counter resource, **When** the settlement report carries a canonical metric usage greater than the int64 cap, **Then** the original value enters `attempts.report_json` and the report idempotency digest; the reservation is released, the resource gains an `actual_out_of_range` hold, no fabricated committed delta exists, the operation ends in `unknown/UnknownOutcome` with precise `unrepresentableMetrics` pending verification, and the resource outcome is `unrepresentable` with `consumed=null`; replaying the same report has no side effects, and conflicting reports only append verification material; no fault may allow a partial commit. Malformed formats, simultaneous setting with an int64 Actual, or non-overflowing decimals are rejected. LED-03 stays verifying until independent SQLite/race acceptance | I02/I07/I08/I10, A22/A23 |
| QA1-SHARED | The same account/resource prepared concurrently through two legal bindings or pool references | Shared capacity is counted once; cross-pool references must not increase capacity or duplicate creatable slots; non-shared resources remain isolated by resource identity | I01/I04/I06, A03/A08 |

I03 (owner/generation/version races), I08 (result priority), and I09 (permit and real single outbound call) are covered by T3.2/QA-02; they must still be included in the T3 summary and must not be omitted just because they are not in the QA-01 main cases. I07's duplicate-report/event idempotency needs the ledger's no-duplicates and cross-state race aspects covered separately in QA1-TRANSITION and QA-02 report races. All multi-process, final SQL state, and send assertions must be executed only after the integration stack provided by LED-05/RUN-04 is available.

Multi-process cases should reuse the existing integration `os.Args[0]` helper-process pattern: the parent test creates / migrates a single on-disk SQLite database, and child processes each open their own Store; start synchronized via a temporary-directory ready/release barrier, and after commands complete the parent process waits and queries final rows over an independent read-only connection. The capacity race can be initiated jointly by at least two independent processes totaling 100 distinct operation IDs, each contending for one unit of the same resource; `-race` separately covers concurrency safety. Child processes must have timeouts and clean up / terminate on failure, and must output parseable structured results to the parent process; winners must not be asserted by relying on scheduling order. This is an implementation proposal; it is still not run or accepted until the integration stack, test commands, and gate are satisfied.

Deliverable: resource rules and repeatable real-DB tests, the I/A evidence index. Acceptance: A03/A07/A08/A17/A18/A22/A23; the semantics of extended trusted code and provider upper-bound evidence are verified separately — structural validation must not be claimed to prove arbitrary plugins.

## T3.2 Multi-Process and Fault Races

1. Concurrent submissions with the same ID and same / different semantics, racing claim/prepare/settle/renew/sweep/cancel; verify one execution right, a single active attempt, stable terminal states, and consistency between events and aggregates.
2. Kill / pause processes at each point: claim without attempt, prepared, executing, report received but not settled, and settled but not returned; recover with the old owner and verify nothing is revived or resent.
3. Enumerate cancellation × success / unknown / no capacity / budget, cancellation before or after terminal states, deadline × unsettled outbound calls, and sweep × settlement; interpreted by the same resolver, with accounting and occupancy independent.
4. The controlled Transport pauses across the lease before final verification with zero outbound calls on recovery; pause after verification but before writing the socket, and verify that a possibly late single send is still conservatively isolated — the provider must not be claimed to be exactly-once.
5. Long streams disconnecting, no consumer, buffer caps, cancellation waits, host exit, and completion rejection under StoreUnavailable; query with the original ID, never stitching together new provider output.

Deliverable: fault injection points, cross-process repeatable tests, and real send count / state / resource comparisons. Acceptance: A01/A02/A04/A05/A06/A11/A15/A20, with actual failure causes consistent with permit facts.

## T3.3 Configuration, Permission, Observation, and Storage Faults

1. After prepare, disable an account / binding, revoke credentials, or tighten spending / business guards, and the permit is denied; loosening does not automatically change existing operation policies. Metering upgrades do not mix old estimates with new balances.
2. Test shared accounts with multiple bindings: authentication errors are isolated by credential version / endpoint scope, while billing blocks are shared; when a required path is unavailable, no silent reselection happens.
3. Unauthorized namespace, and query / cancel / adjustment / audit permissions are denied; the system sweep permission is verified separately; logs are redacted.
4. Observation staleness / cross-window / accounting lag / adjustment during collection / collection failure: the Ledger makes no network calls, watermarks / adjustment versions are verified, refresh only tightens, never increases capacity, never resends business; probes of consumption limits are also subject to permits and accounting.
5. Inject database lock conflicts, commit failures, response loss, current storage unavailability, and old-backup restoration; bounded database retries do not resend network requests, and recovery gaps block outbound calls first.
6. For the email domain, add host-suppression / sender-identity-change / guard evidence-failure verification, and switching providers does not bypass them.

Deliverable: configuration and permission controls, observation collection and management accounting interfaces, fault evidence. Acceptance: A08/A13/A14/A16/A17/A18/A21 (email)/A24/A25; the 60-second deadline must not cause a candidate that is rate-limited for only 2 seconds to wait implicitly.

## T3.4 Correctness Final Acceptance

1. Against the ACCEPTANCE-MATRIX, aggregate the T3-required items; check the applicable responsibilities of built-in I01–I10 and all failure paths, not only the happy cases.
2. Run tests on the actual concurrency / storage shape chosen in D02; a single-process in-memory fake does not count as multi-process real-DB evidence.
3. Verify D06 values and failure behaviors: clock, lease renewal / sweep, cancellation / guard deadlines, unknown occupancy, collection caps, and recovery are all repeatable; record and fix actual deviations.
4. After passing, close D06, and report the remaining external and experience verification for T4/T5/T6 — this item alone must not be used to declare overall first-version acceptance.

Add whole-group pool-order concurrent 409, poolVersion admission / consume races, multi-pool shared unknown, SSE persisted sequence numbers / missed-notification backfill, Go Transport real wire duplicates, and race detection. Per the K5 ownership ruling, the whole-group order API and the AbortBeforeDispatch transaction were first implemented in T2.1/T2.2; T3 verifies the concurrency and fault behavior of these already-implemented mechanisms and does not re-implement them.

Deliverable: the `docs/evidence/T3/` index, actual commands and failure-fix records, and the STATUS exit. Acceptance: all applicable core mechanisms among A01–A08/A13–A18/A20/A22–A30 listed in DESIGN T3 pass; A11/A21/A26–A29 provide mechanism evidence here, with the complete usage journey and applicability verified in T5. A30 completes the Hooks fault-boundary mechanism verification here.

Next item: [T4 Two Accounts](04-MULTI-ACCOUNT.md). Any core failure in T3 is fixed before exit, never deferred to optional extension.
