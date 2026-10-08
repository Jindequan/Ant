# LED-04 self-check acceptance draft (one-shot implementation batch)

> **Snapshot notice:** this is the pre-review self-check record, not the final source snapshot. Codex added the registered PermitRegistry terminal compaction, settlement/cancel call sites, overflow and JSON-encoding fixes after this draft. Its original hashes and “one sub-criterion open” conclusion are superseded by [INDEPENDENT-REVIEW.md](INDEPENDENT-REVIEW.md); use that file for final acceptance and hashes.

State: **verifying — self-check PASS on the in-scope items; Codex acceptance pending.** One acceptance sub-criterion (permit-registry map lifecycle) needs a narrow write-scope extension and is precisely registered below (§ Write-scope gap). This file is implementation evidence only; it is not acceptance.

Task: 取消、续租、清扫与未发送收敛 (TASK-INDEX `LED-04`, entryGate G1-DEV, phase T2.2, dependsOn LED-03). Old dispatch left no source or evidence; this implementation starts from the accepted LED-03 baseline (write set audited first: none of the seven paths existed).

## Final source hashes (SHA-256)

| File | SHA-256 | Lines |
|---|---|---|
| `server/internal/ledger/commands/abort.go` | `614680cd0082b227d25386ceed11cfd2b9f4e6abd1742bdd659a71f7771a235e` | 426 |
| `server/internal/ledger/commands/cancel.go` | `c27959a994ce4848c41dcba0786950743e07f495ab9a05a70be7b328b911f75a` | 120 |
| `server/internal/ledger/commands/renew.go` | `3bb05f4edb8a831cf9185f8999a31ae9c5e437ff0426c67547bbcc1147fa09af` | 71 |
| `server/internal/ledger/commands/sweep.go` | `b37fe35c959b71b4354c764b691c898f54b23149534e7af4c14d7adbdd167918` | 156 |
| `server/internal/ledger/commands/recovery_test.go` | `ee58249d9f785590b8baa0287323d4a5bd5f464b0696f9c3a675a5eea70de87b` | 473 |
| `server/internal/store/recovery.go` | `bedcc23435b751b7bcc99fbb9924fffc8875897e2db7a2b1ad4dc772ecbd41a7` | 67 |
| `server/internal/store/sqlite/recovery_statements.go` | `cbfe26179a4ef910cc0ef45ccc7871ab21c2e4486588817d12ab892f2c1f7159` | 178 |

## What was built (design mapping)

