# V0.7 Design Attachment Validation Record

Date: 2026-10-02. This round only modified requirements/design/plans and design attachments; there was no running server, Web, provider outbound calls, or performance acceptance.

## Go Contract Compilation

```sh
gofmt -w docs/design/contracts.go
go test docs/design/contracts.go
go vet docs/design/contracts.go
```

The actual toolchain was go1.26.0 darwin/arm64; compilation and vet both exited 0, with compile output `[no test files]`. This only proves that the attachment types and standard library references compile; no core algorithm tests were run; the target Go 1.27 toolchain and the real driver are locked in and verified at T1. There is no new claim that a Node business backend or TypeScript backend compiles; the old record is kept in the V0.6 archive.

## SQLite Empty-Database Migration

```sh
python3 docs/design/validate_schema.py
```

Actual SQLite 3.54.0: 34 tables, 22 checks passed, exit 0. It covers the original 14 ledger structure constraints and adds pool member foreign keys/uniqueness, priority range, pool version, operation and pool namespace, persistent event sequence/non-deletable, and pool modification rollback with no partial ordering. The data is only explicitly synthesized fixtures, not provider initial capacity.

Still unverified: the real Go modernc driver, queue/Context/HTTP hidden-replay guards, configuration activation, real UI/API chains, Go race/leak/performance, provider-enforced upper bounds, and external termination. SQL does not automatically prove the complete bundle, the priorities ordering algorithm, or all of I01–I10; evidence must be obtained per P1.2.

## Documentation Consistency Check

Local-link/code-fence checks passed for the 26 current Markdown files; the 24 R items, 29 A items, 7 N items, and 10 I items all have definitions and acceptance matrix mappings; all 29 work items in STATUS have task cards. All existing archived SHA256 manifests verified, including this round's V0.6 27-file snapshot. No valid execution clause was found still directing adoption of the old Node backend, TS backend contracts, or excluding the graphical console.
