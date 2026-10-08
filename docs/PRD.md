# ANT Requirements Document

Version: V0.12 Go backend and Web resource pool requirements baseline · Date: 2026-10-04 · Current status: business requirements have been organized and supplemented with the internal Hooks extension and traceable contracts; full runtime implementation and real-world measurement are not complete.

Companion documents: [Design Document](DESIGN.md); for versions, open items, and review requirements see [Documentation Entry](README.md). This file defines business requirements and does not present implementation suggestions or candidate performance figures as promises of what has already been implemented.

This version retains the three review rounds and the V0.4.1 closing revision: requirements R01–R20 and N01–N07, with R21–R25 and A26–A30 added; it also incorporates the ANT internal Hooks extension capability requested by the user on 2026-10-04. V0.9 calibrated R25/A30 and incorporated Transformers that are progressively verified by the core; V0.10 required named Hook versions, a sealed manifest/revision, and traceable execution decisions; V0.11 brought the effective registration budget that can change runtime behavior into manifest/revision as well; V0.12 clarified that the Hook API version is sealed and that events are injected by the Registry. R18 comes from the user's requirement for flexible extension; R19/R20 refine existing correctness and configuration requirements. For the actual division of work and execution order see [Implementation Plan](plans/README.md). Passing a static design review does not mean the implementation or performance has passed.


2026-10-07 direction ruling: “you are on a totally wrong way! every provider's subscription, quota usage, time, and switch are individual, don't make them in a **warped style, describe 'What I need, what I want to build'?** then discuss with the plan”. Subsequent instruction: “in AI, you should research https://github.com/yetone/magpie learn how to design a router and management of ai subscription, and then to other platform and other category of services”. Provider-native configuration and behavior are required; the generic package wizard is rejected. Start with AI and derive category/provider extension boundaries from actual differences. [Source research and discussion proposal](research/MAGPIE-AND-ANT.md) precede further corrective implementation; proposals are not frozen requirements or acceptance.

## 1. Product Goals

ANT allocates limited resources among multiple authorized provider accounts for business callers, and records the selection, outbound call, consumption, and result of a logical operation.

Core value: after a business submits a request, ANT selects an available account based on verified capabilities, quota, and spending policy; when a block or an uncertain result occurs, the caller can find out what happened and how to proceed.

Success criteria: for the first real business, onboarding, calling, accounting, querying, and failure handling form a complete closed loop; dispatch across two accounts is verifiable; under a strict policy ANT never proactively selects capacity known to incur charges.

## 2. Users, Scenarios, and Evidence

| User | Specific Task | Required Outcome |
|---|---|---|
| Application developer | Onboard multiple service accounts and reduce manual failover | Simple onboarding, a stable calling contract, clear errors and results |
| Business caller of the host application | Initiate AI requests or transactional email | Usable results; when blocked, know whether to wait or try again |
| Developer/operations maintainer | Check quota, locate failures, verify unknown | Traceable records, and a disposition entry point that does not destroy history |

Candidate scenarios: desktop AI calling, SaaS server-side transactional email. The first real outbound-call slice uses D01 to select one business domain and verify two providers; the configuration UI and pool model support multiple providers/multiple resource types and capability grouping. Other domains lacking implementation or evidence can only be saved as drafts and cannot be claimed as executable. The deliverable is a Go resident backend + standalone Web; actual deployment is verified by D02.

Current value evidence comes from the multi-provider quota and implementation requirements raised by the user in existing discussions. There is no quantitative traffic, interview, payment, or provider feasibility evidence yet; T0 supplements one real request scenario and usage records/minimal verification. Commercial value cannot be claimed as verified on this basis.

## 3. Business Objects

