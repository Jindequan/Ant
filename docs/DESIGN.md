# ANT Design Document

Version: V0.9 technical design baseline · Date: 2026-10-04 · Status: retains the V0.8 core boundaries; refines ANT internal Hooks checkpoints, data transformations constrained by the core validator, and the registration framework; the real execution chain wiring still awaits T2/T3.

Requirements basis: [PRD](PRD.md); version and authority: [README](README.md); detailed design: [Technical Design Entry Point](design/README.md); execution: [Implementation Plans](plans/README.md). This version retains the existing invariants and expands engineering, module, data, transaction, and business algorithm details; the Go core contracts and the SQLite reference migration already exist as attachments, and T1 is responsible for implementing and verifying them, so the core interfaces are no longer designed from scratch. D01–D07 status is maintained only in [STATUS](plans/STATUS.md).

## 1. Architecture Goals and Scope of Guarantees

The core goals are: correct allocation of shared resources, traceability of every outbound call, explainable business results, and clear landing points for provider and business extensions. The first release delivers the configuration UI, resource pools, and server-side dispatch; the first real business case serves as the outbound-call acceptance slice; no arbitrary plugin platform or standalone workflow engine is built.

The technical baseline is a Go server plus an independent React/TypeScript/Vite web. Provider, resource, and pool configuration live in the UI; authentication, current admission, manual priority, failover, outbound calls, accounting, and recovery all live in the server. The single-host reference Store is SQLite via modernc.org/sqlite, with one write connection / a bounded writer goroutine and an independent read connection pool; the web build artifacts can be hosted same-origin. See [Engineering Design](design/01-ARCHITECTURE.md) for concrete directories / Context / performance mechanisms. Production multi-host quota sharing requires a shared transactional Store; Node does not participate in backend execution, and the legacy library + CLI form has been archived. The D07 specification is settled by user ruling; environment compatibility evidence still must be verified.

| Aspect | What ANT can stipulate | Conditions / boundaries that must not be conflated |
|---|---|---|
| Logical identity | One execution right per namespace/operationId, with stable terminal-state replay | The persistent ledger must be complete; backup gaps must be audited |
| Network send | One permit calls the core send entry point at most once, and all attempts are traceable | Not the same as provider-side external exactly-once; hidden retries and automatic redirection are forbidden |
| Quota | Atomic admission under a complete local consumption entry point and reliable upper bounds | Cannot replace provider hard limits, reliable time attribution, or external consumption control |
| Spend | Reject known billable capacity; reserve strictly where reliability allows | allowMetered=false is not by itself an unconditional zero-bill guarantee |
| Recovery | Not-yet-executed items can be released; when uncertain, account conservatively and isolate the necessary resources | Lease expiry does not prove the external request stopped; unknown is never resent |

SQLite WAL remains single-writer and requires all participating processes on the same host; PostgreSQL row locks can realize a shared transaction store. Both [S1][S2] performance claims must be backed by real measurements; capacity cannot be claimed by estimating from transaction volume.

## 2. Stable Core and Extension Points

### 2.1 Core Responsibilities

| Module | Responsibility | Rules extensions must not bypass |
|---|---|---|
| Router | Call entry point, operation ownership, attempt budget, deadlines, cancellation intent, result coordination | Deduplication, terminal states, single active attempt, safe reallocation |
| Ledger | Resource snapshots, reservations, intents, permits, leases, settlement, and verification | Atomicity, idempotency, state guards, resource identity, and audit |
| Transport | Holds a single DispatchPermit, performs final validation and sending, and collects transport evidence | One call, no implicit retries / redirection, never reuse an invalidated permit |
| ConfigCompiler | Compiles configuration and presets into ExecutionPlan and LiveAdmission rules | Validation of references, units, evidence, identity, and version compatibility |
| Query/Admin | Reads historical decisions, results, and occupancy; performs authorized dispositions | Does not rewrite original results, and does not treat adjustments or hold removal as send commands |

### 2.2 Constrained Extension Interfaces

| Extension point | Inputs / outputs and role | Typical changes |
|---|---|---|
| ProviderAdapter | Plan→request encoding; response→domain report; network sends go through the core Transport | New providers, protocols, and endpoints |
| DomainPolicy/DomainGuard | Request→capabilities, metric upper bounds, and safety rules; current business state→admission evidence | AI/email, new metering mappings, send authorization / suppression |
| ResourceSemantics | Transactional resource snapshot + demand + report→deterministic admission / accounting change plan | Fixed windows, rolling rules, counting, occupancy, or entitlement |
| LedgerStore/SecretResolver | Transactional storage / credential resolution with identical semantics | Deployment and secure storage changes |

Runtime Hooks attach to lifecycle checkpoints per the [Hooks Design](design/10-HOOKS.md). A trusted composition root may declare named checkpoints and the allowed typed Hooks at startup, or append capabilities to existing checkpoints before Seal; the core must invoke them explicitly at the corresponding boundaries. Hooks may add controlled decisions, ordering, and observation, as well as transform phase data only at explicitly enabled lifecycle points; transformations must pass the validator provided by the core, after which the core's original flow still performs authorization, state transitions, and accounting commits. Hooks do not run inside ledger transactions and cannot access secrets or raw request / response bodies.

The first release only needs the built-in rules it requires; registration is done explicitly in code, and unknown code is never loaded dynamically. ResourceSemantics does not touch the network, does not read the clock itself, and does not write raw SQL; time and state are injected through transaction snapshots. Returned change plans are committed uniformly by the Ledger only after it validates units, windows, versions, resource references, and invariants. The database implementation owns atomicity; semantic extensions cannot change the core flow's authorization or terminal states.

DomainGuard's network / host queries obtain versioned evidence outside transactions; their validity is verified before permits are started. First-release ordering defaults to the internal pure function rankCandidates, which neither reserves nor sends; the newly added internal Hooks mechanism lets users attach a CandidateOrderer at boundaries that are explicitly declared and invoked by the core, but the output can still only be an exact permutation of the core's admission candidates. Transformers can be explicitly opened via the composition root to built-in or custom phase points, but every step of data must be validated stepwise by the core; authorization, permit, accounting, and terminal-state decisions cannot be changed. ProviderAdapter does not privately use auto-retry SDKs; dependencies that do not support the controlled Transport cannot be admitted. Adding real semantics requires new rules and shared contract tests, not silently flipping configuration switches to turn off safety checks.

### 2.3 Core Invariants and the Extension Change Plan

The output of ResourceSemantics is called ResourceChangePlan: it contains the affected resource/window, expected versions, admission conclusions, reservation / consumption / occupancy changes annotated per unit, evidence references, and diagnostic markers. It must not carry arbitrary SQL, arbitrary new capacity, or send commands. T1 fixes the legal change kinds and their corresponding types.

