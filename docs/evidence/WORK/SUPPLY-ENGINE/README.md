# Design 11 phase 2 — engine shell

## Step 1: discovery and scheduling (2026-10-08)

**What changed.** The quota page no longer runs its own refresh loop. The supply engine
(`internal/supply`) discovers credentials through a `Source`, reads each at its own cadence
(policy §3.4: 5 min; 1 min within 15 min of a reported reset; 30 min for balance-only accounts;
errors back off ×2 to 1 h; signed-out waits 1 h), at most one read per credential and 8 at once,
rediscovering every 5 min, and records every run in `collector_runs` and each credential's status in
`supply_credentials` (namespace `-supply-namespace`, default `local`). `internal/supply/quotasource`
is the phase-2 adapter: it discovers and reads with today's quota providers and serves the page
through `quota.Reader`; the page's cards are the adapter's in-memory latest reads.

**Transitional rule.** Only *found* credentials are recorded. Added ones (secrets in
`accounts.json`) are read and shown but not recorded until the import step, because the schema
accepts an added credential only with an encrypted secret reference.

**Boundaries** (`scripts/check-deps.py`): `supply` may import only `supply`, `domain`, `store`;
`supply/quotasource` adds `quota`, and is the only package joining the two.

**Also in this step.** `cmd/ant-server/main.go` 585 → 485 lines (ENG-05: split when touched):
master-key and backup-path helpers moved to their own files unchanged; supply wiring in
`supply.go`. The composition-root comment no longer claims "no provider call at startup" (R4).

**Tests.**
- `internal/supply`: policy table; found recorded / added not; unknown outcome → clipped `error`;
  one read per credential under concurrent `RunNow`; the in-flight cap; rediscovery keeps
  schedules and drops vanished credentials; the start loop reads each due credential once.
  Run with `-race -count=3`.
- `internal/supply/quotasource`: no secret value reaches the locator; kinds; result mapping;
  end-to-end with the real DeepSeek reader against a fake server: the page shows the balance and
  the store has the credential (no key in its locator) and one `ok` run. Only the DeepSeek
  provider is loaded: the others would look at the machine's real sign-ins, which quota stubs only
  in its own tests.
- `internal/quota`: all existing tests pass through a synchronous test `Reader`.

**Live check.** The built binary, non-demo, with an empty HOME and an empty PATH (so no app
config, browser store or Keychain tool is reachable), against a copy of `data/demo.db` at v4:
upgrade to v6 (versions 1–6, 41 tables, `supply_inventory` gone, other rows kept), page served,
`GET /api/quota` and `POST /api/quota/refresh` answer with zero accounts, no errors logged.

**Gates.** `make check` (32 packages), `test-contract`, `test-integration`, `test-fault` pass.