| Object | Meaning the user needs to understand |
|---|---|
| Provider account | The actual quota and billing owner; multiple call channels may share one account |
| Call binding | A call path composed of a credential, endpoint, and adapter, referencing shared or its own resources |
| Quota limit | Whether continued execution is allowed, e.g., periodic usage, rate, concurrency |
| Consumable entitlement | Gifted quota, expiring quota, or purchased capacity; follows the provider's deduction order |
| Spending policy | Whether capacity known to incur incremental charges may be used |
| Logical operation | One business action, identified by a stable ID, e.g., email sending for a given order |
| Attempt | One provider request issued for that logical operation |
| Reservation and consumption | Capacity reserved before the call and usage confirmed/conservatively counted after the call |
| External execution occupancy | Concurrency resources held by requests not yet proven finished; managed separately from the operation's final state |
| Block | Temporary or persistent un-allocatability, with its reason and release conditions |
| Verification and adjustment | Supplementary confirmation and accounting correction after evidence is later obtained for an uncertain result |

Ordinary integrators do not need to manually design the relationships among these objects; presets absorb the complexity for supported providers.

## 4. Main Closed Loop and Reverse Handling

| Stage | Input | Output and Next Step | Responsibility | Implementation Status |
|---|---|---|---|---|
| Onboarding | Credential references, provider, and plan | Verified configuration; submit a minimal request | Onboarding/adapter | Not implemented |
| Submission | Logical ID, business request, deadline | Acceptance, conflict, or existing result | Integration | Not implemented |
| Allocation | Available accounts and resource state | Selection rationale, reservation, and outbound-call intent | Dispatch/ledger | Not implemented |
| Execution | An approved attempt | Output, usage, and execution report | Adapter | Not implemented |
| Settlement | Report or lost-contact evidence | Operation result, balance, and query records | Ledger/integration | Not implemented |
| Handling and reuse | Error, block, or unknown | Wait, cancel, verify, fix configuration, or define a new action | Business/operations | Not implemented |

Reverse boundary: an outbound call that has not started can be cancelled and released; cancelling after the outbound call does not retract business side effects or refund. An email already submitted cannot be retracted by ANT, and AI consumption that has already occurred cannot be deleted without basis. Verification can append evidence and correct erroneous accounting, but facts cannot be reset by erasing history.

## 5. Scope and Non-Goals

The first version must deliver the Go server and standalone Web, provider/resource configuration, heterogeneous logical resource pools, manual priority, and server failover dispatch; it first completes a real closed loop with one account in one domain, then dispatch across two independent accounts. Idempotency, reservation, settlement, recovery, authorization queries, and operational disposition are delivered at the same time.

The metering, rate-limiting, concurrency, periodic, expiry, overage, and shared quota rules genuinely required by the first batch of providers must be expressible. If a rolling window or shared quota is required for admission, it belongs to the first version; when it cannot be expressed, that account must not be admitted.

Not doing for now: automatic adaptation of arbitrary protocols, cross-currency conversion, cumulative budget systems, AI arbitrary-protocol translation, adaptive ranking, plugin marketplaces, public multi-tenant commercial platforms, and unverified real outbound calls in a second domain. The graphical configuration console belongs to the first version; the frontend may be plain, but the configuration/result closed loop and error recovery must be usable.

Execution adaptation for new domains is enabled when a real need arises after the first closed loop; configuration and pool organization for multiple resource types is not a second-domain outbound-call extension and must be implemented in the first version. Unimplemented capabilities are clearly labeled.

## 6. Functional Requirements

Status of all requirements: to be implemented, to be verified.

