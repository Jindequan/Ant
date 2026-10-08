# T1 · Go/Web Engineering, Models, and Contracts

Baseline P1.2 / V0.7. T1.1–T1.3 may advance in parallel with T0 evidence gathering; T1.4 / the final exit still require T0. Primary owner: integration, with ledger / Web collaboration. For specific files and methods see [architecture](../design/01-ARCHITECTURE.md) / [mapping](../design/06-IMPLEMENTATION-MAP.md).

## Currently Converged Scope (2026-10-02)

2026-10-03 task mapping: among the items below, D1/D2 design → CFG-01, D3 → CFG-02, D4 → CFG-03, D2 implementation → CFG-04, D1 implementation → CFG-05, Web mirrors → CFG-06, merge acceptance → CFG-07. Tree task definitions and actual status are in [TASK-TREE](TASK-TREE.md); this card retains phase responsibilities and business requirements.

T1 is a phase, not a single bundle dispatched to the executor. Accepted engineering, quantities, window execution, root schema, Store engineering, and verification tooling evidence are retained; the phase being in verifying does not mean all of that work failed. The current round only addresses D1–D4 of the [latest review](../REVIEW-T1-REWORK3-CODEX.md). Do not restart from T1.1, and do not implement T2 business transactions ahead of time because of this card's old items.

Hand off one independently verifiable work package at a time. The design lead Codex is responsible for open semantics and algorithm constraints; the executor implements per the contracts; the acceptor independently inspects the final code; "freeze it yourself first, then implement" must not be treated as the default handling of open design.

| Work package | Inputs and determined outputs | Code boundary and exit |
|---|---|---|
| D3 evidence declaration (do first) | bound/hard_stop must have a canonical non-negative bound equal to the verified upper bound; initial_state must have a canonical non-negative remaining consistent with the initial value of this verification snapshot. This slice introduces no mechanism for choosing the best among historical observations; missing or conflicting values form an issue with Compiled=nil; there are separate positive cases for the legitimacy of zero values and conservative configuration | config evidence parsing and its tests; first the Go full Compile entry point, then Web. The original four counterexample rejections pass, the positive case where the verified value is also zero passes, and conservative capacity does not regress. Accepted separately once passed, without waiting for other packages |
| D4 scope metadata | The parent VerifiedScope provides real scope/provider identity; child facts inherit when omitted, and load fails on explicit contradiction; a shared alias outputs the shared scope rather than exposing the account ID | localtest directory and corresponding mirrors and tests; legal compiled artifacts have non-empty fields with correct values, and conflicting snapshots are rejected |
| D2 resource types and complete bundle | Trusted semantics constrain kind/commercialRole, and the profile's required roles must participate in coverage judgment. The concrete trusted-field/semantic registration mapping is implemented by Codex in the original design first, then implemented — Go and Web are not left to guess separately | domain/provider/config shared models owned by one integrator; counter masquerading as occupancy and limit masquerading as entitlement are both rejected; positive cases assert the compiled artifact's types and complete bundle |
| D1 window identity | Finite date sampling must not masquerade as rule equivalence; canonical rules are defined per kind. The concrete fixed phase and timezone alias policy is implemented by Codex in the original design and runnable rule cases first, then implemented | Only identity normalization and its callers; the accepted WindowAt is retained. rolling self-inverse, Casablanca/Lagos inequality, and existing legal normalization controls all pass, then proceed to the full Compile entry point |

After Codex took over on 2026-10-03, the limited designs for D1/D2 were completed and accepted via CFG-01 rule cases; the current algorithms are in 04 resource algorithms §1.1 and 07 configuration §1.1. D3/D4 rules are unchanged; the current fix and acceptance status is in STATUS and docs/evidence/T1-CODEX/. Do not call "having a fix direction" the same as "the interface is frozen." For each package, upon completion check real inputs, error categories, and compiled artifacts before starting the mirror implementations that depend on it. Source ownership conflicts are coordinated by the integrator, not adjudicated line by line by the user.

This round reruns only the cases related to the changes; after all four packages complete, run the full gate once and a final tree review. New findings must be classified as missed implementations of existing rules, design gaps, or new requirements; correctness blockers are still handled, and non-blocking improvements are registered for later. Do not quietly expand this round's scope, and do not ignore real defects just to end the rework. None of this changes the T0/T1.4/T2 prerequisites, and it does not authorize real provider side effects.

## T1.1 Backend and Frontend Engineering and Dependencies

1. Establish server/go.mod / go.sum, cmd/ant-server, and the internal layering, locking applicable patches to the Go 1.27 baseline; establish the independent web's React / TS strict / Vite / npm lockfiles. No Node business backend.
2. Implement the composition root / ports and dependency checks per the architecture; external Supplier HTTP only in Transport, SQL only in Store; pure rules make no network calls. The Web does not reference Go internal types / secrets.
3. Establish Makefile entry points for check/build/test-contract/test-integration/test-fault/test-web/test-e2e/bench; unimplemented or empty tests exit non-zero. Web builds and the Go binary are produced separately, supporting same-origin static hosting.
4. Implement a minimal server startup / ready / shutdown skeleton and the Web empty-state route skeleton; do not fill in fake balances and do not treat empty pages as passing features.