1. **AbortBeforeDispatch transaction** (`commands/abort.go`, 03-EXECUTION §9). Reads `(attempt.phase, permit.status, generation)` inside one BEGIN IMMEDIATE, runs the frozen `ledger.DecideAbort` table, applies the whole `AbortOutcome` atomically: counter release via `CounterSettle` with `EndProofReliableUnsent` (charge nothing) on all three not-executed rows; occupancy release as a guarded `reserved`/`active` delta (deliberately NOT `OccupancyEndProved`, which requires provider end evidence); `conserve_unknown` charges the certified bound (`EndProofNone`, lifetime attributed, other windows isolated + held) and moves occupancy `active→uncertain` with an open encumbrance. Competes with consume/sweep on the same CAS guards (`RevokeRecoveryPermit`, `FinishRecoveryAttempt(phase)`, reservation phase CAS); any miss rolls back the whole command with `ledger.ErrAbortConflict`. Receipt idempotency key `abort/<attemptId>` + input hash (owner key, attempt, proof, permit id, system flag); replay returns the stored conclusion verbatim, a different hash is a conflict. `ProofReliableUnsent` requires the in-process `DispatchPermit` handle AND same-source identity with the durable `dispatch_permits` row (permit_id/runtime_id/attempt_id/request_digest, status consumed) — caller claims alone are never sufficient.
2. **RequestCancel** (`commands/cancel.go`, §7). Records intent + first instant (COALESCE) under version CAS, append-only `cancel_requested` event, audit, receipt per command id (`recorded`/`alreadyTerminal`/`notFound`, replay-safe). A **prepared** attempt is closed inline through the same abort core in the same transaction (§9 supplement 1); an **executing** attempt stays intent-only — covered by report/conservation/sweep, and a confirmed success report outranks the intent.
3. **RenewLease** (`commands/renew.go`, §7). Conditional UPDATE per owned running operation, competing with sweep on the same row; lease capped at deadline, `deadline_at > now` guard, active attempts' advisory lease kept coherent. Stale owner / lost generation / terminal operation → `ErrRenewStale` (host must stop dispatching, not retry).
4. **Sweep** (`commands/sweep.go`, §7). One control transaction, due set `lease_until <= now` ordered, batch capped at 100. Per operation: active attempt → the same abort core with `system=true` (prepared → release + not_executed; executing/granted → CAS-proven unconsumed: revoke + not_executed, zero send; executing/consumed → conservative unknown: bound committed, occupancy uncertain + encumbrance — lease expiry is never external-end evidence); no active attempt → unified resolver on durable attempt facts. `ExecutionInterrupted/owner_lease_expired_before_dispatch` only on the sweep path and only when `permits_issued == 0` (§6: 未授许可). Generation always bumped on takeover; an orphan the resolver cannot yet complete stays running parked at its deadline until the resolver completes it (nothing fabricated early). Audit row per swept operation.
5. **Store capability** (`store/recovery.go`, `sqlite/recovery_statements.go`). Narrow additive `RecoveryWriterTx` embedding the frozen `WriterTx` + LED-03 `SettlementWriterTx`; SQL only in `store/sqlite`; guarded UPDATEs return applied=false on CAS miss; `ApplyRecoveryDelta` writes resource events with explicit kinds (`release`/`consume`/`encumber`) so a recovery release is never mislabeled as consumption. No schema change, no frozen signature change, no generic SQL.
6. **Result resolution** stays single-source: every terminal path feeds `domain.ResolveResult` with facts derived from persisted attempts/outcomes (`recoveryResultFacts`); six-way priorities hold on recovery paths (success > unknown > cancelled > failure ladder > running).

## Acceptance criteria mapping

| Criterion | Evidence |
|---|---|
| 六类竞争有稳定结果 | `TestAbortClosesPreparedAttempt` (replay identical), `TestCancelExecutingIntentOnlyThenSuccessWins` (success > cancel), `TestAbortAfterConsumeWithoutProofConserves` + `TestSweepExecutingConsumedConservesUnknown` (unknown > cancel, late report → reconciliation, accounting preserved), `TestCancelPreparedClosesAndCancels` (cancelled), `TestSweepPreparedReleasesAndCompletesInterrupted` + `TestSweepExecutingGrantedRevokesAndParksAtDeadline` (interrupted / deadline ladder, stable same-ID replay) |
| lease 不等于外部结束 | `TestSweepExecutingConsumedConservesUnknown` (sweep of consumed attempt → `unknown`, never `not_executed`) |
| 可靠未发送才释放 | `TestAbortReliableUnsentProof` (positive release + three fail-closed negatives: missing handle, granted permit, foreign attempt), `TestSweepExecutingGrantedRevokesAndParksAtDeadline` (CAS-proven unconsumed → zero-send revoke), `TestAbortRevokesGrantedPermit` |
| 许可注册表有生命周期 | **Partial — in scope:** `releaseTerminalAttempt` drops cached handles and mirrors durable revocation in the runtime registry after commit (observed: post-abort authorize/consume rejected in `TestAbortRevokesGrantedPermit`, `TestSweepExecutingGrantedRevokesAndParksAtDeadline`). **Gap:** `ledger.PermitRegistry.byID/byAttempt` map compaction needs a `server/internal/ledger/permit.go` change outside this leaf's write scope — see below |
| A04/A05/A15/A20 rows | prepared 崩溃/取消 + 旧 owner 禁写 (`TestSweepPreparedReleasesAndCompletesInterrupted`), executing 失联 + 迟到报告 (`TestSweepExecutingConsumedConservesUnknown`), K3 验收行 prepare 后改序 / authorize 后改序 / consume 回复丢失 / consume 后崩溃 / prepared 取消 + 下次 prepare (replan asserts in each abort test), 授权后未消费跨 lease 暂停零发送 (`TestSweepExecutingGrantedRevokesAndParksAtDeadline`) |
| 故障/竞争 fail-closed | `TestRecoveryFailsClosedOnDrift` (resource identity drift and reservation-phase drift abort with zero durable side effects), CAS misses roll back whole commands |