| ID | Requirement | Business Value and Acceptance Focus |
|---|---|---|
| R01 | Supported providers ship verified presets, complete configuration examples, and prerequisites | Integrators do not write accounting rules themselves; examples are verifiable |
| R02 | Validate credential references, account scope, plans, metering, capabilities, and configuration relationships | Errors are found before calls; how to fix them is explained |
| R03 | Logical IDs bind to request semantics; the same ID with the same content corresponds to exactly one logical execution | Concurrency yields only one execution right; different content conflicts |
| R04 | Filter and select by capability, designated account/binding, spending policy, quota, and block status | Evidence of the selection at that time is preserved; required affinity is not silently bypassed |
| R05 | Atomically reserve all applicable resources; failure leaves no partial occupancy | Concurrency does not exceed the configured admission capacity |
| R06 | One outbound call corresponds to one persisted attempt; adapters must not hide retries or switch accounts | Every external side effect is traceable and independently accountable |
| R07 | Success, failure, partial consumption, cancellation, and unknown all have explicit settlement | Actual usage exceeding the reservation is still recorded truthfully; repeated settlement does not double-deduct |
| R08 | Known failures are reallocated only per business rules; unknown terminates automatic attempts | Email with uncertain delivery must not be automatically resent; there are attempt and deadline caps |
| R09 | Submission replies are separate from business results; reasons such as quota, configuration change, and spending rejection are distinguishable | Defaults fail fast without queueing; final states are resolved by a unified priority, adding no states for providers |
| R10 | Long tasks renew leases, lost-contact tasks converge, and duplicate or late events are controlled | Known-unexecuted is released; unknown consumption is conservative, and real occupancy is not assumed released because a lease ended |
| R11 | AI supports streaming output and cancellation; disconnection, unconsumed output, and backpressure have convergent behavior | After partial output is visible, results from another provider are not automatically stitched on |
| R12 | Query operations, accounts, and bindings, explaining historical candidates, selections, attempts, settlements, and blocks | Both success and failure have historical evidence; old decisions are not fabricated per new configuration |
| R13 | Support verification, appended adjustments, and evidence-based release of blocks | History is not rewritten; adjustments do not automatically resend |
| R14 | Isolate trusted business namespaces and operational permissions, protecting credentials and content | Query/cancel/adjust cannot exceed authority; logs contain no secrets |
| R15 | Configuration updates are validated first and the old configuration is kept on failure; updates do not zero out quota | The fixed metering plan is separate from the current safe authorization, and the old plan does not bypass new blocks |
| R16 | Results come with explicit statements of persistence, delivery, and replay capability | The server execution lifecycle is separate from persistent queries; after a caller interruption, retrieval is by the original ID |
| R17 | When real storage is unavailable, no new outbound calls start; after backup restore, historical gaps are verified | No duplicate business actions are created because of ledger loss |
| R18 | Providers, business, resources, ranking, and deployment extend via explicit interfaces | Ordinary extensions do not change deduplication, send permits, or settlement; core invariants cannot be switched off by plugins |
| R19 | Distinguish consumption counting from execution occupancy; after unknown, resources not proven released are retained | Slots still executing in the background are not reallocated; uncertain window ownership is isolated by scope |
| R20 | Preparation and send permits check current admission, cancellation, and business guards | Disabling, credential revocation, spending tightening, and email suppression cannot be bypassed with old snapshots |
| R21 | The Web configures providers, credential references, accounts/bindings, each resource type, and their rules | Draft/validate/activate closed loop; erroneous fields are fixable, the old configuration is preserved, and secrets are not echoed |
| R22 | Different types of offers are grouped into logical resource pools without duplicating the underlying shared resources | Grouped by capability/metering unit; balances and unknown occupancy are shared across pools, with no mixed fake totals |
| R23 | Users manually adjust pool member priority, and the server executes the real order with safe failover | Group-wide atomic save/version conflicts; new requests follow the order; in-flight attempts are not migrated/unknown is not resent |
| R24 | Go resident backend and standalone Web, with configuration and execution over an authorized network contract | HTTP commands + SSE status/output; a browser disconnect does not end server accounting, and persistent results are retrievable |
| R25 | ANT internal extensions can declare and inject typed Hooks at business lifecycle boundaries explicitly opened by the core | The composition root can configure versioned Guard, CandidateOrderer, Transformer, or Observer for named points; the sealed manifest/revision (including the Hook API version and effective budget) is auditable; transformations take effect only in explicitly authorized phases and are progressively verified by the core; extensions must not obtain or bypass authorization, ledger, single-send, or final-state control |

### 6.1 Retrying After No Capacity

Once a logical operation is accepted and created, its final-state result is stable. Resubmitting the same ID while the no-capacity final state persists still returns the original result; it will not quietly execute after quota recovers.

