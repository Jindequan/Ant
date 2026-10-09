# M-3: store write budget under gate load

**Question.** `TestPreparationConcurrentCapacityAndSingleConsume` (tests/integration) and
`TestReorderPoolUpdatesLiveAndCompiledPriorityAtomically` (internal/application) each failed once in
a full gate run with "store: command exceeded budget: context deadline exceeded" and passed 10/10
alone. Why does the per-command budget run out?

**Where the budget is.** `sqlite.Config.CommandBudget`, default 50ms (`defaultCommandBudget`,
internal/store/sqlite/store.go), the 02-STORAGE §3 product number. The clock starts at dequeue
(writer.go), so time waiting in the queue does not count; only the command's own transaction does.

**Method.** Temporary per-phase timing in `runOnce` (busy-timeout pragma, BEGIN IMMEDIATE, Run,
commit), logged for slow commands; not committed. CPU profiles of both tests under `-race`.

**Measured (Apple M4, 10 cores, darwin/arm64, go1.27.1, modernc.org/sqlite v1.60.1).**

| command | no `-race`, alone | `-race`, alone | `-race`, loaded |
|---|---|---|---|
| config activate | 2.5ms | ~20ms | max 44ms (n=20) |
| pool reorder | 1.0ms | ~8ms | max 25ms (n=120) |
| ledger prepare | <1ms | p50 13.8, max 21.4ms (n=300) | p50 17.6, p99 51.2, max 67.9ms (n=1000) |
| ledger authorize | <1ms | ~15ms | max 58.9ms |

Load: `go test -race -count=2 -p 20 ./internal/...` and the integration suite running at the same
time, plus 10 busy shell loops (load average ~20). Under that load the ledger test **failed 4/10 with
the exact gate error**.

Over-budget commands, phase by phase: pragma 0.04ms, BEGIN IMMEDIATE 0.06ms, Run 47–68ms, commit
0–16ms. So:

- **not writer-lock contention**: one write connection; the lock is taken in ~60µs every time;
- **not busy timeout or retries**: no busy errors; no attempt was retried;
- **not test parallelism in the queue**: the 100 goroutines only queue, and queue time is outside the budget;
- **the cause**: CPU work inside the transaction. `-race` (tsan plus checkptr on the transpiled C)
  makes SQLite 10–17x slower, and an oversubscribed CPU stretches the wall clock past 50ms.

Rejected while looking: modernc starts one interrupt-watcher goroutine per statement run under a
cancellable context. Running Run's statements under a non-cancellable context left prepare's p50
unchanged (13.5ms), so it is not the cost. Config validation inside the reorder/activation
transaction is ~2.7ms of 7–20ms under `-race`; it is not the bulk either.

**Decision.** The 50ms budget is a deliberate product limit with 15–50x headroom in production
builds, so it stays. The tests are made deterministic instead: a fixture whose subject is not the
budget no longer grades `-race` CPU speed against it.

- `ddc810f` — `TestProfileDefaultsMatchStorageDesign` pins 10ms / 50ms / 2 retries and the
  unset-budget→default resolution structurally (mutation: default 60ms → fails).
- `160a031` — store fixtures in integration, fault, application, api, supply, quotasource,
  sqlitetoken and cmd/ant-server open with an explicit 10s anti-hang budget (`fixtureCommandBudget`;
  integration's `testConfig()`/`openTestStore` fill it in when it is unset). Budget tests keep
  their explicit budgets (all of them already set one; none relied on the default).
  `Config.CommandBudget` documents the rule. Untouched: open-only tests (invalid configs, failure
  path) and `settle_test` (already 2s).

**Verification.** Same load as above, after the change: ledger test 10/10, reorder test 20/20; the
background `./internal/... -count=2 -p 20` run 27/27 packages ok, integration ok. Gates at `160a031`:
`make check`, `make test-contract`, `make test-integration`, `make test-fault`, all exit 0.

**Not done (optional performance work).** About half of the writer's CPU in the prepare test is
SQLite parsing SQL text: every command re-prepares its statements. A per-connection statement cache
would roughly halve command time in every build. The budget does not need it.
