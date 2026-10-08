# Core extension verification

Date: 2026-10-07 (Asia/Singapore). Executor/reviewer: Codex. Baseline:
PRD R02/R05/R07/R18, DESIGN I01–I10 and the scoped
[core extension increment](../../../implementation/CORE-EXTENSIONS.md).
This is backend increment evidence; the existing real-account, phase and
release gates are not closed. No Git repository exists in this workspace.

## Changes and purpose

- **§1.1/§1.2, R18:** design and business scope precede implementation. Added
  the neutral `planning.ProviderPolicy` port and immutable startup registry;
  the running server consumes its catalog, policy list and adapter lookup.
  Tests reject missing/foreign components, duplicate registrations, revision
  and schema drift, typed nils and mutable metadata. Catalog-only integrations
  retain their schemas but cannot compile executable resource/profile configs.
- **§3.7/§3.8, R02/R05/R20:** planning validates complete, exactly-once resource
  coverage, accounting modes/roles, bound identity and evidence age.
  Preparation accepts demand below a verified ceiling and rejects excessive
  demand or stale mapping; SQLite assertions prove rejection creates no
  reservations or permits. Current authorization and single-use sending remain
  intact. Router validation uses the shared protocol contract, with model and
  provider restrictions at policy/encoding boundaries.
- **§1.4/§3.7, R07/R19:** exact rational metering rounds once per output,
  validates units and samples, preserves conservative basis and emits exact
  decimal overflow reports. DeepSeek's existing frozen mapping uses this
  engine for both request ceilings and reported usage. Real SQLite tests prove
  oversized usage becomes unknown plus a resource hold, survives persistent
  readback and replays without duplicate accounting. One counter settles into
  minute and daily quota windows; replay does not consume twice.
- **§4.1/§4.4:** existing internal/SQLite/fault suites exercise identity,
  authorization, cancellation, single-send, recovery and conservative unknown
  occupancy. New calculator and registry reads run under race detection.
  No page, visual token, navigation, wire schema or SQL migration was changed;
  frontend screenshots and visual acceptance do not apply to this increment.

## Evidence and reproduction

Environment: macOS/arm64, Go 1.27.1, the existing locked modernc SQLite driver,
real temporary SQLite databases and the suite's configured transactional/WAL
settings. Controlled network fixtures are explicitly synthetic. No provider
credentials or paid provider calls are involved. No new dependency is added.

Run from `/Users/devin/code/Ant`:

| Command | Actual result | Raw evidence |
|---|---|---|
| `make check` | Exit 0; current generated runbooks, gofmt/vet/build, dependency graph (27 packages), Web typecheck and generated contract guard | [check.log](check.log) |
| `make test-contract` | Exit 0; all internal packages, fresh race run | [contract.log](contract.log) |
| `make test-integration` | Exit 0; real SQLite integration, fresh race run (17.680s package time) | [integration.log](integration.log) |
| `make test-fault` | Exit 0; storage/configuration faults, reorder/authorization rollback, observation/adjustment/settlement mapping guards | [fault.log](fault.log) |
| `cd server && go test -race -count=1 ./cmd/ant-server` | Exit 0; startup provider assembly and server tests | [composition.log](composition.log) |
| `cd server && go test ./internal/domain/metering -run '^$' -fuzz FuzzRationalMetering -fuzztime=10s` | Exit 0; 2,108,513 executions against an independent integer oracle; this is sampling, not exhaustive proof | [fuzz.log](fuzz.log) |

The numerical fuzz run was followed by final full internal race and SQLite
checks. Two existing command-budget tests timed out during the first
integration/fault checks while fuzz was running concurrently; final sequential runs
passed with unchanged budgets. Preserve the failed evidence:
[integration-concurrent.log](integration-concurrent.log),
[fault-concurrent.log](fault-concurrent.log).
The first static check found stale generated stage runbooks after STATUS was
updated; regenerating their source-digest projections resolved it:
[check-initial.log](check-initial.log). These initial failures are not passes.

The final source manifest covers 339 consumed files, including all new code
and tests. [source-manifest.json](source-manifest.json) and
[source-manifest.sha256](source-manifest.sha256) identify the exact tree. This
is a new snapshot; the historical signed T1 manifest has not been re-signed.

Key executable counterexamples: `candidate_coverage_test.go`,
`provider_registry_test.go`, `catalog/snapshot_test.go`,
`metering/metering_test.go`, `core_bounds_test.go`,
`TestCalculatedOutOfRangeUsageSurvivesSQLiteSettlement`,
`TestOneCalculatedUsageSettlesMinuteAndDayLimits`, and
`TestDecodeOneUsageCounterForMultipleResourceWindows`.

## Known unverified items

Actual provider account admission, current tariff/account reconciliation,
streaming, remaining lifecycle hook boundaries and durable observer outbox,
multi-host storage, frozen production throughput/latency, and go-live remain
unverified. Startup registration cannot verify billing facts or make arbitrary
new resource algorithms executable. Exact calculation does not itself prove
that a provider's selected tariff is correct. Infrastructure has no required
paid service; provider consumption may still incur charges.

## Next step

Use the [extension guide](../../../CORE.md) for the next concrete provider or
model, with actual account and usage evidence. Keep new protocol, model policy
and tariff details at the extension ports; reuse the same ledger, controlled
transport and recovery tests. The backend increment above is verified; the
existing first-version phase status remains unchanged.
