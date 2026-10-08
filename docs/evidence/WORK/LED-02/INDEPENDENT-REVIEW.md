# LED-02 independent review

**Decision: PASS for the constrained G1-DEV local synthetic slice only.** Review completed 2026-10-03 by `独立复核者`, who did not implement the candidate. Review was read-only. This decision does not accept real configuration projection, Transport/provider behavior, LED-03/04, or full T2 integration.

## Findings closed

The initial review found four blocking defects, all corrected before final acceptance:

1. Authorization now rechecks fresh provider observations against total outstanding reservations and rejects expired resources before restoring/moving reservations.
2. Preparation, authorization, and permit consumption obtain time from the injected authoritative clock inside the serialized writer callback. Production code cannot rewind the clock from caller arguments.
3. Private permit handles are synchronized inside writer callbacks, including when a waiting caller is cancelled.
4. Admission-blocked outcomes persist bounded rejected decision records and replayable `blocked` replies.

Final read-only source review confirmed these fixes and stable hashes. Unknown canonical window state remains fail-closed. The revision-equality convention is only a synthetic G1-DEV fixture constraint; real integration must define the authoritative revision mapping.

## Verification and residual test limitation

The independent reviewer ran a race test once with exit 1, then twice with exit 0. The first run's detailed output was truncated and not retained, so its failure cause cannot be diagnosed. Do not attribute a cause or treat later success as an explanation. To check reproducibility, the integrator preserved a complete verbose outbound-denied pass and ran five additional fresh outbound-denied race passes; all five passed, with each log at `/tmp/ant-led02-race-repeat-N.log` and the verbose output at `/tmp/ant-led02-final-race.log`. The full internal race suite and root `make check` also passed.

Command used for the focused independent/reproduction check:

```sh
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' \
  env GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly \
  go test -race -count=1 -timeout 300s ./tests/integration -run '^(TestPreparation|TestClaim)'
```

## Reviewed source hashes

| File | SHA-256 |
|---|---|
| `server/internal/store/prepare.go` | `986ee63136155d76977e8844134ea744311c274e1ea5f136af07d3a65f1119a6` |
| `server/internal/store/sqlite/prepare_statements.go` | `939c8cc02684011dd89d33ffc2f18d13a90aa7a8cbb8e519041b1952e6d99aec` |
| `server/internal/ledger/commands/prepare.go` | `86d6f4f89c1f623f3c9044439461e9dfe228db671efdbfa749ca82e1449b9096` |
| `server/internal/ledger/commands/authorize.go` | `16f90cb8145053a002cf8b6ad145fb8ab025bbded1e8465d0868bc07046c88fb` |
| `server/internal/ledger/validate_change.go` | `6d3ccfac3ec3df9ec7e680b8d0048c27fdd8f5c004012a61c98013952cd6e176` |
| `server/tests/integration/prepare_test.go` | `c6dd9fc20ca6124928e9e50af0a53016fa78e64141fe12b60e8b2fb49fff03ac` |

## Boundaries

This PASS covers a local SQLite transaction slice using synthetic identities, resources, evidence, and the `localtest` provider. It does not cover live compiled configuration provenance, HTTP/API wiring, real credentials, network egress, supplier calls, performance acceptance, terminal settlement, or recovery/cleanup. Those remain integration and dependent-leaf obligations.
