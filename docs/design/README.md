# ANT Detailed Technical Design

Version: V0.7 · Date: 2026-10-02 · Status: Go server + Web console design; runtime implementation not started.

This directory expands [DESIGN](../DESIGN.md) and follows the PRD and I01–I10. V0.6's Node core, worker RPC, and TypeScript backend contracts have been archived and are no longer implemented. Only the files listed in this entry point are currently valid.

| Order | File | Technical problem solved |
|---|---|---|
| 1 | [Architecture and Modules](01-ARCHITECTURE.md) | Go server, Web, source code, dependencies, concurrency and performance mechanisms |
| 2 | [Storage and Transactions](02-STORAGE.md) | Entities, conditional updates, exact numerics, locks, idempotency, and migrations |
| 3 | [Execution Chain](03-EXECUTION.md) | Submission, server dispatch, permits, outbound calls, settlement, cancellation, and recovery |
| 4 | [Resource Algorithms](04-RESOURCE-ALGORITHMS.md) | Independent units, metering windows, provider entitlements, concurrency, and observation |
| 5 | [Host and Adapters](05-RUNTIME-AND-ADAPTERS.md) | Go lifecycle, controlled HTTP, persistent output, secrets, and operations |
| 6 | [Implementation Map](06-IMPLEMENTATION-MAP.md) | Work items → Go/Web files, methods, and the first batch of tests |
| 7 | [Resource Pools and Configuration](07-POOLS-AND-CONFIG.md) | Multi-type resources pooled, manual priority, configuration activation, and races |
| 8 | [API and Events](08-API-AND-EVENTS.md) | HTTP, SSE, identity, version conflicts, reconnection, and duplex boundaries |
| 9 | [Page Contracts](09-WEB-CONSOLE.md) | Configuring providers/resources/pools, operation tracking, four states, and acceptance |
| 10 | [Runtime Hooks](10-HOOKS.md) | Extensible lifecycle checkpoints, core-validated Transformers, post-submission Observers, and failure semantics |
| Attachment | [contracts.go](contracts.go) | Go DTOs, resource pools, core ports; types only, no runtime implementation |
| Attachment | [001-initial.sql](sql/001-initial.sql) | SQLite empty-database reference migration; not deployed |
| Attachment | [Validation Record](VALIDATION.md) | The attachment checks actually run this round and their limitations |

The backend uses the Go 1.27 series with the standard library net/http/database/sql; the SQLite single-host reference Store chooses modernc.org/sqlite; the frontend is React + TypeScript strict + Vite. Dependency patch versions are locked at T1. The Web build can be served as same-origin static hosting by Go; frontend and backend remain separate projects with separate tests. Production multi-host requires a shared transaction Store, not replicated local balances.

The first version must deliver the resource configuration UI, resource pools, and server-side dispatch; outbound call evidence for two providers on one real domain is the first acceptance slice, without limiting the configuration model to a single domain. Unimplemented/unverified adapters may save drafts but cannot be activated for execution. Real accounts, upper bounds, plans, and initial state are still being evidenced in T0; the technical engineering and configuration closed loop can start first.

## Product design slices

- [15 — One view, kept](15-ONE-VIEW-KEPT.md): one shared quota view and a fast, persistent first paint.
- [16 — The router owns the choice](16-ROUTER-OWNS-THE-CHOICE.md): model discovery stays in the page; route decisions come from the server preview.
- [17 — User-chosen supply route order](17-SUPPLY-ROUTE-ORDER.md): persisted per-model ordering over eligible candidates, protected by revision conflicts and independent of spend authorization.
- [Route-order acceptance — 2026-10-10](../evidence/WORK/ROUTE-ORDER/README.md): atomic candidate validation, default reset, race/API coverage, and browser interaction evidence.
- [Router gaps — 2026-10-10](../plans/ROUTER-GAPS-2026-10-10.md): verified supply eligibility, evidence versions, and persisted ordering; consumption remains a separate future step.