After waiting for recovery, the business can explicitly create a new logical action. retrySafety=proved_safe/unsafe/undetermined is used uniformly; proved_safe applies only when the previous action is proven to have no non-repeatable side effects, and unknown must not automatically switch IDs on this basis. A first request rejected before basic validation creates no logical record; this boundary must be distinguished in the API.

By default the Router does not wait for capacity or sleep on Retry-After; the deadline is the overall time limit for this attempt, not a queueing permit. A temporary rate limit can also immediately form a stable failure, with the estimated recovery time serving only for the host to schedule subsequent business actions; the first version has no implicit wait queue.

### 6.2 User Behavior for unknown

Returns the known outbound call, conservative consumption, a verification entry point, and the duplicate risk. Later confirmation can append verification results and correct accounting, but the original unknown final state and evidence are preserved. Whether to initiate a new action is decided by the business, not implicitly triggered by a ledger adjustment.

### 6.3 Email Result Boundary

If the first business chooses email, "provider accepted the submission" and "final delivery" must be distinguished. By default, first-version Router success only means the submission was accepted. Bounces, complaints, suppression lists, and final delivery are the responsibility of the host's email process; adapter admission verifies cross-provider suppression/verified sender identity. If actual requirements include a final delivery guarantee, T0 must bring receipts and the suppression closed loop into scope.

Dynamic recipient suppression is provided uniformly by the host; switching providers does not bypass it. Necessary guards are verified before admission and send permits; when the required state cannot be obtained, ANT blocks. Static presets record verification capabilities and responsibilities, and do not impersonate real-time suppression state.

### 6.4 Cancellation Requests and Factual Results

A successful reply to cancel only means the stop intent was recorded and new attempts are prevented. When business success is confirmed, it still returns succeeded; when stopping is confirmed and the business did not complete, it can be cancelled; when the external result cannot be confirmed, it is unknown. Consumption and unreleased occupancy are shown separately, and cancellation does not refund or erase history. A final state is not rewritten by a late cancellation, and the same ID replays stably.

When cancellation and no-capacity/budget exhaustion both hold before result submission, and the external action is confirmed unexecuted or known finished, cancelled takes priority while the competing reasons remain queryable. Confirmed success and an unknown external result each take priority over cancellation; a cancellation arriving after the result is submitted does not change the final state. The whole system resolves this by the single set of rules in DESIGN §7.1.

### 6.5 Constraints on "Flexibility"

Adding providers, credentials, business metering, or ranking must have an explicit landing point and cannot rely on adding provider special cases in the Router. Extensions only propose capability, plan, ranking, or resource changes; the core uniformly guards authorization, atomicity, deduplication, and recovery. The first version does not implement a dynamic plugin marketplace; genuinely new semantics beyond the guarantees still require an explicit design review.

On 2026-10-04 the user explicitly required the ANT internal Hooks mechanism to support more flexible lifecycle extension. This requirement supersedes the old restriction above of "not opening the ranking registry for now": default ranking is still handled by the `rankCandidates` pure function; a trusted composition root can, at startup, register Guard, CandidateOrderer, Transformer, or Observer for named lifecycle boundaries explicitly opened by the core. Custom points can cover phases the core explicitly delineates and take effect only when the core invokes that point on the real execution path; dynamic loading or arbitrary instrumentation that bypasses core code is not provided. Transformers are open only for explicitly enabled points, and the core must validate the initial data and the result of every transformation step, rejecting the entire transformation chain on failure without returning partial results. Observers only receive immutable events after persistent commit, cannot rewrite results, and cannot synchronously block the business main path. Hooks of any type must not obtain secrets, Store, Ledger, or Transport permissions, nor replace the core's authority, transaction, permit, deduplication, accounting, and final-state control. For concrete call points, budgets, and failure semantics see [Hooks Design](design/10-HOOKS.md); new types still require defining the type contract and core independent-review rules first.

### 6.6 Long Task Retrieval and Observation Refresh

After the server takes over, it returns accepted plus query/event addresses; in-process Promises are not passed. When the browser disconnects but the server survives, saving and settlement continue; if the server exits, recovery follows the lease and may end unknown, with no promise of an automatic second outbound call. On reconnect, the persistent events/results/available body for the same ID are read, without re-attaching to the original provider network stream.