| ID | Mandatory rule | Verification responsibility |
|---|---|---|
| I01 Identity authorization | Namespace and operation identity are correct; resources come from compiled references and current authorization; new windows / resources must not be fabricated to re-mint quota | Ledger validates; configuration verifies identity-sharing relationships |
| I02 Units and values | Units, multipliers, and window identity match the plan; reservations are non-negative and finite; positive and negative adjustments carry explicit semantics and evidence; NaN/Infinity are not accepted | Ledger; domain projection contract |
| I03 State versioning | Updates conform to expectedState, owner/generation, and resource versions; terminal states are never revived; stale holders cannot obtain new permits | Ledger conditional commit |
| I04 Admission atomicity | prepare/permit checks all applicable resources, cancellation, gates, budget, and evidence; any failure must not leave partial changes or permits behind | Ledger transaction |
| I05 Reservation conversion | Each reservation can be consumed, released, or converted to unconfirmed occupancy exactly once; splitting holdings must not add or remove amounts out of thin air; actual over-reservation is recorded separately as an overage fact instead of being truncated | Ledger structural validation + rule contract |
| I06 Occupancy conservatism | Active, unconfirmed, and released sets are mutually exclusive; lease/reset does not delete external unknown occupancy; a release carries corresponding end / attribution evidence | Ledger + ResourceSemantics contract |
| I07 Accounting idempotency | Event, settlement, adjustment, and verification IDs are unique; replays never double-deduct; conflicting records never overwrite existing facts; events and summaries commit in the same transaction | Ledger/Store |
| I08 Result priority | Operation results are resolved uniquely by §7.1; cancellation or admission rejection does not erase consumption / occupancy; late facts go through verification | Router coordinates; the Ledger calls a pure resolution function inside the transaction and commits |
| I09 Single outbound call | An attempt holds at most one valid send permit; budget is counted conservatively over permits already granted; Transport never reuses permits or hides retries | Ledger permit constraints + Transport |
| I10 Faithful recording | Consumption that already happened is still settled and marked overrun even when over limit; observations never create unknown capacity; accounting corrections require authorization, provenance, and audit | Ledger; upper bounds and provider facts are verified by evidence |

The A23 test cases must include at least: wrong units, stale versions, unauthorized resources, over-limit reservations, duplicate settlements, terminal-state revival, releases without end evidence, and duplicate permit consumption are all rejected; settlement of a real over-reservation is accepted and blocks subsequent allocation. The semantics of trusted rule code cannot be proven automatically by a structural validator, so rule contract test cases remain mandatory; whether the provider enforces the upper bound is proven by BoundEvidence, and it must not be falsely claimed that the Ledger has already verified it.

### 2.4 System Layers and Dependencies

The frontend web and the Go server are separate projects; inside the server, layering follows the responsibilities below, initially keeping a single resident deployment unit.

| Layer | Composition and responsibility | Dependencies and boundaries |
|---|---|---|
| Access layer | Web configuration and viewing; Go HTTP API, authorization, DTOs, and SSE; CLI only for bootstrap / operational assistance | Calls application entry points; does not directly modify resources or send provider requests |
| Application layer | Router, Query/Admin; operation lifecycle, execution coordination, queries, and authorized dispositions | Calls contract ports; no provider-specific special-casing or raw SQL |
| Domain and configuration layer | DomainPolicy/Guard, ConfigCompiler, rankCandidates, ResourceSemantics, result resolution | Deterministic rules process injected state / time / evidence; dynamic evidence gathering executes outside transactions through ports |
| Consistency and send core | Ledger, Transport; all-resource atomic commits, single permits, settlement, and unknown occupancy | Ledger exclusively owns the persistent mutation entry point; Transport exclusively owns the outbound call entry point; the network never enters resource transactions |
| Infrastructure and adapter layer | One LedgerStore, ProviderAdapter, SecretResolver, Clock, and the required guard sources | Implements the ports defined by the layers above; injected at configuration assembly; reverse control of the Router or fabricating separate balances is forbidden |

Code dependencies center on contracts / pure domain types: infrastructure implements contracts, the application depends on contracts, and the runtime entry point assembles the concrete implementations. Types and pure rules do not import database drivers, provider SDKs, the CLI, or the process environment. The Ledger may use pure ResourceSemantics, and the Transport may use ProviderAdapter codecs; neither may bypass the other's authorization / commit boundaries.

The path of one call is: authorization entry point→compile and claim→quote / ordering→atomic prepare→atomic permit→Transport final validation and single send→domain report→atomic settle→completion / persistent queries. Guard evidence gathering, encoding, and credential resolution complete before the permit; queries / adjustments / verification are independent administrative paths and cannot be wired back into sending. See [Engineering and Modules](design/01-ARCHITECTURE.md) for concrete directories, goroutines, and source responsibilities, and [Execution Algorithms](design/03-EXECUTION.md) for each step's reads/writes and error paths.

### 2.5 Technical Attachments and Resource Pools

[Detailed Design Entry Point](design/README.md) uniformly lists the Go/web modules, Go DTOs / internal ports, the 34-table reference migration, execution algorithms, resource rules, configuration pools, API / SSE, page contracts, and the implementation mapping; they are all part of this design. Passing type / SQL attachment validation does not mean the runtime implementation or performance has passed.

ResourceOffer references a binding plus every resource required by one call; ResourcePool organizes the different kinds of offers within a namespace, and PoolMember stores enabled / priority / version. One offer can be shared by multiple pools over the same underlying resource, creating no new balances. User priority determines the cross-offer dispatch order, while providerOrder determines the actual entitlement deduction within an account; the two must not be conflated. Different domains / units are grouped by capability, and pool total quota must not be fabricated by summation. See [Resource Pools and Configuration](design/07-POOLS-AND-CONFIG.md) for details.

The web can only save drafts, trigger validate-and-activate, and modify manual ordering; the server validates the complete resource mapping / evidence and permissions. An ordering change updates members / pool version / configuration pointers / control version / audit in one transaction, and the draft is preserved on 409. The current ordering is not the browser list order; poolVersion is validated before sending. An in-flight attempt keeps its original plan; unsent old plans converge according to reliable evidence, and ordering must never be used as a pretext to automatically resend unknown.

## 3. Identity, Plans, and Live Admission

### 3.1 Separating Quota Owners from Call Paths

| Object | Identity and responsibility |
|---|---|
| AccountingAccount | The actual quota / billing owner, with a stable accountId; assumes neither a unique executor nor a unique credential |
| ExecutionBinding | stable bindingId, accountId, domain, adapterRef, endpoint, credentialRef, credentialRevision, capabilities, resourceRefs |
| Resource | stable resourceId, ownerRef, metricId, unit, semanticsRef, limits / entitlements / windows; the owner may be an account or a binding |

One binding references one credential; multiple bindings may point to the same account and shared resources. Account totals and key-specific rates can jointly constrain one call. Additional keys do not add capacity. Sharing relationships come from verified configuration, never guessed from key strings; combinations that cannot be verified are rejected. Even when the first release needs only one binding, it uses the same identity structure.

