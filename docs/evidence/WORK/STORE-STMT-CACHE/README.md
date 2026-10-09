# Write-pool prepared-statement cache (M-3 follow-up)

**Question.** M-3 found that about half of the writer's CPU in
`TestPreparationConcurrentCapacityAndSingleConsume` is SQLite parsing SQL text: every writerTx
method passes SQL text to `tx.QueryContext`/`ExecContext`/`QueryRowContext`, so each command
re-prepares every statement. Does a per-Store prepared-statement cache on the single write
connection pay off, and is it sound?

**Change** (`d3e9f11`). `internal/store/sqlite/stmtcache.go`. The cache maps SQL text to a `*sql.Stmt`
prepared on `writeDB`. Inside a command, `stmtTx` binds a cached statement to the transaction with
`tx.StmtContext(ctx, stmt)`. Statements cannot be prepared while a command holds the only
connection, so a miss runs as plain SQL and is recorded. The writer prepares the recorded misses
after delivering the result and before the next dequeue, so no command's budget pays for that.
`Store.Close` closes the cache after the writer has drained and before the pools close. The cache
is capped at 512 entries for runtime-built SQL (`disableMissing`'s IN lists); variants beyond the
cap run uncached. The 50ms product budget is unchanged.

## Soundness checks

| claim | how it holds | pinned by |
|---|---|---|
| database/sql reuses the connection-level handle | `Tx.StmtContext` searches the statement's per-connection list for the tx's `driverConn` and reuses its `driver.Stmt` (go1.27.1 `database/sql/sql.go`). With `SetMaxOpenConns(1)` that is always the prepared one. If the connection were replaced, database/sql would re-prepare on the new one. | `TestStatementCacheReusesConnectionHandle`: a wrapper driver counts `Prepare` calls. The first command gives 3 prepares (one per statement), then 0 more over 20 cached commands. The probe is sensitive: one `tx.Prepare` moves the count by 1. Mutation (prepare on the tx instead of `StmtContext`) → fails with "20 more". |
| modernc keeps the compiled statement | v1.60.1 `stmt.go`: a single-statement `stmt` keeps its `pstmt`. `exec` binds, steps, then resets and clears bindings. `query` hands the handle to `rows` with `reuseStmt`, and `Rows.Close` resets it. Multi-statement scripts are still parsed per run (migrations only). | covered by the reuse test above |
| statements run under the command deadline (writer.go contract) | The per-call ctx reaches the driver, which runs `interruptOnDone(ctx)` on the connection. An already-expired ctx is refused earlier by `Tx.grabConn`. | `TestStatementCacheKeepsCommandDeadline`: a cached recursive count over ~1e12 rows under a 50ms budget is interrupted (`context.DeadlineExceeded`, command → `ErrCommandBudget`), and the handle is reusable afterwards. Mutation (pass `context.Background()` to the driver) → the statement runs on and the test times out. |
| schema changes / SQLITE_SCHEMA | statements come from `sqlite3_prepare_v3`, which re-prepares on a schema change. Migrations run before the first command, and a missing table makes prepare fail, so nothing referencing it is cached. | `TestStatementCacheSurvivesSchemaChange`: `ALTER TABLE command_receipts ADD COLUMN` after the cache is warm; cached statements on that table keep working. |
| backup (`submitMaintenance`, VACUUM INTO) | VACUUM needs no statement in progress on the connection. Cached handles are reset after every use, and the end of the transaction closes any rows a command left open. Maintenance runs on the writer loop between commands. | `TestStatementCacheAllowsBackupAndClose`: backup with a warm cache succeeds, and commands use the cache afterwards. Close empties the cache, and a later submit gets `ErrQueueClosed`. |
| nested reuse of a shared handle | Running a cached query again while its own rows are still open would reset the outer cursor. `stmtTx` remembers the last `Rows` of each cached query in the transaction and runs a nested use as plain SQL. An audit found no such nesting in the current `*_statements.go`. | `TestStatementCacheNestedQueryRunsUncached`: outer 1..3 with the same query inside, outer `[1 2 3]` and inner complete. Mutation (guard off) → `bad parameter or other API misuse (21)`. |

## Measurements

Method: temporary timing of `cmd.Run` inside `runOnce` (BEGIN IMMEDIATE to Run return, so commit
time is excluded), written per command to a file. A temporary env switch set the cache bound to 0
inside the same binary, so both arms ran the same tree, alternating. None of this was committed.
Apple M4, 10 cores, darwin/arm64, go1.27.1, modernc.org/sqlite v1.60.1.

**Warm commands.** `TestPreparationConcurrentCapacityAndSingleConsume`, 4 alternating rounds of
`-count=5` per arm. The first command of each kind per store is excluded here and reported below.

| command | build | n | p50 off → on | p90 off → on | p99 off → on | total time off/on |
|---|---|---|---|---|---|---|
| prepare | none | 1980 | 399µs → 149µs | 471µs → 197µs | 876µs → 526µs | 2.43x |
| claim | none | 2000 | 86µs → 34µs | 103µs → 45µs | 175µs → 178µs | 2.30x |
| prepare | -race | 1980 | 13.67ms → 4.31ms | 15.36ms → 6.21ms | 17.02ms → 6.75ms | 2.70x |
| claim | -race | 2000 | 2.94ms → 1.08ms | 3.04ms → 1.14ms | 3.43ms → 1.54ms | 2.71x |

**Suites made of fresh stores** (most commands are a store's first of their kind). Two
alternating rounds of the integration suite and `internal/application`. These were taken before
the nested-use guard was added. The guard adds one map lookup per statement; the warm table above
was taken with it.

| command | build | n | p50 off → on | p90 off → on | p99 off → on | total time off/on |
|---|---|---|---|---|---|---|
| prepare | none | 330 | 448µs → 177µs | 671µs → 658µs | 1.23ms → 1.06ms | 1.52x |
| authorize | none | 66 | 496µs → 272µs | 757µs → 527µs | 1.45ms → 1.51ms | 1.60x |
| consume | none | 36 | 334µs → 142µs | 473µs → 170µs | 1.03ms → 845µs | 2.17x |
| prepare | -race | 330 | 13.66ms → 7.68ms | 15.80ms → 18.41ms | 26.08ms → 36.41ms | 1.28x |
| authorize | -race | 66 | 13.27ms → 7.36ms | 16.80ms → 15.18ms | 29.93ms → 60.36ms | 1.38x |
| consume | -race | 36 | 9.78ms → 3.19ms | 14.06ms → 6.10ms | 51.42ms → 26.90ms | 2.29x |
| config activate | -race | 24 | 17.57ms → 10.01ms | 20.57ms → 24.95ms | 21.71ms → 29.95ms | 0.95x |
| pool reorder | -race | 12 | 2.05ms → 1.46ms | 8.21ms → 6.70ms | 8.51ms → 7.32ms | 1.27x |

**First command of a kind (cold).** In the concurrent prepare test under `-race`, the first prepare
per store took a median of 14.6ms with the cache off and 17.1ms with it on (n=20 each). Config
activation shows the same pattern (48 samples: cold ~19ms → ~21ms; warm ~18ms → ~9ms). An
isolated test, with a fresh store and one command of uncached statements and nothing else running,
**does not reproduce it**: medians 5.12/5.15ms off vs 5.14/5.40ms on under `-race`, and 161/162µs vs
196/154µs without. Two candidates were ruled out: 40 resident idle statements and 40 resident
statements executed once (no change to the uncached path). The cause in the concurrent test was
not found. The tail growth in the fresh-store suites comes from these cold commands. The p99/max
cells rest on 1–3 samples, so they are noisy.

## Decision

Merged. A long-running server runs nearly every command warm, and warm commands are 2.3–2.7x
faster in both builds. Under `-race`, warm prepare has its p99 at 6.75ms against the 50ms budget
(was 17ms). The cost is that each statement's first use in a process runs uncached and is then
prepared once more between commands. Suites built from fresh stores therefore gain less, and their
tails can grow under `-race`.

**Verification.** At `d3e9f11` (main, after the design 13 phase C fast-forward, which touches
nothing under `internal/store`): `make check`, `make test-contract` (28 packages ok),
`make test-integration` and `make test-fault`, all exit 0.
