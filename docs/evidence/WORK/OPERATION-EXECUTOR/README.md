# Operation execution coordinator

Date: 2026-10-05

`application.CandidateOperationExecutor` implements the `OperationExecutor` port consumed by `Router`. It refreshes candidates against the active configuration, quotes resource requirements, encodes through a registered provider adapter, obtains trusted guard evidence when configured, prepares the controlled transport request, and asks the ledger to reserve and authorize before one dispatch. It persists decoded output chunks before settlement. A blocked preparation receives a new attempt identity and a refreshed candidate snapshot before the next offer is considered, preserving current priority order during concurrent configuration changes. Candidate checks per pass are capped at 64; reaching the bound is recorded as an admission reason.

Once dispatch is authorized, transport or decode failures use `ProofNone` through the recovery ledger path and stop the operation; the executor never automatically resends an uncertain request. When candidates are exhausted before a send, bounded, deduplicated reasons are passed to `CompleteWithoutAttempt`. The host now catches task panics without logging panic values, releases local capacity, and leaves durable outcome resolution to the supervisor's recovery path.

## Verification

Focused race tests:

```text
cd server && go test -race ./internal/application ./internal/runtime -run 'CandidateOperationExecutor|QuoteFits|ExecutionHostContainsTaskPanic' -count=1
exit 0
```

Full repository verification:

```text
make check test-contract test-integration test-web
exit 0
```

The full run passed Go internal race tests, SQLite integration race tests, and Web tests (7 files / 244 tests). A final `make check` and focused race run passed after the last logging and reason-classification edits.

The integration suite also includes `TestHTTPSubmitRunsSQLiteLedgerAndControlledTLSEndToEnd`: it sends an authenticated HTTP submit through the real Router and ExecutionHost, uses real SQLite claim/prepare/permit/output/settlement transactions, verifies the permit is consumed when the local TLS server receives the single request, and checks the durable success result, stored output, and terminal event. The planner and adapter in this test are explicit fixtures; this proves the internal path, not a vendor integration.

## Source hashes

```text
2d3b238a9c55b5dc7701232d4b4854bb9a4187d9989015479678f4fd87665e9a  server/internal/application/operation_executor.go
0a21842a9212f158a3091a4b7efd192bb257151735319d11243a29ecf2eabafb  server/internal/application/operation_executor_test.go
9908900d62702f6d6275a2b55cbfb1af5bb81699da79795eae0439f43a2b2d58  server/internal/runtime/host.go
cca12d631ac3b4790a2cfe7b167e9785b93d04a91eb1f84ff830d111c3309e00  server/internal/runtime/host_test.go
562f5752dc807af818b865ca06d3f3dd0436e01ed4a1c0e6c78280c81942ba8a  server/cmd/ant-server/main.go
c110c00622fce280c8936464e5838af6e2fe9067143667c32f51f6aeccaabe79  server/tests/integration/executor_test.go
321c57c8e77fa60f4fa3dba5fb7ac19115fdae4e9ff9fdeeb46b800c6d3a0557  server/tests/integration/prepare_test.go
dbd916c3f84e2cdeae734d639c9054d79dd7a1de57e7705be79b6fc8bfb71841  docs/design/05-RUNTIME-AND-ADAPTERS.md
```

## Remaining integration work

The coordinator is not instantiated in `ant-server`, and the operation submit route is not mounted. No production domain policy or vendor adapter is registered, so this code does not send requests or prove an end-to-end user journey. Those require a verified workload contract and explicit provider integration. The existing localtest catalog is not a production provider.
