# ENG-03 / design 11 phase 0 — supply inventory retired (2026-10-08)

User ruling R7 (2026-10-08): drop the rejected supply-inventory surface and its data.

Removed: `/v1/inventory` route (`cmd/ant-server/main.go`), `api.InventoryHandler`,
`application.InventoryService`, `internal/domain/supply`, `store.Inventory*` port and
`store/sqlite/inventory.go`, and their tests; OpenAPI `/v1/inventory` path and the
`Supply*`/`Inventory*` schemas (spec and fixture manifest 1.1.21 → 1.1.22).
Added: migration `005_drop_supply_inventory.sql` (one-way). Migration 004 is kept so v3 databases
upgrade through it. Live docs updated (`design/02-STORAGE.md`); `CORE.md` and
`implementation/SUPPLY-INVENTORY.md` carry a retirement banner and remain as history.

Acceptance: `grep -rni inventory server --include='*.go'` matches only migration tests.
Tests: `TestMigrateDropsSupplyInventoryFromVersionFour` (v4 with inventory rows → table gone,
other data kept, 41 tables), `TestMigrateUpgradesVersionThreeThroughInventoryAndDrop`,
`TestMigrateUpgradesVersionOne/TwoDatabase`, `TestLaterMigrationCopiesMatchCanonicalFiles`.
`TestCodexRegressionFutureSchema` used the literal version 5 as "future"; now current + 1, same intent.
Gates on the final tree: `make check`, `test-contract`, `test-integration`, `test-fault` pass.
Local `data/*.db` lose their inventory rows on next start, as ruled.
