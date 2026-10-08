# Pool operation submit route mount

Date: 2026-10-05

## Change

`api.PoolsHandler` now accepts an optional `OperationSubmitCommands` implementation and mounts `POST /v1/pools/{poolId}/operations` beside pool read and order routes. The standalone `OperationSubmitHandler` remains the single owner of permission checks, strict body decoding, path/body pool binding, response validation, and HTTP status mapping. Existing callers that do not provide a submitter keep the same route set.

This is the HTTP composition seam only. `cmd/ant-server` does not yet provide a production submitter; this evidence does not claim that the running binary accepts provider operations.

## Verification

`TestPoolsHandlerMountsOperationSubmitAtPoolRoute` sends an authenticated HTTP request to the mounted pool path and asserts the route reaches the submitter with the pool ID bound from the path and the namespace from authenticated context. Existing `TestOperationSubmitRouteBindsPoolAndRequiresExecute` covers path/body mismatch and execute permission.

Focused verification passed: `go test -race ./internal/api ./cmd/ant-server ./internal/transport`. Full verification passed: `make check test-contract test-integration`, including dependency boundaries, generated Web type drift checks, Go vet/build, all internal race tests, and SQLite integration race tests.
