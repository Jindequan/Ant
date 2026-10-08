# Files, Methods, and Execution Work Items

Version V0.7 / Plan P1.3. For leaf tasks and dispatch see [TASK-TREE](../plans/TASK-TREE.md); this document keeps the mapping from stages to technical artifacts. Executors implement the existing Go/SQL/API/pool/page designs and no longer invent a new core from goals.

## 1. First Increment

T1.1 establishes server/go.mod, cmd/ant-server, and an independent web, splitting the contracts.go types, with check/build runnable; no provider requests are sent. T1.2 implements pure digests, result parsing, resource rules, and pool filtering/priority, plus the DTO codec; T1.3 implements modernc real-driver migration and StoreTx/writer. T0 real scenarios and admission can be collected in parallel; missing credentials do not block these preparations; real outbound calls still require D03.

## 2. Each Item Mapped to Source and the First Batch of Verification

| Work item | Files/methods | First batch of verification |
|---|---|---|
| T0.1–T0.4 | docs/implementation/BASELINE, PROVIDER-EVIDENCE | Real user requests, two providers/upper bounds/termination evidence, deployment and frozen load |
| T1.1 | server/go.mod, cmd/ant-server/main, web/package/tsconfig/vite, Makefile | Go/Web builds, dependency boundaries, empty project must not fake success |
| T1.2 | domain/types/fingerprint/result_resolver, resources/*, pools/eligibility/priority, api/dto, web/api/types | Digest includes pool, result races, quantity string, manual ordering, and heterogeneous hard filtering |
| T1.3 | migrations/001, store/sqlite/store/writer/statements/migrate | 34 tables on the real driver, unique/FK/transaction rollback, queue/timeout |
| T1.2 / CFG-01–07 | Generic config compilation, controlled verification catalogue, local/test fixtures | Trusted identity, typed evidence, complete bundle, consumable compiled output |
| T1.4 / IN-05 | Real provider facts/preset mapping, real fixtures, runtime/settings | Actual requests enforce parameters, expired evidence rejected, runtime parameters frozen |
| T2.1 | application/config_service, api/config, security/auth, Web minimal configuration form | SaveDraft/Validate/Activate; old configuration retained, GET reads back real values |
| T2.2 | ledger/commands/{claim,prepare,authorize,settle,cancel,sweep}, validate_change | Atomic reservation, unknown occupancy, single active, duplicate settlement |
| T2.3 | transport/permit/http_transport, runtime/host/stream_hub, first adapter, api/operations/events | consumeCAS, requestDigest, Go old-connection/redirect wire counting, save-before-deliver |
| T2.4 | Web minimal pool/execution/result pages + one real offer | Real UI→real API→real database→real outbound calls→replay/disconnection queries |
| T3.1 | domain/resources, ledger changes and shared references | 100 requests / 50 capacity, slot-1 unknown, real overrun, cross-pool sharing |
| T3.2 | tests/fault, host/context, owner/lease guards | Multi-process/cancel races, browser exit, server crash, old permit rejection |
| T3.3 | Config activation, pool version, security, observation, API events | 409 keeps draft, admission tightening, privilege escalation, SSE lost-notification backfill, storage failure |
| T3.4 | Correctness evidence index and final regression | A01–A08/A13–A18/A20/A22–A29 applicable mechanisms, D06 closed |
| T4.1 | Second adapter/preset/offer and resource fixtures | Two real accounts + shared dual bindings, without doubling quota |
| T4.2 | pools/priority, application/router, api/pools, Web pools | UI reorder → server routes by new version, same values stable, heterogeneous grouping within pool |
| T4.3 | Router safe continue / Ledger settlement | Known safe failure switches offer, unknown/first output does not switch, two-provider real evidence |
| T5.1 | Web four-navigation 7-category pages, components/tokens/locales, client/events | Page contracts, four states, keyboard ordering / 390, schema forms / error retention |
| T5.2 | QueryAdmin, verification/observation/hold management and operation details | Original decision display, fact vs cancel intent distinction, admin cannot resend |
| T5.3 | security/secrets, output saving, backup/restore_guard | Secrets not echoed, missing bodies honest, backup gaps seal sending |
| T5.4 | e2e and dual-end evidence, independent first use, D05 | Real full journey / reverse flow, 7-category pages screenshot page by page, N02 time recording |
| T6.1 | Bench scripts, profile environment and data | D04 frozen / raw sampling, provider time not counted as engine overhead |
| T6.2 | pprof/benchmem, queues, writer/connection/stream benchmarks | race, goroutine leaks, throughput/tail latency, FULL durability / lock hotspots |
| T6.3 | Go binary, web/dist, new-environment installation guide | No Node backend dependency, admin bootstrap / actual configuration → result / restart |
| T6.4 | DELIVERY + matrix + STATUS | R01–R25/N01–N07/A01–A30/I01–I10 all applicable evidence |
| T7.1/T7.2 | Dedicated design and Adapter for new domains/new provider protocols/duplex sessions | Runtime extensions only when needed; do not defer first-version configuration pools and priority |

## 3. First Batch of Explicit Assertions

Same poolID with consistent content digest; different poolIDs conflict; the six result race classes go through the unified resolver; pools only filter compatible domains/profiles, smaller priority first; two pools reference slot-1, and after unknown both reject; any member illegal in a total-order modification transaction rolls back everything; poolVersion changing between prepare and consume must not allow a new send; Go HTTP actual wire count is at most once; SSE duplicate sequences are deduplicated, and subscription disconnection does not start a new request.

Every test case must check both the result and the accounting/permit/actual network counts; asserting only that the interface returned 200 is not allowed. Attachment compilation / Python schema checks are design evidence only.
