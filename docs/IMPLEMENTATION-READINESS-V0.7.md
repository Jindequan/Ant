# V0.7 Implementation Readiness Assessment

Date: 2026-10-02. Scope: the current PRD/DESIGN V0.7, the detailed design, the Go/SQL attachments, and the P1.2 plan. This file is an assessment record; it does not replace the effective design and does not imply the gaps below have been fixed.

## Conclusion and Start Scope

**T1 engineering and core rule implementation may begin; V0.7 cannot yet be treated as a complete execution specification with all contracts frozen.** The current architectural direction is stable enough that there is no need to redesign the frontend/backend form or introduce microservices. The executor should first treat K1–K3/K6 below as concrete closure items before the T1 contract freeze and T2 outbound calls; K4 is resolved when the interface/UI freezes, and K5's task ownership is adjusted before advancing to T3. T0 real inputs are collected in parallel; undecided business/provider matters do not block engineering, but they do block real outbound calls.

Can be done immediately: Go/Web engineering, dependency boundaries, amount codecs, digest/result parsing, pure resource rules, reference migrations and Go driver integration, and controlled test fixtures. For the configuration main path, the data contracts and a real storage closed loop can come first; actual provider parameters, entitlements, initial state, and strict spending guarantees cannot be replaced with synthetic values.

Cannot be promised immediately: implementing a full dynamic configuration UI from the current files without completing the contracts; implementing automatic replanning/switching before K3 is closed; admitting providers without real evidence; or claiming performance, stability, or onboarding targets are met without real operation.

## Parts Already Sufficient to Guide Implementation

1. Frontend, backend, and server responsibilities are clear: the Web configures/observes, and the Go server handles authentication/dispatch/outbound calls/accounting/recovery; a modular monolith fits the current scope.
2. The separation of accounts/bindings/resources from offers/pools is correct: the same physical resource is still shared across pools, heterogeneous units are independent, and user priority is separate from providerOrder.
3. The claim→prepare→authorize→consume→send→settle main chain has transaction and state constraints; the unknown terminal state is separated from external reservations, and cancellation does not erase facts.
4. There are concrete mechanisms for bounded execution, short transactions, connection pooling, persistent output and events, and backpressure/recovery, along with corresponding testing responsibilities.
5. Go types, reference SQL, R/N/A/I, and task mappings already exist as artifacts, clearly exceeding plans that have only goals and module names.

That the above holds does not mean every module seam is ready to be coded by direct copying.

## Concrete Gaps That Must Be Closed

### K1 · P1: The configuration main path still lacks complete, verifiable data contracts

Location: [ConfigDraft.Definition](design/contracts.go:279), [RuntimeConfig overview](design/05-RUNTIME-AND-ADAPTERS.md:7), [T1.2](plans/01-CONTRACTS.md:14).

ConfigDraft.definition is still a RawMessage; there is no RuntimeConfig root schema, no concrete AccountDefinition or ProviderEntitlementGroup structures, and no CreateDraft/UpdateDraft requests or complete configuration read projection. ProviderDescriptor stores a schema container, without the actually usable first batch of schemas and complete fixtures. Resource internal fields exist, but they cannot directly serve as frontend wire structures. AI/email Payloads are also carried by RawMessage, and the first batch of request schemas still needs to be specified.

If the UI, ConfigService, and Compiler are developed directly, the three implementers will each decide field names, nullability semantics, reference relationships, and which unsupported descriptions may be saved/activated, producing rework.

Adjustment: T1.2 first freezes a versioned ConfigDocument: accounts, bindings, resources, entitlementGroups, offers, pools, grants, and evidence references; for each item define the wire DTO/schema, required fields, IDs, units, lifecycle, and permissions. Unsupported descriptions are explicitly placed in the non-executing part of the draft; it must not simultaneously claim saving is allowed and reject the whole draft over unknown fields. Add complete key-free fixtures so the UI/Compiler/API use the same set of validation material. Provider type details use controlled extension objects; core reference structures must not all become free-form JSON.

Acceptance: the fixture passes schema validation, Go codec round-trip, and read-back via the saved GET; a complete shared resource bundle compiles; erroneous/unknown executable fields are rejected, unsupported drafts have an explicit status; changes to another namespace or shared-owner resources without permission are rejected.

### K2 · P1: The creation/read seam of the Go dispatch capability is not closed

Location: [DispatchPermit/PreparedHTTPRequest](design/contracts.go:544), [Ledger and Transport ports](design/contracts.go:600), [final consumption algorithm](design/03-EXECUTION.md:108).