Observation refresh is triggered on demand by operators via Query/Admin; the first version has no implicit periodic collection or automatic retry. When a trusted capacity API exists, evidence is gathered outside the transaction; without an API, well-founded material is imported; the Ledger only verifies and saves. A failed observation refresh shows the next step and does not trigger business resending or increase capacity; when the required upper-bound/initial-state evidence is untrusted, the block remains.

### 6.7 Resource Pools and Manual Priority

Logical pools can organize multiple described resource offer types such as AI tokens/amounts, email counts, and storage; compatible candidates are selected by request domain and capability, and each unit is displayed/accounted independently. One attempt selects one complete resource bundle and cannot make up a balance with units of another type. Multiple pools referencing the same resource identity still share the real capacity.

Manual priority decides ordering across offers; the entitlement deduction order inside a provider account must follow verified facts and cannot be changed by drag-and-drop. Lower priority numbers go first, with offerId breaking ties; candidates lacking capability, quota, evidence, permission, or spending permission are skipped with an explanation. Order changes are saved atomically, with drafts kept on conflict; the original path is kept during execution, unsent permits are re-checked against the latest version, and unknown is not resent because of reordering.

The UI is the only main configuration path, and the server is the adjudicator of admission/failover. Body streams and cancellation can use SSE and HTTP in parallel; audio/video duplex sessions with continuous input require separate design, without pretending the first-version text API already supports them.

## 7. Quota and Charge Rules

- Limits and entitlements must match the provider's real rules; the same commercial fact must not be represented twice.
- When multiple keys share the same actual quota, they must share a quota identity, or the configuration must be explicitly rejected; it cannot be disguised as multiple independent balances.
- An authentication failure on a single credential does not by default exhaust or disable other legitimate channels of the same account; account-level billing problems cannot be bypassed by swapping keys.
- allowMetered=false rejects candidates known to require charged capacity. A well-founded zero-spend commitment can be made only when conditions such as a reliable provider billing upper bound, complete consumption records, a trusted initial balance, and a hard stop all hold.
- Upper bounds have enforceable sources and verification evidence per billing dimension; unenforced request parameters or rule-of-thumb values cannot be treated as reliable upper bounds.
- The first version reserves directly against proven upper bounds with no implicit scaling of reservations or capacity; when external calls or unknown billing items break the guarantee, ANT blocks in strict mode with an explanation.
- An over-limit result should state which rule blocked it, when recovery is expected, or what manual handling is needed; releasing a block does not increase the balance.
- The first version of ANT does not sell end-user quota and does not take on charging, refunds, or revoking provider bills. Consumption that has actually occurred must not be deleted because of cancellation.
- Leases and local timeouts are not evidence that external execution has ended. After an operation goes unknown, concurrency occupancy or cross-period isolation may still be held; the reason and evidence-gathering path must be shown, and capacity cannot be recovered via month rollover, restarts, or groundless purges.

## 8. Non-Functional Requirements

| ID | Requirement and Acceptance Method |
|---|---|
| N01 Correctness | Preserve R03–R10 and R19/R20 under concurrency, out-of-order events, duplicates, staleness, and crashes; record migrations and explain externally unfenceable boundaries |
| N02 Onboarding | With prerequisite credentials and external settings in place, a developer who did not participate in development completes the minimal action within 60 seconds following the guide, or sees real availability and the next step; actual elapsed time is recorded |
| N03 Usability | Errors state what happened, how to handle it, and whether results/consumption are preserved; blocks have release methods |
| N04 Performance | After freezing the load, measure ANT's added p95/p99, throughput, lock waits, and rejection rate; kept separate from provider latency |
| N05 Observability | Decisions and state changes are traceable by operationId/attemptId/accountId, with sensitive information redacted |
| N06 Durability and Recovery | Reservations, intents, and final states persist under real storage; lost contact and backup restore have verifiable dispositions |
| N07 Maintainability | Provider presets carry verification time, source, applicable plans, and maintenance responsibility; changes are traceable to requirements and acceptance |

