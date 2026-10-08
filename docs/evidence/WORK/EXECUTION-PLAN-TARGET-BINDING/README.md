# Execution plan target binding

Date: 2026-10-05

The persisted `ExecutionPlan` now carries the provider type, endpoint, and credential reference selected from the active configuration. `CandidatePlanner` copies them from the same preparation snapshot used to build the plan. The ledger reloads current configuration in its write transaction and rejects a plan if any of those values differ. `ControlledHTTPTransport.Prepare` checks the adapter's encoded endpoint and credential reference against the plan before resolving the secret, so an adapter cannot redirect a prepared operation or select another credential.

The TLS integration tests now write their local server URL into the persisted binding before planning, keeping the fixture's active target, execution plan, and encoded request aligned. The tests also verify that endpoint or credential substitution is rejected before secret resolution.

## Verification

Focused race tests:

```text
cd server && go test -race ./internal/application ./internal/transport ./internal/ledger/commands ./tests/integration -run 'CandidatePlanner|ControlledPrepare|ControlledTransport|Preparation.*Plan|Plan.*Stale|CompleteWithoutAttempt' -count=1
exit 0
```

Full repository verification:

```text
make check test-contract test-integration test-web
exit 0
```

This passed runbook freshness, gofmt, `go vet`, `go build`, dependency checks, TypeScript and generated API checks, all Go internal race tests, SQLite integration race tests, and Web tests (7 files / 244 tests).

## Source hashes

```text
41db73678cb39f01a262d5056b112180af4c8b88a2b226a3e41f45c61632c35a  server/internal/domain/planning.go
2e373c25a6b460a0a683f0a371652793c5aab0b389ba0b1ba832defa76222e42  server/internal/store/prepare.go
a86e386ab564a1f553159cc48c8da9bf65a7d992f6c3c044d406fe172439f6f8  server/internal/application/candidate_planner.go
693abb5743f44ccbbfb6e6ca914c99366e336027f2f25e6e937dc57a9da95d9b  server/internal/application/candidate_planner_test.go
384e2723d504b452be2ef6cbda5558420a372304a7637b574fc3514ca672f9ed  server/internal/store/sqlite/prepare_statements.go
c7d7b77ae074a48b47850ef7a3a3e0f62654c1c0e54d72115d4aa4979df226df  server/internal/ledger/commands/prepare.go
b681ab070945a1428b3d12718eb5460c2084127994ec551e48e7d3b100b39702  server/internal/ledger/commands/settle_test.go
6cb57fe1b8f62b2bb6aa758b0cc99488986d2c4735ad68de4c3fd5d81c2b8f2b  server/internal/transport/controlled.go
de139d87b4de29e66036249b385bbb63564b31cbd37f9926235b61048ed1bce5  server/internal/transport/http_client_test.go
cefaaf8dbb682749dc57c798b9ca5f7a4b7a09f0a0ed4a2224c5ec48aeec19d3  server/tests/integration/prepare_test.go
f60db808b1a77b0c4bafe479ffb5d2bba5a70d9b2a7e2df74180d18bd831dd49  server/tests/integration/transport_test.go
e640ba768a3ec9e56ee4546672298bb8f1abb5c8ef6eb1ab6de29378ea93e3c3  docs/design/contracts.go
e8c3f4a5e0d18bb147364e0e01f75db5d8e35d76ebf4c9d7157f8ac7accd67ef  docs/design/05-RUNTIME-AND-ADAPTERS.md
```

The binding is now enforced between planning, ledger preparation, and controlled HTTP preparation. The operation executor remains absent, and no production provider adapter or real provider request is claimed by this work.