The fields of both handles are entirely unexported, and they are defined in the contracts package; after the design splits into ledger and transport packages, external packages cannot directly create instances containing those fields or read their identity. The comment "split into private packages in T1" gives direction but not yet the seam of who creates, who registers, and who reads/verifies. Single-file compilation only checks type declarations and cannot surface this cross-package assembly problem. The permit binding information listed in DESIGN is even more than the fields in the attachments; it must be clarified whether this information lives in the DB/private registry or in the handle itself.

Adjustment: assign the handle concrete types/registry to a definite server internal package; fix the ports and data flow for Ledger post-grant registration, Transport preparation/one-time marking, and Ledger final consumption. The handle may contain only an unpredictable token, with binding information kept in the registry/DB, but the three modules must be able to obtain the same identity through controlled methods. Do not open HTTP-based reconstruction of the dispatch capability, and do not use reflection/unsafe to break through private fields.

Acceptance: implement minimal assembly tests in at least three real Go packages — domain, ledger, transport — covering non-zero handle creation, reading the same attempt/digest, and rejection of forged/cross-runtime/reused handles; no actual provider is needed.

### K3 · P1: The attempt/permit/resource convergence after pre-dispatch rejection lacks an explicit transaction

Location: [authorize/consume](design/03-EXECUTION.md:102), [reorder taking effect](design/07-POOLS-AND-CONFIG.md:58), [completeWithoutAttempt](DESIGN.md:293), [Ledger port](design/contracts.go:600).

authorize already changes the attempt to executing and concurrent reservations to active. When a subsequent poolVersion change causes consume to reject, the documents require "after a reliable non-send, revoke the old reservation and replan," yet there is no explicit reject/revoke command or complete atomic effect. completeWithoutAttempt only allows no active attempt; the settle main flow is report-oriented and does not define who forms the local non-send proof, how it races with consume/sweep, or how the old attempt is closed.

If the executor only releases the summary without marking the old attempt terminal, the single-active-attempt unique index will block the next prepare; if all executing attempts are released as non-sends, real external reservations may be mistakenly released. The current design intent is conservative, and the gap does not mean a P0 incident has already occurred, but implementers must not be left to fill in this core logic themselves.