Candidate performance targets (unconfirmed, unmeasured): 10 accounts, up to 8 resources per account, 1 million historical usage records; 20 operations/second sustained for 30 minutes, 50 concurrent long tasks; ANT's added latency from submission to launch permit p95≤50 ms, p99≤100 ms; no unhandled lock errors under normal load; no sustained reservation leaks after a 2-hour representative run. D04 should freeze or adjust these based on actual deployment and business, and must not lower the targets after the fact to mask failure.

## 9. Acceptance Criteria

| ID/Requirement | Given | When | Then/Evidence |
|---|---|---|---|
| A01/R03 | Same ID, same content | Concurrent submissions | One logical execution right; approved outbound calls are not duplicated by concurrency |
| A02/R03,R16 | Different content or a finalized same ID | Resubmission | Conflict or stable replay; no new outbound call |
| A03/R05 | Capacity 50, each reservation 1 and held without release | 100 concurrent requests | Exactly 50 succeed; failures leave no residue |
| A04/R10 | No attempt after claim, or crash after prepared, with cancel intent present/absent respectively | Lease-expiry sweep and old owner recovery | Converge by unified priority; claim cases with no cancellation and no higher-priority fact return ExecutionInterrupted/owner_lease_expired_before_dispatch; releaseable reservations are released, and the old owner cannot start |
| A05/R08,R10 | executing loses contact | Sweep and late report | unknown settles conservatively with no automatic resend; evidence is verifiable |
| A06/R07,R10 | Lease renewal/sweep, duplicate/conflicting reports | Competing updates | One legitimate result, no double deduction |
| A07/R05,R15 | Window boundary or configuration switch | Launch after preparation, completion across the boundary | Attribution is correct; quota cannot be zeroed via switching |
| A08/R07,R13 | Old observations, negative adjustments, actual usage over reservation | Recalculation/settlement | No wrongful loosening; overage is recorded truthfully |
| A09/R04,R08,R09 | Two accounts with hold/affinity | Execution or no capacity | Decisions follow policy; known failure and unknown have different follow-ups |
| A10/R01,R02,N02 | Prerequisites complete, integrator not involved in development | Onboarding per the guide | No accounting model design needed; elapsed time and sticking points recorded |
| A11/R11,R16 | Long stream, disconnection, cancellation, or no consumer, from caller interruption or execution host exit respectively | Query the original ID or recovery sweep | While the host survives, settlement continues; on host exit, recovery follows facts; persistent results are retrievable, and the original provider network stream is not rebuilt |
| A12/R12,R13 | Authentication/quota block or unknown | Fix/verify | Disposition is well-founded, history preserved, and subsequent queries accurate |
| A13/R14 | Unauthorized namespace or operational identity | Query/cancel/adjust | Access denied; audit recorded, no secret leaks |
| A14/R17 | Storage failure or old backup restore | New submission | Outbound calls blocked; gaps verified before recovery |
| A15/R07,R09,R11 | Cancellation competing with success/unknown/no-capacity/budget exhaustion, cancellation before or after final-state submission respectively | Unified resolution and query | Factual success, unknown, effective cancellation, and failure are ordered by priority; a submitted final state is not changed, and consumption is preserved |
| A16/R09,R15,R20 | Already prepared, account disabled or spending tightened | Obtain launch permit/resubmit | An outbound call not yet started is rejected with the specific reason saved; the old plan cannot loosen authorization |
| A17/R02,R05 | Two bindings sharing a total quota with one invalid credential | Concurrent reservation and authentication failure | Capacity is not doubled; authentication isolates only the corresponding binding, while an account billing hold is shared |
| A18/R02,R07 | Upper-bound evidence missing a dimension, expired, or parameter unenforced | Strict candidate admission | Rejected; sources with sufficiently enforced upper bounds pass, not limited to a specific parameter name |
| A19/R04,R12 | Configuration/balance change after a successful selection | Query the old operation | Returns the selection, version, and preparation races at that time; not recalculated per new configuration |
| A20/R06,R10,R20 | Pause across leases before/after the final pre-send validation | Recovery/sweep | The former has zero outbound calls; the latter, if a late send occurs, is recorded conservatively with no automatic resend, without claiming an absolute fence |
| A21/R02,R20 | Email target suppressed by the host or invalid sender identity | Provider switch/evidence gathering before permit | All non-compliant paths are blocked; guards fail closed; applies to the email domain only |
| A22/R05,R19 | Concurrency limit 1, A already unknown but the provider still executing | B's request and subsequent completion evidence gathering | B does not reuse the slot; released once after evidence confirms completion, and window reset does not clear the unknown isolation |
| A23/R18 | Built-in/new resource plans deliberately violating the applicable items of DESIGN I01–I10; or reporting real over-reservation | Validation and submission | Illegal identity/unit/version/reservation/release/permit/final state is rejected; legitimate overage facts are recorded and block |
| A24/R09 | Candidate rate-limited for only 2 seconds, operation deadline 60 seconds | Submit this operation | Stable block without waiting the 2 seconds, returning the estimated time; resubmission with the original ID still replays without an outbound call |
| A25/R12,R13 | An adjustment invalidates the observation, source collection fails, or the window/adjustment version changes during collection | On-demand refresh/query | The Ledger makes no network calls, failures are visible, stale evidence is rejected; no capacity increase, no business resend, and trusted new observations are correctly stored |

