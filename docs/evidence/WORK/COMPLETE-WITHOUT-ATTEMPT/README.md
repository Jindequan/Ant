# Complete an operation without creating an attempt

Date: 2026-10-05

`PreparationService.CompleteWithoutAttempt` now implements the ledger port for operations that have been accepted but cannot proceed to an execution attempt. It checks the operation's owner, generation, lease and active-attempt state in the write transaction, derives resolver facts from persisted attempts and current cancellation/deadline/budget state, and applies `domain.ResolveResult`. A terminal result, `operation_terminal` event and audit record commit atomically. Repeating the command returns the persisted result without duplicating those writes.

When prior attempts were safely settled and execution continued, the terminal result carries their attempt IDs, resource outcomes, and stored-output reference. Candidate rejection reasons are classified as affinity, admission, or domain facts for the shared resolver.

## Verification

Focused SQLite race tests:

```text
cd server && go test -race ./internal/ledger/commands -run CompleteWithoutAttempt -count=1
exit 0
```

Full repository verification:

```text
make check test-contract test-integration test-web
exit 0
```

This passed runbook freshness, gofmt, `go vet`, `go build`, dependency checks, TypeScript and generated API checks, all Go internal race tests, SQLite integration race tests, and Web tests (7 files / 244 tests).

Source hashes:

```text
e6e02bee2d4f3c25c3094020b0a66d7eb5d695e0a33e4c31db2f5531e89c5b0e  server/internal/ledger/commands/complete_without_attempt.go
55acea69a43bd6f026a1ae10bea916d0730c57f46bb5ea522154a0b90994e612  server/internal/ledger/commands/recovery_test.go
```

This fills the ledger command implementation. The operation executor still needs to invoke it when the planner exhausts candidates; no claim is made that submission or provider execution is end-to-end connected.