Deliverable: buildable server and web, dependency locks, directory / check and lifecycle skeletons. Acceptance: Go vet / compilation and Web types / build pass; startup makes no unverified outbound calls; no uncontrolled goroutines. D07 environment-compatibility evidence is still closed by T0.2.

## T1.2 Models, Go Ports, Network DTOs, and Pure Algorithms

1. Split domain / API DTO / internal ports from [contracts.go](../design/contracts.go); write a quantity decimal-string strict codec with explicit optional semantics; permit and secret must not appear in OpenAPI.
2. Implement Account/Binding/Resource/Offer/Pool/Member/Draft/Event, with digests including poolId; Operation policy fixed on first use, namespace from trusted identity.
3. Implement Normalize/Fingerprint/ResolveResult and the pure rules for counter/window/occupancy/waterfall, retaining I01–I10; real overages are never truncated.
4. Implement pool member filtering and priority ASC / offerId ASC; cost / preferred does not override manual ordering; cross-type units are not summed; cross-pool shared identity does not add capacity.
5. Generate OpenAPI and Web types from the [API routes](../design/08-API-AND-EVENTS.md), covering request / reply / error / version-conflict and SSE event samples; generation is reproducible, and schema validation matches the Go codec.

Deliverable: actually compilable ports / DTOs, OpenAPI, Web types, rule cases, field documentation. Acceptance: identical / differing digests, six-outcome races, int64 boundaries / JSON strings, stable priority, heterogeneous filtering, cross-pool sharing, unknown / missing fields, and protocol-illegal states all have real unit tests.

## T1.3 Real DB, Full-Entity Migration, and Write Channel

1. Per the [34-table SQL](../design/sql/001-initial.sql) / [transaction design](../design/02-STORAGE.md), implement the chosen actual Store's migrations, prepared statements, and the StoreTx interface. Single host: modernc database/sql, one write connection / bounded writer, read-only connection pool; for actual multi-host use D02's shared-transaction Store — no dual implementation.
2. Freeze the StoreTx port required by the Ledger, conditional-update semantics, and the operation→pool/member→binding/account→resourceId/window lock ordering; this item verifies Store primitives. The complete transaction assembly and race verification of individual Ledger commands happen in T2.2 and are not re-implemented here; the network and waiting consumers stay out of transactions.
3. Deliver reference transaction primitives such as draft persistence and the whole-group priority ReplacePoolMembersCAS, verifying version, audit, and receipt committed in sync; prepare the schema and transaction ports for resource events / operation_events. The complete configuration activation service is wired and accepted in T2.1, and execution state and event business-atomic transactions in T2.2.
4. Verify empty-DB migration, repeated startup, pool namespace / member foreign keys, single active attempt, permits, event immutability, and full rollback on partial failure. Reference Python verification cannot substitute for real Go driver integration.

Deliverable: running migrations / Store / writer, entity lock ordering, and conditional updates. Acceptance: real-DB integration in Go, queue overload, context cancellation / lost results checked against receipts, and no partial saving of ordering. Existing production legacy databases must never have history cleared with empty-DB scripts.

## T1.4 Real Samples, Parameters, and Verification Entry Point

1. Map T0 real materials to two-provider presets / Catalog schema / full resource bundles per offer / pools, with real upper bounds and reported evidence explicit; additionally provide synthetic heterogeneous and cross-pool fixtures with their purposes annotated.
2. Freeze D04 stream / queue / quota, D05 secret / identity / retention / cleanup, and D06 Clock / lease / cancellation / unknown handling. HTTP text + SSE is the first version; un-admitted duplex sessions are rejected.
3. I01–I10 illegal changes, overage facts, poolVersion changes, whole-group ordering conflicts, one real HTTP wire exchange, and disconnect-retrieval cases enter the test entry point.
4. Prepare fixtures for the T2 config → minimal Web → Go API → real DB → one-offer outbound call path and the real test action scope.

Deliverable: real presets, synthetic fixtures, frozen parameters; D04 closed, D05/D06 frozen. Acceptance: check / test-contract / test-integration already pass in the running project; T2 needs no new protocol or resource model.

## Phase Exit

Exit conditions: T0 and T1.1–T1.4 have each satisfied their own exits (this sentence is a condition, not a statement of current completion). Deliver exactly one Store and the first real-domain execution adapter; the configuration framework and heterogeneous pool model must exist. Compilation of the Go/SQL design attachments does not substitute for the engineering tests above. Next item: [T2 real slice](02-VERTICAL-SLICE.md), while preparing [T3](03-CORRECTNESS.md).