| A26/R21 | Real type descriptions, errors, and a correct resource draft | Configure/validate/activate from the Web | A correct configuration reads back consistently via GET and is executable; errors point out the fields and the old activated version is preserved; secrets are not echoed |
| A27/R22 | Heterogeneous offers in one pool, the same physical resource across pools | AI filtering/concurrent calls/calling again after unknown | Only compatible paths are selected, displayed per unit; shared references do not increase quota or clear unknown occupancy |
| A28/R23 | Two available offers, two tabs with the same pool version, with prepared/executing attempts | Manual order save/concurrent modification/new request | The atomic order decides new server selection; a 409 conflict keeps the draft; prepared is re-checked, executing is not migrated, and old history is not recalculated |
| A29/R24 | Go resident server, Web subscription, two permission identities | Disconnect/reconnect/cancel/unauthorized call | The same ID reads ordered persistent events/results with no duplicate outbound calls; a cancel ACK does not impersonate a result, and unauthorized access is rejected |
| A30/R25 | Built-in and custom lifecycle points, declaring versioned Guard/CandidateOrderer/Transformer/Observer capabilities per phase | Registration/seal/real invocation points, and persistence of a manifest revision including the Hook API version and effective budget; deadline, panic, validator failure, and Observer fault injection | Permitted Hooks take effect at real invocation points in a stable order; decision records include the sealed registry revision and phase results, and the applicable Hooks' IDs/versions can be reconstructed from the manifest; ranking returns only the original candidate set; Transformers take effect only at explicitly opened points, with the initial value and every step's output validated by the core validator and no partial results on failure; over-budget, undeclared type, deadline, and panic all fail closed per contract; no Hook bypasses identity, permits, ledger, single-send, or final state; Observers only see post-commit redacted snapshots, and their failure does not change committed results or block the main path |

Evidence must include build and configuration versions, deployment form, inputs, expectations, and actuals; simulated faults and real provider records are labeled separately. Everything is currently to be verified.

## 10. Risks and Phase Exits

Value risk is handled by T0's real scenario and minimal verification; usability risk by presets and the first onboarding walkthrough; feasibility risk by provider admission and the early real slice; commercial risk by spending-commitment boundaries and hard-stop verification.

Phase exits are in DESIGN Section 10; specific tasks, execution criteria, and acceptance evidence are in [Implementation Plan](plans/README.md). The single status record for D01–D07 is in [Plan Progress and Decisions](plans/STATUS.md); requirements and plans must not lower each other's acceptance requirements.

Passing requirements and design reviews does not mean passing go-live; stability, usability, and performance must be accepted separately with implementation evidence.
