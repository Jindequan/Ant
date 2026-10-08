# G1-DEV post-reopen local integration regression

Date: 2026-10-04. This is fresh regression evidence after the LED-03 reopening; it does not replace the original acceptance reports.

- Command: `make test-integration`
- Result: exit 0; `go test -race -count=1 -timeout 300s ./tests/integration/...` passed (`ant/server/tests/integration`, 11.901s).
- Scope: temporary on-disk SQLite databases, synthetic fixtures/localtest identifiers, and local subprocess contention tests. No real credentials, provider requests, or external HTTP clients are used.
- Raw output: `post-reopen-test-integration.log` (SHA-256 `0345617f721f2e541b7fb0452c7994a205b7f63780599d0f41e7900121fbbeef`).
- Raw log SHA-256: `0345617f721f2e541b7fb0452c7994a205b7f63780599d0f41e7900121fbbeef`

This earlier post-reopen run confirmed the existing local integration suite passed at that point. It predates the over-int64 implementation below and did not cover preservation/isolation or resolve its report/result contract. G1-DEV, G1, and T2 remain separate boundaries.

## Post-reopen source and regression audit

- `make test-contract`: exit 0; fresh `-race -count=1` across `server/internal/...`. Raw output: `post-reopen-test-contract.log` (SHA-256 `322df6a0d25a0a2eec8db1f8591e0de86cbb48d2c1d6fb822dc21f4aa0a26482`).
- `make check`: exit 0; runbook consistency, gofmt, vet, build, dependency boundaries, Web typecheck and generated guards. Raw output: `post-reopen-make-check.log` (SHA-256 `0eea910b9855d6bafeb2801cbaadf4af208b4b9b19a674901db827d010a5fcb5`).
- `python3 docs/plans/check_tree.py`: exit 0; 55 tasks, 8 gates, DAG valid. Raw output: `post-reopen-task-tree.log` (SHA-256 `b4d767febe49fc7594381c6c7132a4bdf8a3748055f8a7507722f435ac7bd6a1`).
- The aggregate `source-manifest.sha256` contains 31 paths, but the union of source hash tables in the six leaf acceptance reports is 33. It omits `server/internal/store/settle.go` and `server/internal/store/sqlite/settle_statements.go`; both are in the LED-03 approved write set and have hashes in its independent review. The latest audit has 28/33 historical source matches and five historical hash differences:

  | File | Accepted SHA-256 | Current SHA-256 |
  |---|---|---|
  | `server/internal/ledger/commands/abort.go` | `614680cd0082b227d25386ceed11cfd2b9f4e6abd1742bdd659a71f7771a235e` | `0d15aeed518527f8eb6c1a0329a192949d258627f755dff8cd657fbf6ad5b50c` |
  | `server/internal/ledger/commands/settle.go` | `033fa3a5dd506ac31f0bb55eee9b10aebf11019c7f2ac7244e14da818485df1f` | `fe51bc848472081d02f82e2019bd72bc18683d28d5404ab18aba2769436bedd1` |
  | `server/internal/ledger/commands/settle_test.go` (post-LED-04) | `455f8315286e39df45549de35853d08ac6e96ec53d85dc66f9c3b42760deacd5` | `2fa3259df9a46baf3d77039816a2a9c204389dd86786adea6b9fc82858884f9b` |
  | `server/internal/ledger/commands/sweep.go` | `c04d8fb2ff3ce7000cb57341995260121bfee8d2f9a9040a567abdf7c653f90c` | `f3da023f7df0b0ed81644bda07d03829015333be0bac6e94a94f714d2961a837` |
  | `server/internal/store/sqlite/settle_statements.go` | `0281502e8b15626bb5548de7387feaf80ee346ec7315182de97d820bff62b3cc` | `1de9dcd3616f54399a7bc31e95e4306c64018213c46f7291ca686a025d734d9e` |

  `server/internal/store/settle.go` matches its accepted hash. Current-source reviews now cover all five differing paths: the two earlier LED-04 queue/source files and the new LED-03/contract edits to `abort.go`, `settle.go`, and `sweep.go`; each current digest matches its review. They do not reconstruct the unavailable historical source delta. The original 31-path check log is `source-manifest-post-reopen-check.log` (SHA-256 `9e136953c5fbbfc8a05c03e31741db2610210ef7ac418a18e71d3197ea777163`). Current settlement-path review details are in [LED-03 source review](../LED-03/ACTUAL-OUT-OF-RANGE/CURRENT-SOURCE-REVIEW.md) and [shared LED-03/04 review](../LED-04/CURRENT-TREE-REVIEW-2026-10-04.md); historical acceptance records and manifest remain unchanged.

  Reproduce the leaf-report union and current-file comparison with `python3 docs/evidence/WORK/G1-DEV/reconcile_sources.py`. The script reads the six reports in chronological order, retains all historical hashes for shared paths, uses the later leaf review as the effective accepted hash, and does not modify the historical aggregate manifest. It checks separate LED-03 and LED-04 current-tree reviews, so historical mismatch and present-source review are distinct. It exits 1 while historical omissions or hash mismatches remain. Current output: 36 report entries, 33 unique paths, 28 historical matches, five historical mismatches, no missing files, two manifest omissions, and 5/5 current-tree-reviewed files matching their review hashes. Machine-readable output is `source-reconciliation.json`; script: `reconcile_sources.py`.
- `make verify-manifest` remains non-zero due the accumulated post-T1 implementation/gate-script changes; its raw output also shows the unexpected settled-test hash from the preceding item; raw output: `post-reopen-verify-manifest.log` (SHA-256 `04fc9610ddfe4bf0fa0b9cf484acaece2f667b6f5122f5f35d5249de9709e5d6`). It reports additions and modifications, no removals. This is not evidence that the current tree is byte-identical to T1's accepted 184-file baseline.

## Fresh current-tree rerun (2026-10-04)

- `make test-contract test-integration`: exit 0; internal `-race -count=1` and SQLite integration `-race -count=1 -timeout 300s` passed. Raw output `current-tree-race-2026-10-04.log` SHA-256 `2cf9cedc13c40391fe0b0f0f118372e0d42013c39eb3f03ecb27ed047b763ef9`.
- `cd web && npm test`: exit 0; 6 files, 235 tests passed. Raw output `current-tree-web-2026-10-04.log` SHA-256 `3670e17695526aef057ea9b6d9dcf4a4871283dffa85e829da494237783c256a`.
- `make check`: exit 0 after removal of the obsolete repository hook stubs; runbook consistency, formatting, vet, build, dependency boundaries, Web typecheck and generated-file guards passed.
- `python3 docs/plans/check_tree.py`: exit 0; 55 tasks, 8 gates.

This rerun increases confidence in current behavior but does not independently accept the five historically divergent source files, close LED-03/LED-04, satisfy G1, or establish production Hooks call sites. Their statuses remain unchanged.

No source restoration or historical manifest re-sign was performed: this workspace has no Git metadata, and a targeted search under `/Users/devin` outside Library and large caches found only the current copies of the two previously divergent files. The aggregate manifest omissions and five historical hash differences remain explicit audit items, while all five current-source review hashes match. Passing tests do not substitute for historical source identity review, and this audit alone does not restore LED-03 acceptance.
