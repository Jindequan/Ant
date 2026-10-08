# CFG-03 / D4 scope metadata

2026-10-03. Finite local/test catalog fix; no T2 behavior or external calls.

Changed `server/internal/providers/localtest/catalog.go` and added `scope_metadata_test.go` in that directory. Child facts inherit empty scope/provider IDs from their VerifiedScope. Explicit mismatches reject construction. Missing parent IDs, unregistered provider, duplicate scope ID and unsupported scope kind reject construction rather than returning unresolved identity. Private `newWithData` enables immutable snapshot tests without editing embedded globals.

§4.8 quick gate: existing configuration compile scope metadata contract only; no page/IA/schema changes. §1.6 boundary retained. §3.8 contract checks and §4.1 developer/attacker boundaries: exact account and shared scope/provider output, account and binding aliases, foreign provider misses, explicit matching identities, contradictory children and malformed parent metadata tested. Shared aliases return shared billing identity rather than account/binding IDs.

§1.3 evidence: `go-scope-metadata-race.log`: `cd server && go test -race -count=1 -run TestScopeMetadata -v ./internal/providers/localtest`, exit 0. Go runtime: go1.27.1 darwin/arm64. Model identification was not exposed to this 独立审查者.

`go-localtest-race.log` preserves initial whole-package run, exit 1: metadata tests passed; descriptor byte parity failed while the integration owner was editing descriptor copies. This 独立审查者 did not edit descriptor/scopes JSON. Final package rerun after the owner synchronized descriptor copies: `go-localtest-final-race.log`, `cd server && go test -race -count=1 -v ./internal/providers/localtest`, exit 0, including descriptor/snapshot parity. Full Compile output is assigned to root-owned integration tests (internal-package test import would cycle through config).

§4.6 unresolved: full Compile output and overall final-tree checks belong to integration owner; Web mirror, UI journey, persistent activation, ledger, live supplier verification, deployment and T1 release are not claimed by this package. Code file ownership is released to integration owner after delivery.
