# LED-01 claim transaction — acceptance draft

State: `done` for the G1-DEV local leaf only. Independent review: [INDEPENDENT-REVIEW.md](INDEPENDENT-REVIEW.md). G1-DEV only. This is a Codex implementation and self-check, not independent review. No real credentials, provider requests, or external network access were used.

## Scope and behavior

- Added a narrow `ClaimWriterTx` optional extension in `server/internal/store/claim.go`; the frozen T1 `WriterTx` and `Store` method sets remain unchanged.
- Added the SQLite operation-row read/insert statements in `server/internal/store/sqlite/claim_statements.go`; the command still enters through `SubmitWrite` and uses the command's one SQL transaction.
- Added `server/internal/ledger/commands/claim.go`: it validates execute permission and semantic fingerprint, requires an enabled pool in the authenticated namespace, active config and no restore guard, then inserts the running operation and `claim/<namespace>` receipt atomically. The initial owner ID is random UUIDv4; repeated same-semantic claims return `inProgress` without another owner; a semantic mismatch returns `conflict`; terminal retries replay the stored result. Existing IDs resolve before first-claim-only deadline/attempt/runtime policy checks, so a late same-ID retry still returns `inProgress` or replays its terminal result. The receipt does not persist or reissue the owner token.
- The task scope was extended because `BASE-05` exposes no operation-row transaction primitives. The extension is additive and keeps SQL inside `store/sqlite`.

## Evidence

- Real-file SQLite integration tests cover execute-permission and restore-guard rejection, first claim, same-ID/same-semantics reuse despite strategy changes, expired-owner no-reissue, different-semantics conflict, terminal result replay, and transaction rollback when receipt insertion fails.
- A subprocess test starts two independent Go test processes against the same SQLite file behind a barrier; exactly one receives `new` with an owner, while the other receives `inProgress` without an owner. Operation and receipt row counts remain one.
- Late same-ID retries with an elapsed deadline and invalid current attempt/runtime policy still resolve existing running and terminal records; invalid policy on a new operation is rejected. This matches `docs/design/03-EXECUTION.md` §2, which resolves an existing ID before applying new-operation policy.
- Initial targeted race run passed: `go test -race -count=1 ./tests/integration/ -run 'TestClaim'`.
- Cross-process race test passed five consecutive runs: `go test -race -count=5 ./tests/integration/ -run '^TestClaimCrossProcessGrantsExactlyOneOwner$'`.
- Final full validation passed after guard/replay test additions under a macOS sandbox that denies outbound networking: `go test -race -count=1 ./internal/... ./tests/integration/...`, `go vet ./...`, and `go build ./...`.

## Remaining

- Independent review of the final source tree and acceptance evidence.
- Final manifest verification/re-sign after review. The previously signed T1 files were not edited by this leaf.
- Expired-operation recovery remains LED-04 work; claim does not mint a replacement owner for an existing running operation.

2026-10-03 Codex expanded review found the initial implementation applied deadline/attempt/runtime validation before reading an existing operation, contrary to the frozen claim order. Validation now runs only after the transaction proves the operation ID is new; regression tests cover late in-progress and terminal retries plus invalid policy on new claims. Final outbound-denied `go test -race -count=1 ./internal/... ./tests/integration/...`, `go vet ./...`, and `go build ./...` pass. This is Codex review/fix evidence, not an independent second-party acceptance; state remains `verifying`.

Root `make check` then caught unregistered T2 package edges. The shared `scripts/check-deps.py` graph now allows `ledger/commands→store` without widening the parent `ledger` package and retains only API as the non-cmd `net/http` boundary. A negative import probe confirms parent `ledger→store` is still rejected; the full outbound-denied root `make check` passes.
