# T6 · Performance, Durability, and First-Version Delivery

Goal: prove correctness, usability, and performance under the frozen deployment and load, and deliver an installable first version. Prerequisite: T3–T5 exit. Primary owners: verification / integration; ledger and operations collaborate. Related: PRD N01–N07, DESIGN §11.

## T6.1 Performance Environment and Benchmarks

1. Freeze the Go toolchain / Web build, code / configuration, runtime, hardware, Store, sync / durability, indexes, logs, historical data volume, account / resource distribution, and request mix.
2. Establish load and metrics against the D04 frozen values. If the PRD candidate values are kept: 10 accounts, at most 8 resources per account, 1 million historical records; 20 operations/second sustained for 30 minutes, 50 concurrent long tasks; new-admission p95≤50ms, p99≤100ms; 2 hours of representative durability.
3. Separate normal admission, capacity rejections, hot-spot contention, and query / management load; record success rates, rejection reasons, and lock waits — total rejections cannot prove throughput.
4. Use controlled provider responses to measure core added latency and streaming behavior, then use the real provider to measure the overall journey; span definitions are fixed, never derived by guessing provider timing and subtracting.

Deliverables: bench scripts and a frozen environment / data / metrics index. Acceptance: the load is reproducible, raw samples can compute p95/p99; test nature and real calls are clearly labeled, and performance targets are never lowered after testing.

## T6.2 Capacity, Tail Latency, and Durability

1. Run normal load, resource hot spots, full-capacity rejections, preparation contention, extra writes from final permit consumption, cancellation / lease renewal / sweep and control batch reads, reset migration, decision writes / queries, output merge transactions, and checkpoints.
2. Verify backpressure on long streams / no consumer / disconnects, recording buffer peaks, memory, goroutines, and settlement; during the 2-hour run, check reservations, events / rollups, and the reservation set.
3. Distinguish legitimately retained unknown reservations from leaks; uncertainty must never be deleted for the sake of a zero-leak metric. Record storage growth, lock errors, failure rates, p95/p99, and business outcomes.
4. If targets are missed, locate the specific bottleneck and optimize only affected paths; do not weaken durability / authorization / evidence, and do not add unapproved balance caching. After changes, run the affected correctness and performance regressions.

Deliverables: raw data, pprof / benchmem / race / goroutine and queue reports, bottleneck fixes and regressions. Acceptance: all frozen targets are met with no unhandled lock errors / reservation leaks / accounting deviations; if targets are missed, the failure is retained and must not be reported as a pass.

## T6.3 Install Artifacts and the Real Full Journey

1. Generate the Go server binary and the web/dist artifacts; verify same-origin static hosting and standalone API deployment configurations; verify fresh-environment installation, migration, configuration validation, permissions, running, and shutdown / restart; commands in the documentation must actually be executed.
2. On the same final version, walk the two-account real success path and at least one failure / verification / recovery path, checking request IDs, accounting, and reservations.
3. Verify that packaging contains no test fakes, development keys, or unverified default configuration; real calls remain limited to the established test-action scope.
4. Check that D05/D06 still hold in the final run form; during installation, configure real resources / pools / priorities through the Web, without requiring users to understand internal types or hand-write resource semantics.

Deliverables: installation packages / build artifacts, usage guides, operations manuals, and final real-journey evidence. Acceptance: a new environment runs; original-ID replay / queries are stable; the chosen domain and the Store's first-version scope are complete; deployment to production is not required to prove installation and operation.

## T6.4 Acceptance Summary and Delivery

1. Against the ACCEPTANCE-MATRIX, compile the applicable evidence for R01–R25, N01–N07, A01–A30, and I01–I10; note email / AI conditions and reasons for non-applicability.
2. Verify that T0–T5 have all exited, T6.1–T6.3 are complete, D01–D07 are closed per milestone, and the baseline matches the final source / configuration versions; after this item's acceptance, T6 exits. Unverified items must not be hidden under "future optimizations".
3. Submit `docs/evidence/T6/DELIVERY.md`: changes and scope, evidence, open items, and next steps. Record capability boundaries: exactly-once is not guaranteed outside the provider, and missing verification evidence can block indefinitely.
4. Update STATUS and usage entry points to the actual delivery state; do not rewrite historical snapshots. First-version delivery is distinct from production release; if the user has not requested a release, delivering artifacts and evidence suffices.

Acceptance: the first-version completion conditions in the plan README are met. If in-scope items remain unverified or failing, done is not set; out-of-scope items are recorded as optional T7.