Authentication failures act, according to evidence, on a binding or on credentialRef/credentialRevision; credential-level errors propagate to the bindings referencing the same credential revision, while endpoint configuration errors block only that binding. Billing failures act on the AccountingAccount; resource exhaustion acts on the specific Resource. Blocking granularity is determined by the scope the signal can prove, and is conservatively widened only when the scope is unknown. Cross-account offers may enter the same logical dispatch pool; cross-account commercial balances are not fabricated, and billable units without basis are not merged.

### 3.2 Fixed Plans and Live Gates

An ExecutionPlan fixes the domain/adapter/metering rule versions, request constraints, upper-bound evidence, resource mappings, cost, and window rules. Every attempt stores planId/planRevision, bindingId, credentialRevision, and a semantic digest; settlement interprets the real report according to that plan.

LiveAdmission stores the current account / binding enabled flags, credential revocation versions, spend policies, business guards, resource holds, and security control versions. prepare and permit start atomically check the current gates; reconfirm before sending. Security tightening stops actions not yet sent; a fixed old plan must not be treated as permanent authorization.

When new metering rules change reliable upper bounds or resource mappings, old estimates and new balances are never stitched together: the prepared attempt is recompiled / re-reserved, or config_changed is returned stably. Calls already started still settle under the old plan, with actual overages recorded faithfully. Loosening configuration does not automatically loosen the permits of existing operations; new requests use the new plan.

### 3.3 Persisted Objects

| Entity | Core content and constraints |
|---|---|
| Operation | unique on namespace+operationId; semanticFingerprint, state, owner/generation, lease, execution policy, cancellation intent, result |
| Attempt | id, operationId, binding/account, plan, generation, prepared/executing/terminal, permit, report / transport evidence |
| ReservationItem | attempt/resource/window, reserved quantity and type; every resource change has traceable ownership |
| ResourceState | unique on resource/window; counting summaries or occupancy sets, version; never forces every resource into a single used number |
| UsageEvent/Adjustment | append-only counting, unit, window, evidence; unique IDs for deduplication, summaries updated in the same transaction |
| Observation | same resource / window, remaining, cumulative watermark, source, and observation / recording / expiry times |
| DecisionRecord | candidates, rejection reasons, ordering keys, plan / policy versions, selected binding, preparation result |
| Hold/Encumbrance | blocking scope, reason, release evidence; external occupancy without a confirmed end is independent of the operation terminal state |
| Reconciliation | original operation / attempt, subsequent confirmations, accounting correction references, evidence, and operator; the original terminal state is never overwritten |

The namespace comes from the trusted host authorization context. semanticFingerprint includes poolId, the business domain, normalized side-effect content, required affinity, and request constraints that would change business output; it excludes deadline, maxAttempts, preferred affinity, trace, and automatically changing configuration versions. The latter are fixed at first acceptance, and resubmission does not modify existing operations. The field table and hash algorithm are fixed in T1.

Resource IDs are decoupled from configuration versions; changing rules requires an explicit balance migration, never resetting history to manufacture capacity. Integer minimal units or Decimal; units, multipliers, minimum billable quantities, and rounding are made explicit by presets. There is no legacy data migration burden today, but the rules are established from the first release.

## 4. Public Contracts: Submit Replies and Business Results Are Separate

```text
Router.Submit(ctx, trustedAuth, ExecuteInput) -> (SubmitReply, error)
SubmitReply = accepted(operationId/operationUrl/eventsUrl) |
              inProgress(OperationView) | replay(OperationResult) | rejected(ErrorDTO)
QueryAdmin.GetOperation / ReadOutput / ReadEvents / Inspect -> authorized projections
Ledger.RequestCancel -> CancellationAck
ConfigService.SaveDraft / ValidateDraft / ActivateDraft / ReorderPool -> committed version
QueryAdmin.RefreshObservation -> RefreshResult
```

For the specific wire fields and internal ports, see [Go contracts](design/contracts.go); for HTTP routes / status codes / SSE, see [API design](design/08-API-AND-EVENTS.md).

rejected is only for validation failures or identity conflicts where no operation was created; inProgress does not re-attach to an existing stream. StoreUnavailable means access failed and the operation may already have been persisted; the caller should query or resubmit with the same ID, and must not assume that "a database error equals not executed".

OperationResult contains operationId, state=succeeded/failed/cancelled/unknown, code, the reason, attempts, per-resource consumption/reservation status, decisionRefs, outputRef/availability, a reconciliation summary, and suggestedAction. Queries while executing return state=running and show cancelRequested, without falsely reporting cancellation.

| Scenario | Submit reply / operation result |
|---|---|
| Base request or config invalid before claim | rejected; no operation created |
| Same ID with a different semantic digest | rejected(OperationIdConflict); the original operation is unchanged |
| Same ID currently running | inProgress; no outbound call |
| Same ID already in a terminal state | replay; metadataOnly stated explicitly when the output body is missing |
| No candidate can be admitted | failed / AdmissionBlocked, with reasons containing capacity_exhausted/account_disabled/config_changed/policy_denied etc. |
| required binding / account unavailable | failed / AffinityUnavailable, with the specific reason listed separately |
| Attempt budget / deadline exhausted | failed / AttemptsExhausted or DeadlineExceeded, prior consumption retained |
| No attempt after claim, owner lease expired, and no cancellation or higher-priority fact | failed / ExecutionInterrupted, reason=owner_lease_expired_before_dispatch; states explicitly that no send permit was granted |
| Cancellation completed, business not successful, result known | cancelled / Cancelled; consumption recorded independently |
| Business success confirmed, including success despite a cancellation race | succeeded / Success; notes that cancellation did not prevent success |
| Active outbound call outcome cannot be confirmed | unknown / UnknownOutcome; not changed to cancelled even if the user had cancelled |

Top-level result categories are stable; detailed reasons are grouped, extensible, and carry source and handling suggestions, with raw provider errors only in sanitized diagnostic fields. Do not extend Operation.state each time a new provider is onboarded. A no-capacity case after claim still persists a stable terminal state; after recovery the business explicitly creates a new action, and retrySafety is only proved_safe/unsafe/undetermined — an unconfirmable case is never marked safe.

ExecutionInterrupted belongs to the runtime-recovery grouping, not to the quota/policy reasons of AdmissionBlocked. owner_lease_expired_before_dispatch is only for cases where a persistent record proves that no send permit was ever granted; it must not be used for a request already executing that lost contact — that case follows the unknown-outcome rules. T1 fixes the code/reason groupings, fields, and serialization samples; T3's A04/A05/A06 jointly cover claim with no attempt, prepared, executing lost contact, and their cancellation races, asserting that reasons stay stable and that unknown is not marked as not executed.

Base errors must also state that an accepted operation may exist; whenever a communication/storage response is lost, keeping the original ID is recommended. An unsaved full stream cannot promise replay. A cancellation ACK only means the cancellation intent was persisted / a terminal state already exists; the factual outcome is confirmed by the persisted terminal event and queries.

