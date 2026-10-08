# LED-03 independent review

**Decision: PASS for the constrained G1-DEV local synthetic slice only.** Reviewed by Codex after implementation was completed. This does not accept live configuration projection, Transport/provider behavior, LED-04 recovery, or full T2 integration.

## Review findings

The implementation closes the six registered review areas: continuation holds and retry policy, fixed/rolling charge attribution and waterfall allocation, persisted identity/mapping validation, owner/generation/idempotency/CAS/lease cases, output availability from existing chunks only, and the LED-04 recovery boundary. Source review found the settlement write remains within one transaction, duplicate matching reports replay their receipt, conflicts are retained for reconciliation, and no schema or generic SQL interface was added. The rolling-window attribution uses the same current-interval cutoff as preparation. Persisted resource columns are checked against both certified definition JSON and the pinned plan before facts are used.

The 686-line settlement test file exceeds the module guideline, but keeping all registered tests in the sole permitted test file preserves the approved write scope. The report explicitly limits report-mapping revision equality to the synthetic G1-DEV convention; real integration must establish the authoritative mapping. No blocker remains for this local slice.

## Independent verification

All Go commands ran with outbound networking denied and module downloads disabled:

```sh
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' \
  env GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly \
  go test -race -count=1 -timeout 300s ./internal/ledger/commands -run '^TestSettlement'

sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' \
  env GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly \
  go test -race -count=1 -timeout 300s ./internal/...

sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' \
  env GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly \
  go test -race -count=1 -timeout 300s ./tests/integration/...

sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' \
  env GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly make check
```

All four commands exited 0. The final report also records five fresh focused race repeats and their logs. Hashes independently checked during review:

| File | SHA-256 |
|---|---|
| `server/internal/store/settle.go` | `111108f4dc31063607a2f9fa9de3b68da1f6dc3c5308a7a1ac695f878b0ef6f1` |
| `server/internal/store/sqlite/settle_statements.go` | `0281502e8b15626bb5548de7387feaf80ee346ec7315182de97d820bff62b3cc` |
| `server/internal/ledger/commands/settle.go` | `8f4389c7a3ee5a6f0f1c0983cd9cd0cda83793c6c25ed69143dbb3e7bb97ba66` |
| `server/internal/ledger/commands/settle_test.go` | `c8f30c13803d511ae8b6f894ba9d04edb796ed23d8dc74e3ca696e3754e941c1` |

Verification logs and implementation detail are in [FINAL-REPORT.md](FINAL-REPORT.md). Acceptance is limited to synthetic local SQLite; no secrets or provider calls were used.
