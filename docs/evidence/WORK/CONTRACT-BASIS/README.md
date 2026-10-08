# K6 consumption-basis contract alignment

Date: 2026-10-04. Scope: correct a discovered mismatch between ledger settlement output and the versioned API contract. This does not change charging arithmetic, provider calls, the LED-03 over-int64 report design, or task gates.

## Change

- `server/internal/ledger/commands/settle.go` emitted `actual`, which was not a legal API enum. Measured consumption now emits `provider_reported`; bounded/absent results retain their existing meanings.
- Settlement and abort occupancy resources already emit `not_applicable`; the OpenAPI `ResourceOutcome.consumptionBasis` enum now declares it.
- Domain and design contract use a typed `ConsumptionBasis`. A contract regression compares the exact Go enum set with OpenAPI and validates each domain value against the schema. Normal settlement also asserts measured and occupancy values.
- OpenAPI version advances from `1.1.0-t1-rework` to `1.1.1-t1-rework`; generated Web API types are refreshed.

## Verification

| Command | Result | Evidence |
|---|---|---|
| `make test-contract test-integration` | PASS; all internal and integration Go race suites | `go-race.log` (SHA-256 `65c0c2959a413b005237a9344519dc02f47e80ea2643ce6d2ec2c783b86df9e1`) |
| `make check` | PASS; runbooks, gofmt, vet, build, dependencies, Web typecheck and generated schema | `make-check.log` (SHA-256 `1efa1d09cdaf90c7d2861e14308421b1ad46db0f4a2a0a070a04c2434a9804c5`) |
| `make test-web` | PASS; 6 files, 235 tests | `test-web.log` (SHA-256 `2a5fbef92fd1ca481c134fcc10f6e05ff1a74630f38fffe0fa6c5bb79860b710`) |
| `python3 docs/plans/check_tree.py` | PASS; 55 tasks, 8 gates, valid DAG | `task-tree.log` (SHA-256 `b4d767febe49fc7594381c6c7132a4bdf8a3748055f8a7507722f435ac7bd6a1`) |

The leaf-source reconciliation was rerun after these edits. It intentionally exits 1 because historical accepted hashes differ and the historical aggregate manifest omits two leaf sources. It reports 2/2 sources in the separate LED-04 current-tree review matching their current digests; see `../G1-DEV/source-reconciliation.json` (SHA-256 `772d9d59c978ebb0083c68bbbaf82d4c577dc9343afb2a4e6e3430e7d558b9c8`) and `../LED-04/CURRENT-TREE-REVIEW-2026-10-04.md`. The original acceptance records and aggregate manifest were not rewritten.

## Source snapshot

| File | SHA-256 |
|---|---|
| `server/internal/domain/types.go` | `fd14651f8a91a88708c090f4cc6fbbe999a1fe62e069d11e50174ce5d750c4ef` |
| `server/internal/ledger/commands/abort.go` | `3f18e3fc57aa98f69ac73a4d7242ce8e93f69df61575d8c75b72a487741bb76a` |
| `server/internal/ledger/commands/settle.go` | `aab41c412c28b598757d072c2a9ba0ee17e482d5202436508fb0fe64b725df2f` |
| `server/internal/ledger/commands/settle_test.go` | `02f6a79096a729a88509a52a9f7c5d4aca0643aeb69914e558a015cb2b0fd4de` |
| `server/internal/contracttest/contract_test.go` | `d7c878fd806131c6fbe0a4a488c406fbd95bbb96631a46e7dc430610517b20d7` |
| `contracts/fixtures/api/manifest.json` | `5d6cda09f84cef335bc80c9ae58fdcc3537e48c0c86ad9a7471a2f7795b51a0b` |
| `web/tests/contract.test.ts` | `62a37c9966f799acc932a7a3d029252db06af2fe2075a87dd4f4f78b7d8310e4` |
| `web/src/api/types.ts` | `ca098c209f71e1cb977c1457caba62ff3281b6a10823eefcfb2f1cbceae1dd92` |
| `docs/design/contracts.go` | `eeec422fcc7db303d2bb602317595213c16801f180b2b1ac60d2b3802bf3e5da` |
| `contracts/openapi.yaml` | `289b9df2173b94a5bdaaaec081e5de90db32dbaceafe4934d51fabadabc8ce63` |
| `web/src/api/schema.d.ts` | `76bc498a6ad0bab6266ad95ef4a3a13df565663baf067cda937a733aecad5081` |