### 4.1 Resident Execution and Result Retrieval

submit returns accepted with HTTP 202 after validation, claim, and bounded takeover by ExecutionHost; it returns only serializable IDs / query / subscription addresses. The execution context comes from the server supervisor and the operation deadline, not bound to the browser request. If the browser leaves while the server stays alive, persistence and settlement continue; a server exit does not promise to automatically continue outbound calls, and conservative sweeping after expiry may yield unknown.

Terminal state / result references / events are persisted before delivery to the UI. GET OperationView includes attempts, consumption, independent reservations, cancellation intent, results, and verification. A new SSE subscription reads already-saved events / body ranges, deduplicates in order, does not resume the old network stream, and does not generate new outbound calls. Inaccessible storage means StoreUnavailable, never masquerading as business failed or not executed; keep querying by the original ID.

CancellationAck recorded/alreadyTerminal/notFound only means the intent was recorded / a terminal state already exists; a factual success or unknown takes precedence. Config / output / pool / operation permissions are all scoped by the server-authenticated identity; disabling things in the frontend is no substitute for authorization. The first version requires a resident server; after returning accepted it must not rely on a goroutine of an already-finished Serverless invocation to keep running.

### 4.2 Internal Interfaces and Responsibility Check

| Owner | Interface behavior | Definition / T1 fixed items |
|---|---|---|
| Router | execute orchestration, execution host takeover, attempt/cancellation coordination, and unified result resolution | §4/§4.1/§7.1; no independent persistence commands, state changes call the Ledger; query and admin entry points delegate to Query/Admin |
| Transport | consumes DispatchPermit, final validation before sending, single send, and transmission evidence | §7.3/§8; internal call contract, does not expose a business send entry point that bypasses the Router |
| Ledger | claim, tryPrepare, authorizeDispatch, consumeDispatchPermit, requestCancel, renewLease, settle, completeWithoutAttempt, sweep, reconcile | §7.2 / detailed execution design; command identity, conditional updates, results, and transactions |
| Ledger | quote, appendOutput, activateConfig, recordGuardEvidence, readControlSignals, recordObservation, adjust, clearHold | detailed storage / host design; windows, outputs, versions, permissions, idempotency, and failure reasons |
| LedgerStore | transactional snapshots and reads of operation/account/binding projections | §3.3/§4.1; same consistency semantics, Query/Admin authorizes before reading |
| ProviderAdapter | encode/decode, controlled single request; optional read-only system_probe encode/decode | §2.2/§8/§5.4; endpoints, bindings, resource mapping, and transmission evidence |
| DomainPolicy/Guard | match, upperBound, projectReport, safe-retry rules / business admission evidence | §5/§8; inputs/outputs, versions, and missing-field semantics |
| ConfigCompiler | compiles config into plans, resource mapping, and live gate definitions | §3/§9; reference, unit, upper-bound, and version-compatibility validation |
| ConfigService / API | draft / validate / activate / whole-group ordering; identity, network DTOs, and event subscriptions | detailed config / API / page design; the Ledger commits actual changes, no browser dispatch |
| Query/Admin | getOperation, inspect, cancel, refreshObservation, authorized adjustment / verification / release | §4/§5.4/§9; cancel persists intent via Ledger.requestCancel and the Router coordinates execution; cannot modify tables directly or resend |

Diagnostic collection also goes through the core send entry point; its read-only purpose, required resources, and credential permissions are specified by the preset, with no automatic exemption from quota or authentication blocking. When there is no read-only collection capability, refreshObservation only supports importing evidence-backed material; it does not add another sending system for this reason.

## 5. Resource Semantics and Reliable Evidence

### 5.1 Counting Consumption and Execution Reservations

Constraint handles admission and Pool represents real entitlement; each may exist alone or combined, and the same commercial fact is never counted twice. Another independent dimension is counting-type versus reservation-type:

| Resource semantics | Preparation / execution | Known end | unknown |
|---|---|---|---|
| Counting: request count, tokens, amount, entitlement | Reserve a reliable upper bound; set the window per plan | Known / conservative consumption appended, unconsumed difference released | Counted at the upper bound, not treated as a free failure; when time attribution is uncertain, isolate the relevant scope |
| Reservation: concurrent slots | Slot taken at prepare, still held during execution | Released only when an external end is proven | Becomes an uncertainty encumbrance, keeps holding the slot; lease expiry does not release it |

Ended and unknown items in the reservation set must not overlap. Only provider end evidence, a trusted enforcement deadline, or a verified externally initiated termination can release them; a local abort/timeout/lease is not external termination. Inability to obtain evidence may block indefinitely — that is a real capability limitation, and queries must show the verification action. A manual "clear hold" likewise cannot delete an external reservation without justification.

If an unknown request may only start / consume in a new period, the relevant admission isolation is retained across reset until end and attribution are proven; uncertainty must not be auto-cleared at the start of the month. Counts verified as attributable to a fixed window settle normally at the upper bound, without indiscriminately isolating all accounts.

### 5.2 Windows and waterfall

Fixed windows are minute/hour/day/week/month, including timezone, anchor, and month/DST definitions; the rolling semantics are a different ResourceSemantics, implemented only as admission requires. Non-resetting resources have lifetime instances; expiring entitlements have expiresAt.

When preparation crosses a reset, atomically re-verify / migrate the relevant reservations before starting again; on failure there is no outbound call. The Pool's cross-period attribution, deduction order, and validity must be verified; when they cannot be handled reliably, apply boundary blocking based on a trusted maximum execution time or refuse admission, and never book all delayed consumption back into the old window.

Pool splitting and the priority/min(expiry,nextRefill)/id order are used only when matching the provider's real waterfall. Configuration must not invent a consumption order. allowMetered=false rejects allocations known to be charged; an actual overage beyond the reservation is still honestly recorded as overdrawn/policy_overrun, blocking subsequent allocations.

### 5.3 BoundEvidence and DomainGuard

The upper bound for each consumption / billing dimension includes: metricId/unit, bound, enforcementSource, mapping-rule version, applicable endpoints / parameters, verification basis, and validity period. Sources may be request parameters the provider genuinely enforces, an enforced model total cap, or a contractual cap; empirical averages and unverified theoretical caps do not qualify. Actual dimensions such as reasoning, tool, cache, and error usage must not be left out of the accounting.

When not all required dimensions are covered, evidence has expired, or it cannot be proven that parameters actually take effect, strict mode rejects that binding. Provider billing hard stops, trusted initial balances, and complete consumption entry points are verified separately. The first version uses the proven upper bound directly for reserved amounts and takes capacity from verified configuration, with no implicit scaling between the two; unknown error cannot be turned into a reliable guarantee by inflating estimates.

