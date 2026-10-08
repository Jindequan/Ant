# LED-03/04 shared settlement current-tree review addendum

Date: 2026-10-04. This is a fresh review of the present local tree after the aggregate G1-DEV manifest audit found source-hash drift. It supplements, and does not rewrite, the historical LED-03/04 acceptance reports. It does not claim to reconstruct the unavailable old-file diff or independently re-sign the leaf acceptance.

## Reviewed files

| File | Historical LED-04 hash | Current SHA-256 | Current lines |
|---|---|---|---:|
| `server/internal/ledger/commands/settle_test.go` | `455f8315286e39df45549de35853d08ac6e96ec53d85dc66f9c3b42760deacd5` | `2fa3259df9a46baf3d77039816a2a9c204389dd86786adea6b9fc82858884f9b` | 870 |
| `server/internal/store/sqlite/settle_statements.go` | `0281502e8b15626bb5548de7387feaf80ee346ec7315182de97d820bff62b3cc` | `1de9dcd3616f54399a7bc31e95e4306c64018213c46f7291ca686a025d734d9e` | 216 |

`settle_statements.go` did not change in this review. The accepted-to-current source delta remains unexplained because no prior copy or Git history is available. Current-source inspection confirms that settlement facts compare stored resource columns against certified definition JSON, the pinned reservation, plan window revision, and account/binding owner; continuation facts include account, binding, credential, domain, resource holds and open encumbrances; output availability comes only from stored chunks. Resource deltas, reservation phase changes, attempt report, result, and ordered events are applied through the surrounding writer transaction and CAS checks. The schema constrains `owner_kind` to account or binding.

The current `settle_test.go` review found that `TestSettlementQueuedLeaseExpiry` used `time.Sleep(100ms)` to infer that a settlement command had entered the writer queue. That timing assumption was replaced with a test-only `queueObservedStore` decorator: only when `SettlementService` reaches `SubmitWrite` does the decorator wrap the context, and the underlying SQLite `SubmitWrite` signals through `Context.Done()` after bounded-lane admission. The test advances the clock only after that deterministic enqueue signal, independent of `SettlementService`'s internal pre-submit steps. The assertion remains that a lease expiring while queued must retain reconciliation evidence and leave the executing attempt, active slot, and uncharged counter unchanged. This review change accounts for 25 added lines and the current 716-line file; it does not explain the full pre-existing difference from the historical 691-line file or establish byte-for-byte equivalence.

The current test also covers the LED-03 over-int64 settlement: exact report/result preservation, null consumed value, resource hold and reservation release, follow-up prepare denial, replay/conflict idempotency, malformed values, and forced transaction rollback. The API enum assertion verifies `provider_reported` for measured resources and `not_applicable` for occupancy. These tests cover the current intended source behavior; they do not reconstruct the full historical source delta. The companion [LED-03 source review](../LED-03/ACTUAL-OUT-OF-RANGE/CURRENT-SOURCE-REVIEW.md) records the current `settle.go` and `abort.go` digests and the accounting-path review.

## Fresh verification

| Command | Result | Evidence |
|---|---|---|
| `go test -race -count=10 -timeout 300s ./internal/ledger/commands -run '^TestSettlementQueuedLeaseExpiry$'` | PASS, 10 repetitions | `queue-barrier-focused-race.log` (SHA-256 `fdfacf2673434bd72ca6a9e4975b05756fb2b7442fa74b57dabeb5e8449de9e4`) |
| `make test-contract test-integration` | PASS; all internal and integration Go race suites | `queue-ordering-regression.log` (SHA-256 `a83486a21ccaf7cf5364beea840d5a949d9793baf906fa0aab37568a4de28a7e`) |
| `make check` | PASS; runbook, gofmt, vet, build, dependency, Web typecheck and generated guards | `current-tree-make-check.log` (SHA-256 `0eea910b9855d6bafeb2801cbaadf4af208b4b9b19a674901db827d010a5fcb5`) |
| `python3 docs/plans/check_tree.py` | PASS; 55 tasks, 8 gates, DAG valid | `current-tree-task-tree.log` (SHA-256 `b4d767febe49fc7594381c6c7132a4bdf8a3748055f8a7507722f435ac7bd6a1`) |

## Decision

The sleep-based queue race is removed and the present tree passes fresh Go race, integration, and repository checks. The reconciliation audit continues to report historical source-hash differences and two paths omitted from the historical aggregate manifest; the separately reviewed current settlement sources match their listed current digests. LED-04 remains `verifying` because the prior source copy is unavailable. LED-03's report/schema design blocker is resolved and the current implementation is under verification; real Adapter normalization remains a RUN-01/G1 obligation. These records do not rewrite the historical acceptance reports.
