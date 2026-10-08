# LED-03 final implementation report (continuation report)

State: **verifying — implementation complete, independent acceptance pending.** The implementation findings and registered review areas are recorded below. This is implementation evidence only; it is not acceptance. The leaf stays `verifying` until independent acceptance.

## Final source hashes (SHA-256)

| File | SHA-256 |
|---|---|
| `server/internal/store/settle.go` | `111108f4dc31063607a2f9fa9de3b68da1f6dc3c5308a7a1ac695f878b0ef6f1` |
| `server/internal/store/sqlite/settle_statements.go` | `0281502e8b15626bb5548de7387feaf80ee346ec7315182de97d820bff62b3cc` |
| `server/internal/ledger/commands/settle.go` | `8f4389c7a3ee5a6f0f1c0983cd9cd0cda83793c6c25ed69143dbb3e7bb97ba66` |
| `server/internal/ledger/commands/settle_test.go` | `c8f30c13803d511ae8b6f894ba9d04edb796ed23d8dc74e3ca696e3754e941c1` |

`store/settle.go` is byte-identical to the frozen candidate baseline; the SettlementWriterTx surface was already the narrow typed extension and did not change.

## Production changes on top of the frozen candidate

1. **Rolling-window `charged_at` attribution** (`commands/settle.go`). The frozen draft called `resources.WindowAt` for any reported `charged_at` and returned its error — but rolling rules always return `ErrRollingNoBounds`, so a rolling counter whose report carried a charge instant aborted the whole settlement, while the same report *without* `charged_at` settled conservatively. Attribution is now temporal for rolling rules: the charge is attributed to the reserved bucket only inside the current rolling interval (`charged_at > now − duration`, the same cutoff preparation availability uses); otherwise it is unattributed and isolates. Zero/negative duration never attributes (fail closed).
2. **Independent settlement-facts identity check** (`sqlite/settle_statements.go`, LED-03 review item 3). `GetSettlementFacts` compared only resource ID/kind. It now compares every persisted `resources` column (owner_kind, owner_id, metric_id, unit, quantum_exp, kind, semantics_ref, semantics_revision) against the certified `definition_json` and the pinned reservation — mirroring what `preparationBundle` enforces at prepare time — and additionally requires the semantics revision to equal the plan's window rule revision and the resource owner to match the plan's account/binding.
3. **Credential holds gate continuation** (`sqlite/settle_statements.go`, LED-03 review item 1). The `Blocked` computation omitted the `credential` scope, so a credential hold blocked new preparations but not continuation of an executing attempt. The binding's current `credential_ref` is now a hold scope for continuation, matching preparation admission.
4. **Mapping/metric verification before accepting facts** (`commands/settle.go`, LED-03 review item 3). Each requirement's bound must be certified under the plan's `ReportMappingRevision`, and each allocated resource must still carry its requirement's metric identity (this also closes a hole where a drifted definition would have settled against a zero-value metric report). Drift is persisted-identity inconsistency: fail closed with no side effects (`ErrClaimIntegrity`); reconciliation owns recovery. The equality convention between the two revisions is the synthetic G1-DEV fixture convention (same caveat as LED-02); real integration must define the authoritative revision mapping.

## Registered review items closed (test evidence in `settle_test.go`)

1. **Continuation gating** — `TestSettlementContinuationGatedByHoldsAndPolicy` (18 cases): account/binding/credential/resource/domain holds and open encumbrances refuse continuation (expired `until_at` holds do not), and the trusted retry allowlist requires `AllowContinue` + non-nil `RetryAllowed`, proved-safe externally-ended known-failure work with no cancellation, deadline, attempt budget, visible/stored output, or isolation hold. Every case also proves dispatched facts still settle (counter committed, attempt terminal); a held continuation resolves to `failed` via the shared result resolver.
2. **Window attribution / isolation / multi-bucket / overrun** — `TestSettlementWindowChargedAtAttribution` (fixed inside attributed; fixed outside → window-attribution encumbrance + hold, charge conserved; rolling inside attributed; rolling at/before cutoff isolated) and `TestSettlementWaterfallOrderAndOverrun` (provider order beats stored plan order for bucket attribution; overrun on the last bucket recorded in full and held).
3. **Identity/mapping integrity** — `TestSettlementIdentityAndMappingIntegrity` (7 tamper cases: column drift, definition drift, kind drift, owner drift, semantics drift, plan mapping drift, consistent metric drift) all fail closed with zero durable side effects.
4. **Ownership/race/CAS/lease** — `TestSettlementOwnerGenerationAndTerminalEvidence` (foreign operation key forbidden; wrong owner/generation retain facts and append reconciliation evidence exactly once; terminal-without-settle-receipt never re-applies), `TestSettlementConflictingReportsRace` (40 goroutines, two conflicting reports: exactly one applied — 20 success/20 conflict, one receipt, two resource events, one reconciliation), `TestSettlementCASFailureInjection` (operations/attempts/reservations CAS updates applying zero rows roll back the whole settlement, then a retry succeeds), `TestSettlementQueuedLeaseExpiry` (a settlement queued behind another write command evaluates the lease on the writer clock at execution time; an expired lease is late evidence — nothing charged, no slot released).
5. **Output availability** — `TestSettlementOutputAvailabilityFromChunksOnly` (`stored` only from pre-existing `output_chunks` rows, `unavailable` otherwise; settlement writes no output rows and never fabricates an `OutputRef`). Stored output also blocks continuation (gating table).
6. **LED-04 boundary preserved** — no abort, uncertain-release, sweep, permit-registry cleanup, or lease-recovery code or tests were added; uncertain occupancy stays consumed behind an open occupancy encumbrance until LED-04 owns release.

## Verification against final hashes (all exit 0)

All Go runs executed under `sandbox-exec … (deny network-outbound)` with `GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly`, synthetic local SQLite only; no secrets, no provider calls, no schema changes, no new SQL outside `store/sqlite`, no frozen Store/WriterTx/ReaderTx signature changes, no generic SQL entry point.

| Check | Log |
|---|---|
| Focused settlement race, `-race -count=1 -v` (8 top-level tests, 39 with subtests) | `final-race.log` |
| Full internal race suite `./internal/...` | `internal-race.log` |
| Integration race suite `./tests/integration/...` (claim/prepare regression) | `integration-race.log` |
| Five fresh focused outbound-denied race repeats (reproducibility, LED-02 bar) | `race-repeat-1..5.log` |
| Root `make check` (gofmt, `go vet ./...`, `go build ./...`, `check-deps` 19 packages, web typecheck, guard:gen) | `make-check.log` |

`internal-baseline-race.log` is the frozen-candidate verification log, kept verbatim as historical evidence.

## Deviations and notes for the independent reviewer

- `settle_test.go` is 686 lines, above the 500-line module guideline. All mandated coverage must live in the registered write scope, which names exactly this one test file; splitting into a second test file would have exceeded the approved scope and forced a stop. The deviation is deliberate and confined to the test file.
- The terminal-without-receipt case injects state by demoting the settle receipt's `kind` (`command_receipts` is FK-referenced by `resource_events` and append-only triggers prevent deletion); this exercises the same missing-settle-receipt conflict branch.
- `TestSettlementQueuedLeaseExpiry` uses a 100 ms sleep to let the settlement queue behind a gating write command. An enqueue race would fail the test loudly (settlement would succeed before the clock advances), never silently pass.
- Acceptance of this leaf covers only the synthetic local SQLite settlement slice. Real config projection, Transport/provider behavior, LED-04 recovery, and full T2 integration remain open per the task tree.