DomainGuard gives the business allow/deny, scope, revision, and evidence validity period; it must not call host network inside a ledger transaction. When dynamic state is needed, re-obtain evidence and verify the version before granting the permit; if it cannot be obtained, refuse. Guard evidence TTL and checkpoint are fixed in T1, without claiming to eliminate every external race after the check.

### 5.4 Observation, Aggregation, and Adjustment

```text
localAvailable = effectiveCapacity - committed - adjustmentTotal - reserved
providerBound = observedRemaining
                - max(0, committedNow - committedAtObs) - reserved
effectiveAvailable = max(0, min(localAvailable, providerBound))
```

The formula applies only to counting capacity; reservation rules are computed over sets and uncertain items. adjustmentTotal increases consumption when positive and decreases it when negative; committed is appended monotonically. Observations must be for the same resource / window, unexpired, and not spanning a reset; the observation and the local watermark must come from a consistent source. Observations only tighten; the local side still needs a trusted initial state and complete consumption records.

An adjustment invalidates related old observations, which are re-collected after verification; invalidation cannot serve as evidence for releasing uncertainty. Accounting corrections, balance aggregation, and audit happen in the same transaction, with a unique adjustment ID for idempotency. Late observations / reports do not overwrite newer evidence, and unincurred consumption is not extrapolated from provider billing lag.

Observation collection is managed by Query/Admin and executed by the runtime host outside transactions; Ledger.recordObservation only validates, deduplicates, records the watermark, and persists — it makes no network calls. The first version defaults to manual on-demand refreshObservation, with no periodic polling or automatic retries. Presets supporting a verifiable capacity API may provide read-only system_probe encode/decode capability; without an API, import evidence from authorized sources, and material that cannot be reliably correlated with the watermark is diagnostic only and does not participate in admission computation.

Before collection, record the window, the committed watermark, and the adjustment version; at ingestion, check that the window has not switched, the evidence has not expired, and there are no incompatible adjustments, with consumption added in between conservatively deducted by the watermark difference. If a query itself consumes restricted resources, it must be modeled in the preset and go through controlled permits / accounting; if unmodeled, automatic network collection is forbidden, and manual consultation of external sources must still be included in the complete consumption boundary.

refreshObservation returns stored/rejectedStale/sourceUnavailable with reasons, and a failure never resends the business action. After an adjustment, inspect shows observationRefreshNeeded; operators refresh explicitly. When a trusted local baseline exists, invalid observations are ignored and configured capacity is not increased; if the evidence a strict policy depends on has expired or the baseline is untrusted, LiveAdmission stays blocked. D03 freezes source and freshness, D06 freezes call frequency, request timeouts, and failure handling; the default automatic retry count is zero, and periodic collection is left for later real demand.

## 6. Selection, Historical Interpretation, and Policy

Filter the enabled members of the authorized pool: domain / capability semantics, required affinity, current gates / business guards / evidence / resource quote / spend constraints. Remaining candidates are ordered by PoolMember.priority ASC, offerId ASC; the user's order is the primary sort and is not silently overridden by cost or preferred. allowMetered is a hard gate, unknown cost is not zero, and no cross-currency conversion is performed; detailed comparison rules are in the resource pool design.

quote does not consume quota. The internal rankCandidates only sorts and does not change admission facts; tryPrepare re-checks, and on failure continues with other candidates but under an overall deadline and a planned retry cap. maxAttempts conservatively counts attempts that have obtained a send permit, and does not return budget on the basis of an unproven send.

By default it does not wait for capacity, does not sleep in the Router per Retry-After, and does not treat the deadline as a queuing window. Even when all candidates are temporarily blocked, a stable failure and a suggested recovery time form quickly; the deadline only bounds the total time of this round of planning / outbound calls. If the business needs to wait, the host creates a new logical action after the original operation's terminal state and retrySafety are explicit; resubmitting the original ID does not automatically execute again. The first version provides no persistent waiting queue.

At the start of preparation, DecisionRecord saves: semanticFingerprint, poolId/poolVersion/offerId/memberPriority, plan / admission versions, candidate bindings, rejection reasons, sort keys, the selection, quote time, and the actual resource versions and outcomes at prepare time. The selected permit is committed in the same transaction as the record; the quote→prepare race and the final winner are recorded, and queries must not recompute history with the latest configuration.

All candidate evidence has entry-count / byte limits; the selected permit, key rejections, and versions must be saved, and exceeding the limit is marked traceTruncated, never silently pretending completeness. No credentials or bodies. In AdmissionBlocked, the estimatedAvailableAt of a capacity reason is only an estimate over known candidates, with unknown blockers itemized one by one; within a single candidate AND takes the maximum known release time, across candidates take the minimum, and availability at that time cannot be promised.

## 7. State, Cancellation, Atomicity, and Recovery

### 7.1 Operation State and Cancellation Precedence

| Current state | Event | Result |
|---|---|---|
| Not existing | Atomic claim | running; owner/generation, lease, and first-execution policy fixed |
| running | cancel request | Records cancelRequested; blocks new prepare/dispatch without immediately fabricating a business result |
| running | No outbound call and cancellation has taken effect, or external stop confirmed with business not completed | cancelled; consumption / reservations handled per facts |
| running | Full business success confirmed | succeeded, retaining the factual success even with a cancellation intent |
| running | Known failure with no further attempts possible / admission blocked | failed; reasons distinguished from prior consumption |
| running | Result cannot be confirmed | unknown; conservative consumption and necessary reservations / scope isolation |
| Any terminal state | Resubmission or late cancel | Replay; terminal state unchanged, no outbound call |

Facts are judged from reports and domain rules; cancellation intent only forbids new attempts. The race between a success report and cancellation must be resolved under the same atomic result-commit rules; if a terminal state is already committed, late evidence can only be verified, not directly overwrite it. A state with no external side effects after cancellation may be cancelled; if the provider has already accepted the email, it remains succeeded; if the AI did not finish but truly stopped and consumption is clear, it may be cancelled.

The event table above must not have a row picked independently by each path. All terminal paths use the same resolution order, checked inside the result-commit transaction:

1. Terminal state already exists: stable replay; late material can only append evidence.
2. The current persisted fact confirms complete business success: succeeded, with cancellation intent and other blockers kept as diagnostics.
3. Any action that obtained a send permit has an unconfirmable outcome and needs to terminate: unknown, not masked by cancelled / quota rejection.
4. Cancellation intent persisted and all actions confirmed not executed / known-ended with no success: cancelled; competitive reasons such as no capacity, budget, or deadline remain in reasons.
5. No cancellation intent and a failure is required: a required affinity block takes precedence over ordinary admission reasons; after that, classify by DeadlineExceeded, AttemptsExhausted, ExecutionInterrupted, AdmissionBlocked, and domain failure categories, keeping other diagnostics. For the no-permit recovery reasons of ExecutionInterrupted see §4.
6. None of the above termination conditions holds and active execution has valid attribution: remain running.