Adjustment: specify a Ledger-internal AbortBeforeDispatch transaction (or explicitly reuse settle's not_executed branch), and fix the following matrix:

| State/Evidence | Atomic Handling |
|---|---|
| prepared, never granted a permit | Close the attempt and release the corresponding reservation; cancellation/failure/replannable are resolved by a unified resolver |
| granted and a transactional CAS proves consume has not happened | granted→revoked, old attempt→terminal, release the amount with no external execution yet; the granted permit budget is retained |
| consumed, with a reliable non-send proof from the controlled Transport | Verify the proof and the corresponding permit/attempt, and settle per resource; call parameters alone must not be trusted |
| consumed, with no reliable non-send/closure evidence | Conservatively keep unknown and necessary reservations; automatic switching is forbidden |

The transaction updates the attempt, reservations, events, rollup, permit, receipt, and decision together, and races with consume/sweep through the same state guard. Cancelling a prepared item must also close the attempt, not just the Operation.

Acceptance: reorder after prepare, reorder after authorize, lost consume reply, crash after consume, and prepared cancellation each verify the actual network count, the resource set, and the next prepare; every rejection must explain why replanning is or is not allowed.

### K4 · P2: Numeric priority and "whole-group order" interface semantics are inconsistent

Location: [PoolMember/ReorderPoolInput](design/contracts.go:224), [reorder rules](design/07-POOLS-AND-CONFIG.md:54), [UI numeric input](design/09-WEB-CONSOLE.md:27).

The model allows equal priority with offerId breaking ties, and the UI allows numeric editing; yet the request passes only orderedOfferIds, and the server uniformly generates 10/20/30. An input priority=7 or two members with equal values will not be saved as-is by this interface.

Adjustment: choose one authoritative semantic. The recommendation is to keep the user's explicit manual priority: the whole-group request includes each offerId and its priority, validating that each member appears exactly once, the numeric ranges, and the version; drag operations generate an explicit set of values, with equal values still broken by offerId. If only relative order is saved instead, the requirements/UI must state clearly that the numbers are positions only and are normalized on save, dropping the "same priority" promise. Two write paths must not overwrite each other.

Acceptance: numeric editing of 7/7 can be read back via GET with stable ordering, or the normalization behavior is shown explicitly; conflicting edits from two tabs roll back entirely. This item is resolved at the T1.2 protocol freeze, not by changing the backend after the page is done.

### K5 · P1: There is a gap in implementation ownership and acceptance ordering for the priority mechanism

Location: [T3 exit](plans/03-CORRECTNESS.md:43), [T4.2](plans/04-MULTI-ACCOUNT.md:14), [T2 core tasks](plans/02-VERTICAL-SLICE.md:17).

T3 already requires whole-group order 409, poolVersion races, and the A28 core mechanism to pass; T4 depends on T3 exit, yet T4.2 is also written as the phase implementing choice/order and the UI. T1 has the pure ordering/storage mechanism, but T2 does not explicitly include the ReorderPool application service/API and post-rejection convergence in its deliverables. Following the task cards as written, it is easy to discover at T3 that the capability to be tested is still waiting on T4 implementation. A static ID dependency graph without cycles does not mean capability dependencies are closed.

Adjustment: T1 owns the fields/pure rules; T2.1/T2.2 explicitly implement the configuration service, the pool total-order submission API, basic selection, and the K3 transaction; T3 verifies these core races; T4 only connects the second real provider, refines the ordering UI, and verifies actual selection/safe reallocation. T5 completes the full 7 pages. Amend the corresponding task cards and the STATUS work item delivery notes so the same core capability does not have two "first implementation phases".

Acceptance: every real method/API invoked by each T3 core use case has a completed T1/T2 artifact; T4/T5 only add real differences and complete journeys and do not retroactively satisfy the T3 start conditions.

### K6 · P2: The API table and the Go DTOs still need one real field alignment

Location: [accepted example](design/08-API-AND-EVENTS.md:39), [SubmitReply](design/contracts.go:133), [API mapping](design/08-API-AND-EVENTS.md:15).

The accepted example has a state field, but SubmitReply does not; unified errors have an outer error/requestId, while the attachments have only ErrorDTO; several create/update/pagination inputs are undefined. Many status/code fields are still strings, and illegal combinations must be validated by running the codec. These can be finished in T1.2, but it cannot be claimed that a complete network contract able to directly generate all clients already exists.

Adjustment: freeze the actual network structure with versioned OpenAPI/wire DTOs/request-response fixtures; determine whether a poolId repeated in path and body rejects inconsistency, whether If-Match and the body version must match, and the pagination and empty-array semantics; bring all API samples into contract tests. Internal types without JSON tags, such as Adjustment/ExecutionReport, must not be output directly to the Web.

Acceptance: Go handler encode/decode, schemas, and Web types use the same fixture; illegal accepted/terminal combinations, amount overflow, duplicate identity, and version field conflicts are all rejected.

## External Inputs and Performance Boundary

D01 first real domain/request, D02 actual deployment, and D03 two-provider and resource evidence remain open; these are registered external inputs and are not something writing more documents automatically solves. Go1.27 and modernc dependency compatibility must be verified in the actual engineering; the local build used Go1.26.0. SSE suffices for the current text/status and HTTP cancellation; continuously duplex provider sessions need a separate session metering design and must not be quietly folded into a single HTTP attempt.

The performance mechanisms have concrete landing points, while the targets remain candidates/pending freeze; SQLite single-writer and FULL durability throughput need real measurement. First land bounded execution/connections/transactions/output batches, then use benchmarks to localize; there is currently no reason to jump straight to microservices, Kafka, or an authoritative in-memory balance cache.

## Evidence for This Round

- Re-read the current main documents, Go contracts, SQL, the detailed chain/pool/API/page documents, and the task cards, cross-checking by entity, port, state, and phase exit.
- `go test docs/design/contracts.go` exits 0 and explicitly reports `[no test files]`: this only proves type compilation; no core tests were run.
- `python3 docs/design/validate_schema.py`: SQLite3.54.0, 34 tables, 22 constraint checks passed; this cannot prove configuration, ordering, outbound calls, the UI, or all resource invariants.
- The repository still has no server/go.mod, web/package.json, OpenAPI, or run-test artifacts; attachment validation must not be presented as application implementation evidence.

## Recommended Boundaries for the Executor

Start T1.1 with T0 evidence collection in parallel; T1.2 delivers K1/K2/K4/K6 as explicit contracts without guessing fields on its own; close K3 and adjust K5 task ownership before T2 implementation. Once done, proceed through the original phases: real DB→real Go API→minimal real Web→controlled outbound calls→real providers. There is no need to continue endless comprehensive static review, but "start writing code" must not be used to skip these concrete seams.
