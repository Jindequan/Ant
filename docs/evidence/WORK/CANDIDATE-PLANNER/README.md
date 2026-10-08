# Candidate planner implementation record

Date: 2026-10-05

This is implementation evidence for the application-level candidate planner. It is not an independent acceptance of T2.1, T2.3, RUN-04, or the product execution path.

## Implemented

- Reads the active compiled configuration, live pool, pool members, and offer projections inside one `Store.Read` snapshot. It rejects projection/version disagreement, including member priority drift.
- Applies domain/profile, offer/member state, required affinity, metered-spend policy, binding state, and account state before candidate ranking.
- Asks a registered domain policy for resource requirements and evidence bounds, then validates resource identity, metric/unit, evidence source, bound, and expiry against the active configuration.
- Uses a production wall clock and an injectable package-private clock for deterministic tests. Expired bounds are rejected. The fixture's original evidence expiry values remain unchanged.
- Pins the fixture's USD resource to `alpha-usd-grant-promo`, the matching provider fact, to avoid an ambiguous dimension lookup.

## Verification

Executed from the repository root on 2026-10-05:

```text
make check test-contract test-integration test-web
exit 0
```

The command ran runbook freshness, gofmt, `go vet`, `go build`, dependency checks, web typecheck/generated-file guard, all Go internal tests with race detection, SQLite integration tests with race detection, and Web tests. Web result: 6 files and 243 tests passed.

Focused planner tests also cover active config compilation, stable tie ordering, plan fields, expired evidence, disabled accounts, and missing policy selection.

## Source hashes

```text
2c85aeaa9fa03e4329c7c25103284ca6cd3a39bd6155a324f3f45052ce27dc4a  server/internal/application/candidate_planner.go
66dec37de0627eda63d0be0407006896f28e2a381adf52ba432abd33dd9dba29  server/internal/application/candidate_planner_test.go
56ddc2106f0a76f7a4b5bd02893131201d7fff8bcae1766a6fa8231629850df0  contracts/fixtures/config-document-valid.json
```

## Still incomplete

`CandidatePlanner` is not mounted in `ant-server`, is not invoked by `Router` or an `OperationExecutor`, and has no production domain policy or provider adapter. No external provider request or live user journey was run. Those are required before claiming T2.1/T2.3/RUN-04 or an end-to-end product flow complete.

## 2026-10-05 follow-up

The planner now rejects any bound whose `MappingRevision` differs from the plan's `ReportMappingRevision`. Settlement already requires this identity, so checking it during planning avoids emitting a plan that the ledger would later reject. A regression test exercises this mismatch.

Fresh verification from the repository root:

```text
go test -race ./server/internal/application -run CandidatePlanner
make check test-contract test-integration test-web
exit 0
```

The full command passed static checks, all Go internal race tests, SQLite integration race tests, and Web tests (7 files / 244 tests).

Updated source hashes:

```text
09353343b4ff629b07d4123df8cf65d18d8884bbb0b0b8059a247a7f66c6041a  server/internal/application/candidate_planner.go
c7e08b6b59ac0a076ecd3b131609aa0d71633fadb9e5c9150608d66135e8ba6d  server/internal/application/candidate_planner_test.go
```