When a send permit has been granted but reliable not-sent evidence is missing, that does not count as "confirmed not executed". Cancellation and result transactions are linearized by persistence order: a cancellation arriving after a failed terminal state only returns alreadyTerminal. Resolution precedence does not decide reservation release; consumption / external reservations are still handled separately per evidence.

### 7.2 Attempts and Core Commands

Attempt.phase=prepared/executing/terminal, independently storing externalOutcome and per-resource consumption/occupancyEvidence. External unknown does not mean all resources are unknown; business not executed does not mean the request rate was not consumed. Prepared can release everything when it proves Transport was never invoked; for executing, failed_not_executed is projected per dimension and cannot be uniformly zeroed.

| Command/transaction | Atomic effect |
|---|---|
| claim | Unique key, digest, and execution right; distinguishes newly acquired from existing runs; the operation holds a lease from this point on |
| tryPrepare | Validates current operation, cancellation, plan/gate, budget, resources; creates intent, all reservations, and the decision; rolls back entirely on failure |
| authorizeDispatch | The complete contract replacing the old markExecuting: re-checks current gate/guard/window/ownership, migrates reservations or rejects; writes executing, a single-use permit, and the budget |
| consumeDispatchPermit | Transport performs the final validation and CAS granted→consumed; binds runtime, generation, request digest; a second call does not return send authority |
| completeWithoutAttempt | When there is no active attempt, submits a stable failure/cancellation with the same result resolver, preserving consumption already recorded for existing attempts |
| appendOutput | Stores chunks idempotently by sequence number/hash; the first-visible marker is committed in the same transaction as the chunk; delivered to consumers only after commit |
| activateConfig/recordGuardEvidence | Activates configuration or dynamic guards after trusted-source and authorization/version validation; on failure the old state is retained |
| requestCancel | Records stop intent and forbids subsequent attempts; prepared can be safely terminated; executing waits for facts/unknown to converge |
| renewLease | Conditional update of the operation and its active attempt; cannot revive terminal states or invalidated ownership |
| settle | Usage/occupancy transfer, attempt report, hold, releasing releasable items, final result and references committed in one transaction |
| sweep | Races with renewal/settlement conditions; releases prepared, executing becomes unknown; retains necessary occupancy; the operation converges atomically |
| reconcile | Appends confirmation, necessary accounting/occupancy corrections, and evidence; no re-sending, no altering the original terminal state |
| recordObservation | Verifies the resource window, watermark, adjustment version, source, and freshness; saves or rejects, no network |
| adjust | Unique ID, authorized reason, and evidence; appends a correction, updates rollups, and invalidates affected observations |
| clearHold | Validates authorization and the basis for lifting; lifts only the corresponding liftable block; does not implicitly delete occupancy or increase balance |

Mutating commands validate expectedState, owner/generation, and resource versions; special administrative/sweep paths use explicit system authority. An operation has at most one active attempt at a time. All transactions follow a uniform lock order (table category + stable ID); modules must not define their own resource lock orders; T1 fixes the global order along with concurrency samples.

SQLite can use BEGIN IMMEDIATE; PostgreSQL uses row locks and unique constraints; bounded retries happen only inside the database transaction, never re-sending over the network. Metering snapshots, live admission, and identity verification must not reside in mutually uncoordinated databases; otherwise the guarantees require separate evaluation.

### 7.3 Permits and pause windows

A DispatchPermit contains attempt/generation, binding/credentialRevision, plan and admission versions, requestDigest, validity period, and resource preparation references. Before sending, the Transport validates current state, lease, cancellation, and gates, marks single use within this process, and consumeDispatchPermit performs the final validation + CAS consumption in the same transaction; the extra write amplification is included in performance verification. After authorizeDispatch commits, no long operations such as probes or re-tokenization are inserted; credential resolution/request encoding are completed in advance with versions re-checked.

Pausing before validation across leases: after recovery it must be rejected, with zero outbound calls. Pausing after validation but before the actual socket write: without provider fencing, a late send cannot be absolutely prevented. A permit cannot be taken to another process for re-sending; after a sweep, the same ID does not take over sending; uncertain resources remain isolated. Do not advertise "checking the database again" as external exactly-once.

The corresponding uncertainty is released only when reliable termination evidence is received; if the business requires eliminating the above external races, admission needs strong idempotency / queryable termination / independent egress conditions. The first version does not build a proxy in advance for hypothetical requirements.

### 7.4 Crash table

| Failure point | Convergence |
|---|---|
| No attempt after claim | Expiry resolved per §7.1; with no cancellation and no higher-priority facts, it becomes ExecutionInterrupted/owner_lease_expired_before_dispatch, making clear that send permission was never obtained; the original ID is stable |
| prepared not authorized | Released; cancellation/expiry reaches a terminal state per known facts; the old owner cannot authorize |
| executing before/during outbound call | unknown if non-sending cannot be proven; counting upper bound, necessary occupancy/cross-cycle isolation |
| Result obtained but not settled | Retry via the ledger without re-sending; recoverable once a reliable report is persisted, otherwise unknown |
| After settlement, before returning | Replay the saved result by the same ID |
| Original result already terminal, report arrives late | Append evidence and go through verification; do not overwrite the settlement |
| Current storage unavailable / restored from an old backup | Stop new outbound calls; determine persistence and historical gaps; do not default to "never executed" |

Candidate lease=30s, heartbeat=10s, sweep=5s; frozen by D06, and external execution completion cannot be inferred from them. The operation and its active attempt maintain the lease together; shared deployments use database time or a verified skew policy, with Clock injectable. Sweeping may commit in batches, but each item is idempotent and has a time budget.

## 8. Outbound calls, streaming, and reallocation

The ProviderAdapter encodes/parses one request and sends it over the network through the controlled Transport; it does not hide retries, automatically follow redirects, or switch bindings. If the provider's business itself requires multiple requests, split it into explicitly metered actions or evaluate the composite contract separately; do not disguise it as a single outbound call.

The ExecutionReport carries the business outcome, raw error, executedBinding/account, providerRequestId, per-dimension consumption, and termination evidence; reallocation is decided jointly by the domain whitelist and core guards. Any prohibiting condition among unknown, cancellation intent, deadline, budget, and visible output outweighs the adapter's reallocationAllowed.

Provider reads and UI event delivery are separated: background settlement does not depend on consumers; after the first segment of visible output, bindings are not switched automatically; before that, only explicit and permitted failures may be retried. Consumption of a failed attempt is also recorded. Buffered bytes, consumer idle time, cancellation waits, and overall execution time limits all have upper bounds fixed in D04/D06; local time limits are not treated as proof of external stop.

Cancellation/disconnection/no consumer must converge the local stream and result, but may leave unknown and external occupancy behind. When storage fails, state whether the body was saved; querying by the original ID returns metadataOnly or a usable reference, never a fabricated full replay. An unsettled outbound call must not be masked by a zero-consumption DeadlineExceeded.

## 9. Onboarding, email guards, and operational disposition