## Self-check verification (all under `sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' env GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly`, synthetic local SQLite, no secrets, no provider calls)

| Check | Command (from `server/` unless noted) | Exit | Log |
|---|---|---|---|
| Focused recovery race | `go test -race -count=1 -v -run 'TestAbort\|TestCancel\|TestRenew\|TestSweep\|TestRecovery' ./internal/ledger/commands/` (13 tests) | 0 | `focused-race.log` |
| Full internal race | `go test -race -count=1 -timeout 300s ./internal/...` | 0 | `internal-race.log` |
| Integration race (claim/prepare regression) | `go test -race -count=1 -timeout 300s ./tests/integration/...` | 0 | `integration-race.log` |
| Five fresh focused race repeats | same as focused, ×5 | 0 ×5 | `race-repeat-1..5.log` |
| Root `make check` (gofmt, vet, build, check-deps, web typecheck, guard:gen, runbook check) | `make check` at repo root | 0 | `make-check.log` |

`make verify-manifest` reports the known accumulated T2-DEV drift (AUTH-01/CFG-08/LED-01..04 files, including the seven LED-04 paths); the global manifest re-sign remains deferred to the Codex T2-DEV file-set review, unchanged from prior leaves.

## Write-scope gap registered for Codex (permit-registry map lifecycle)

The blocked record (STATUS, 2026-10-02, T2.2 P3-1) requires lifecycle cleanup of `ledger.PermitRegistry` (`server/internal/ledger/permit.go` `byID`/`byAttempt`) after settle/sweep terminal states. That file is **not** in LED-04's write scope, and the runbook forbids silently extending it. Delivered in scope: handle-cache cleanup + registry revocation mirroring after every LED-04 terminal path (above). Precise proposed extension (~15 lines, new method only, no frozen-interface change):

```go
// Release drops the registry record of an attempt whose permit reached a
// terminal state (consumed/revoked) in a committed transaction; the durable
// dispatch_permits row remains authoritative.
func (r *PermitRegistry) Release(attemptID domain.ID) { ... delete(r.byID, permitID); delete(r.byAttempt, attemptID) ... }
```

Wiring: call from `releaseTerminalAttempt` (commands) and from LED-03 settle's post-commit path (settle.go is also outside this leaf's scope — same request covers both call sites or a follow-up leaf). Per the scopeNote this sub-item is **stopped for Codex review**; everything else is self-checked PASS.

## Deviations and notes for the independent reviewer

- **Event vocabulary**: abort terminalization of an attempt reuses the frozen `attempt_settled` operation-event type (schema CHECK constrains the enum; adding a type would be a schema change). Payload is the resource-outcome list, same shape as settle. `ConsumptionBasis` gains the value `not_executed` on internal outcome payloads (free string in `domain.ResourceOutcome`; not a wire DTO change).
- **Sweep leave-running parking**: an orphan with a once-granted (now revoked, proven unconsumed) permit and nothing pending is left running under a bumped generation with `lease_until = deadline` until the resolver can complete it (deadline ladder). Rationale: §7 completes the no-attempt case via the unified resolver; fabricating a terminal state outside the resolver would violate I08, and leaving it due would loop generation bumps. `TestSweepExecutingGrantedRevokesAndParksAtDeadline` pins this behavior, including the deadline completion.
- **`ExecutionInterrupted` fact**: set only on the sweep path and only when `permits_issued == 0` and every attempt is confirmed not-executed-or-ended — the strict §6 "未授许可" reading. Owner-driven aborts never set it (the owner is alive and replanning).
- **Renew vs sweep race semantics**: both sides are conditional UPDATEs on the same row inside BEGIN IMMEDIATE; whichever commits first wins. A renewal whose SubmitWrite ctx ended may still have committed — callers rely on receipts, same contract as every other command.
- `recovery_test.go` reuses the settle fixture from `settle_test.go` (same package) rather than duplicating ~70 lines of synthetic store setup; the second-operation pattern (`freshRunningOperation`) exists because the fixture auto-dispatches its own attempt.
- Coverage limits (unchanged from prior leaves): synthetic local SQLite slice only; real Transport-provided unsent proofs, real config projection, multi-process fault injection (`test-fault`, T3) and full T2 integration remain open.