First onboarding happens in the Web: select provider → securely write credential references → confirm account/binding → configure resources and offers → create pools/rank them → verify activation → minimal call → query results and accounting. Presets include official basis, verification time, plans, windows, metering mappings, BoundEvidence, billing boundaries, failure/cancellation/external termination capabilities, and maintenance responsibility. Complete real fixtures are delivered in T1/T2; unverified plan numbers are not copied.

Email condition items: sending domain/address authorization, unified recipient suppression sources, status refresh and guard timing points, and the distinction between submission and delivery receipts. The host maintains cross-provider suppression; DomainGuard rejects suppressed targets and is not bypassed by switching bindings. Block when required guard data is lost; the provider still bears final identity and delivery verification.

| Block | Scope and lifting |
|---|---|
| rate_limited / unavailable | Trusted scope and until; on expiry, other admission conditions are still checked |
| quota_exhausted | Resource window; a known reset or updated evidence within the same scope / a substantiated disposition |
| authentication_failure | binding/credentialRevision; successful verification of new credentials |
| billing_failure | AccountingAccount; confirmed resolution of the billing issue |
| policy_overrun | The involved resources/accounts; actual consumption determined and policy corrected |
| uncertain_execution/occupancy | Termination/ownership evidence or a trusted enforced boundary; does not disappear with a lease or an ordinary reset |
| domain_denied | Business scope and guard version; changes in host authorization/suppression conditions |

clearHold does not add capacity, delete unknown occupancy, or re-send; adjust does not change the operation's terminal state; reconcile confirms and corrects according to evidence. Operation identity, reason, source ID, and permission audits are complete. Authentication, accounting, and business permissions are separated; logs contain no secrets or bodies.

Retention: deduplicated identities and unknown-evidence are not auto-deleted in the first version; the independent policy for bodies is fixed by D05. DecisionRecords may be retained as desensitized digests; no secrets are stored. Backups cover operations, resources, binding versions, and unlifted occupancy; after a recovery gap, sending is closed first. inspect can show the decision at the time, current availability, and "operation is terminal but external occupancy pending confirmation".

## 10. Implementation and acceptance

Responsibility owners may be combined in one person; actual assignees are to be determined. Task cards include goals, prerequisites, actual owner, deliverables, evidence, and out-of-scope items. Integration continuously maintains the complete path.

| Task | Prerequisites/owner | Exit conditions |
|---|---|---|
| T0 scope and admission | None; project/adapters | First business evidence, accounts and bindings, deployment; D01–D03/D07 closed, D04 load and metrics frozen |
| T1 contracts and samples | Technical preparation may run in parallel with T0; integration/ledger | T0 exited; implement the interfaces from the detailed design, full-entity schemas, migrations, digests, reports, ranking, and result parsing; real upper bounds/trace/collection samples; D05/D06 specs frozen, D04 streaming budget frozen and closed |
| T2 single-binding real slice | T1; integration leads | Re-check applicability of D03 evidence before outbound calls; minimal Web config → Go API → real database → pool dispatch → controlled Transport → real outbound call → settlement → UI query |
| T3 correctness | T1, interleaved with T2; ledger/verification | Applicable core mechanisms of A01–A08, A13–A18, A20, A22–A29; built-in rules I01–I10, concurrency, configuration, cancellation, evidence, and recovery; D06 evidence verified and closed |
| T4 two-account dispatch | T2/T3; dispatch/adapters | A09/A19/A27/A28; the UI's manual ordering is consistent with server selection; heterogeneous/multi-pool sharing and preparation races are explainable; review interface extraction only when an ordering variant genuinely exists; the default function does not block delivery |
| T5 onboarding and operations | May start at T2, delivery depends on T4; onboarding/operations | A10–A12/A26/A29; 7-page Web contract and screenshots on both ends; A21 when email is included; verification/occupancy and result retrieval available; D05 operational evidence verified and closed |
| T6 performance and delivery | T3–T5; verification/integration | Frozen load, durability, a real two-account closed loop, and one failure/verification path |
| T7 extend on demand | T6; project/adapters | Second domain/new resources extended along the same boundaries; A23's applicable contracts over I01–I10 pass; no forcing of ordering variants that do not exist |

Per the detailed technical design, T1.1–T1.3 engineering/types/rules and reference schemas may proceed in parallel with T0 evidence gathering; T1's final exit still requires T0's real finalized conclusions and T1.4 specs/samples. Extension interfaces are validated with already-selected real differences; do not write future implementations for every interface. T2 is only a non-production slice; do not claim go-live readiness before T3–T6 pass.

This table is an architecture-phase constraint. Executable work items, task status, decisions, evidence, and phase exits are maintained in the [plans entry point](plans/README.md) and its linked files; implementation must not bypass this document's invariants on the grounds of plan simplification.

Requirement tracing: R01/R02→compilation/presets→A10/A17/A18; R03→claim/results→A01/A02; R04/R08/R09→admission/ranking→A09/A16/A24; R05/R07→resources/transactions→A03/A06/A07/A08/A22; R06→Transport→A20; R10→leases and recovery→A04/A05/A06; R11/R16→streaming/retention→A02/A11/A15; R12/R13→trace/disposition/collection→A12/A19/A25; R14→permissions→A13; R15→plans and live gates→A07/A16; R17→persistence/backup→A14; R18→extension contracts→A23; R19→consumption/occupancy→A22; R20→live authorization→A16/A20/A21; R25→Hooks lifecycle extension→A30.

## 11. Extension paths and performance evidence

2026-10-07 core-first user ruling authorizes the bounded backend increment in [CORE-EXTENSIONS](implementation/CORE-EXTENSIONS.md): validated startup provider assembly, exact reusable metering, and complete policy resource coverage. It adds no dynamic plugins or infrastructure dependencies and changes no persistent/wire schema. Existing real-provider and release gates remain in force; this preparation need not wait for T7.

| New requirement | What should change | What should stay stable |
|---|---|---|
| New provider | Presets, ExecutionBinding, ProviderAdapter, admission evidence | Identity, send permits, settlement, recovery |
| Multiple credentials/endpoints | ExecutionBinding and resource references, credential versions | AccountingAccount and the shared balance |
| New windows/concurrent resources | ResourceSemantics and shared contract samples | Router main flow, transaction commits, and deduplication |
| New business domain | DomainPolicy/Guard, business requests and results | Generic state, ledger, Transport |
| New ordering | By user pool member priority; explicit design when a new policy is needed | Admission hard gates, explanation records, and send authority |
| New deployment | LedgerStore/SecretResolver | All business and consistency contract tests |

The first version does not need to implement a dynamic plugin platform. A23 checks that extensions can only propose plans and cannot bypass atomic commits, unknown-result restrictions, single sends, or permissions; when new semantics exceed the guarantees, explicitly re-evaluate the architecture.

Performance targets reference only PRD N04. Freeze hardware, storage, instances, sync/durability configuration, data volume, and logging; measure ANT overhead with the controlled ProviderAdapter/Transport, then look at overall experience with real providers. Cover resource hotspots, preparation failures, permit re-checks, cancellation, renewal/sweeping, window migration, decision writes, checkpoints, backpressure, and history growth.

Batch quote, indexed rollups, short transactions, stable lock order, bounded buffers, and sweep budgets are design constraints, not proof of performance pass. Report p95/p99, write transactions and lock waits, rejection rates, memory, storage growth, and reservation utilization; average transaction rates cannot be extrapolated into tail latency.

## 12. Basis and unverified items

- [S1 SQLite: Write-Ahead Logging](https://sqlite.org/wal.html): same host, single writer, synchronization, and checkpoints; verified 2026-10-02.
- [S2 PostgreSQL: Explicit Locking](https://www.postgresql.org/docs/current/explicit-locking.html): row locks, transactions, and deadlocks; verified 2026-10-02.
- This round completed the technical design and appendix verification; running code, driver integration, providers, outbound call failures, admission, performance, and extension acceptance remain to be verified. D07 technical specs are recorded; real business/deployment/provider facts such as D01/D02/D03 still require evidence gathering.

### 12.1 Term definitions and T1 freeze entry points

| Name | Sole definition location/artifact |
|---|---|
| All DTOs/internal types/ports in contracts.go | The detailed design appendix is the entry point for complete fields/methods; T1.2 implements exports, codecs, and runtime validation; no additional same-named types are created beyond this table |
| Router/Ledger/Transport/ConfigCompiler/Query/Admin | §2.1 responsibilities; §4 public entry points; §7.2 persistent commands, with T1 providing concrete types and permissions |
| ProviderAdapter/DomainPolicy/DomainGuard/ResourceSemantics | §2.2 extension behavior; §5 evidence and rules; T1 inputs/outputs for the first batch of real implementations |
| ResourceChangePlan, I01–I10 | §2.3 plan structure, validation responsibilities, and common use cases; T1 union type of legal changes |
| AccountingAccount/ExecutionBinding/Resource | §3.1 identity; §3.3 records; T1 schemas and shared resource fixtures |
| Operation/Attempt/ReservationItem/ResourceState/UsageEvent/Adjustment/Observation | §3.3 full entity tables; T1 full-entity schemas, uniqueness, versions, states, and event/rollup relationships, not just the excerpted fields frozen in this table |
| semanticFingerprint | §3.3 business semantics digest; T1 normalization fields, excluded fields, hash algorithm, and same/different semantics samples |
| ExecutionReport | §8 domain results, per-dimension consumption, and termination/transmission evidence; T1 complete input/output types and missing/conflicting report semantics |
| rankCandidates | §6 internal pure ranking is the default implementation; the core may plug in a CandidateOrderer at declared lifecycle points, and the ranking result must still preserve the admission candidate set |
| ExecutionPlan/LiveAdmission/BoundEvidence | §3.2 fixed interpretation/live gates; §5.3 per-dimension upper bounds; T1 version and validity fields |
| SubmitReply/OperationResult/OperationView/CancellationAck/AuthorizedView/OutputPage/ReadModels | §4–4.1 and the contracts appendix; T1 implementation types, codecs, missing semantics, and serialization samples |
| retrySafety | §4 three-valued; PRD §6.1; T1 is a three-value union, no two-value alias retained |
| OperationResult.code/reasons | §4 result classification and recovery reasons, §7.1 priorities; T1 groupings and serialization samples; T3 verifies claim/prepared/executing recovery differences |
| DispatchPermit/DecisionRecord/Hold/Encumbrance/Reconciliation | §3.3, §6–9; T1 schemas, uniqueness, evidence, and state constraints |
| refreshObservation/CapacityProbeRequest/recordObservation/RefreshResult | §5.4 collection, network, and ingestion responsibilities; T1 permissions, resource consumption, and result fields |
| Clock/LedgerStore/SecretResolver | §2.2/§7.4: injected time, storage with same-transaction semantics, credential version resolution; concrete interfaces fixed after D02/D07 |

Unfrozen items must correspond to the T1 artifacts and pending records above; implementers must not invent new guarantees or hidden services on their own. This register is the entry point for documentation completeness and does not indicate that running functionality is implemented or performance is verified.

### 12.2 Scope of this round and new tracing

R21→config drafts/Catalog/Web→A26; R22→ResourceOffer/ResourcePool/shared references→A27; R23→server priority/version transactions/historical selection→A28; R24→Go server + standalone Web/HTTP + SSE→A29. These are first-version necessities and are not deferred to T7. Resource pools, ConfigDraft, OperationEvent, Go Context, and wire DTO definitions are covered respectively in technical designs 07/08/09 and contracts.go. The original text of user rulings is registered in README; the old V0.6 no longer participates in current execution.

## 13. Configure resources before consumption (2026-10-07 ruling)

The corrective business model, page contracts and additive SQLite v4 persistence boundary that were implemented are specified in [SUPPLY-INVENTORY](implementation/SUPPLY-INVENTORY.md), with [local verification evidence](evidence/WORK/SUPPLY-INVENTORY/README.md). Inventory is separate from executable ConfigDocument v1. Admission into execution remains subsequent work; no user acceptance is claimed. **Retired 2026-10-08** (ENG-03, ruling R7): the supply inventory API, code and table were removed; schema v5 drops `supply_inventory` and its rows on the next server start. The supply direction is now [design 11](design/11-SUPPLY-ENGINE.md). The following older editor description is historical and rejected.

The delivered editor described below was rejected on 2026-10-07: “what a shit!
do you know what should you build??” Its technical implementation is historical,
not accepted business design. Provider accounts must remain distinct from their
subscriptions and other resource packages. Presets must translate plan/package
rules into normalized resources and metering definitions; ordinary users must
not construct the ledger graph by typing internal fields. The historical corrective scope in RESOURCE-CONFIGURATION is superseded by
the supply inventory above, itself retired. Existing execution contracts and
acceptance gates remain applicable.

The user directs resource configuration to precede consumption. The existing
ConfigDocument and draft CAS commands remain the authoritative write boundary;
no storage schema changes are needed. The Resources page can create a draft,
record subscription accounts including future provider IDs, and add/edit/remove
unreferenced declared resources. Capacity is an exact decimal integer string;
windows, precision, commercial role, metering and expiry are recorded without
inventing verified scope facts, bound evidence or available balances. Incomplete
or unsupported definitions persist with field issues and cannot validate or
activate. Editing a declaration removes any prior verification references.

`-enable-resource-config` selects a configuration-only composition with catalog
schemas, ConfigService and read projections, but no provider adapter, policy,
transport, secret cipher, execution host or supervisor. A master key is unnecessary.
The separate localtest execution composition retains its explicit opt-in and key
requirements. Consumption and additional live-provider verification remain next.

Business scope and acceptance: [RESOURCE-CONFIGURATION](implementation/RESOURCE-CONFIGURATION.md).
