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

## Step 2: reconciler and Account → Inventory (2026-10-08)

**What changed.** A good read now carries an `Inventory`; the engine records it, the run's end and
the credential's status in one transaction (`supply/reconcile.go`). Accounts are stored under the
provider-reported identity, or under `credential:<id>` when there is none, so credentials merge
only on what the provider said. Packages, resources and physical keys get stable ids (a re-read
updates rows); a reading is appended only when its value changes; a reading not newer than the
stored one is skipped and noted on an `ok` run; if recording fails, the run is still closed as an
`error` saying why, and stored facts are untouched. Declared packages never get readings (§3.6).

`quotasource/inventory.go` converts quota's `Account`: one package per mode (plus one per group),
`observed` when the reader read the provider, `declared` when the card came from the mode's rule.
Windows and balances become meters at one fixed precision (10⁻⁶, part of identity); balance
strings are converted exactly, never through floating point; negative balances are kept.

**Honest limits of adapter facts.** Quota reports windows by name, not as `domain.WindowRule`, so
adapter meters have window kind `unmodeled` and semantics `quota.adapter.*`. They are history and
display data; phase 5's fact source will expose only native (phase 4) templates, never these.

**Identity finding.** Two shipped quota identities are not provider-reported: Z.ai's is inferred
from coincident reset times, Claude's is the constant `claude-plan`. Design §2.6 forbids merging on
inference, so the store does not use them (only Bailian/Alibaba Cloud and Copilot identities are
trusted). The page still groups those cards as before; that is display, not a stored fact.

**Plan change.** `mergeSame`/`hideFailedWays` stay as page presentation until the page reads supply
projections (with the `/v1` session, step 4); the store-side merge is the reconciler's.

**Tests.** `supply/reconcile_test.go`: merge only on reported identity (3 credentials → 2
accounts, shared meter, no duplicate reading); append-on-change and never-read-declared; same-ms
skip noted on an ok run; a failed recording closes its run as an error with stored facts intact;
physical key ignores namespace and Ant ids. `quotasource/inventory_test.go`: exact decimals
(incl. rounding and rejects), identity allowlist, window/percent/model-scope/group conversion,
rule-only → declared, negative balance. The end-to-end test now checks the stored DeepSeek reading
(23.40 CNY = 23 400 000 units, granted/topped-up in evidence, credential-scoped account).

**Gates.** `make check`, `test-contract`, `test-integration`, `test-fault` pass.

## Step 3: per-site browser opt-in, ruling R6 (2026-10-08)

**Rule.** Ant opens no everyday-browser cookie store (Chromium family, Firefox, Safari) and asks
the Keychain for nothing for a site the user hasn't allowed. The gate is at the single choke point
(`findBrowserSession`/`findBrowserCookie`); `Local.Browser` is nil-means-none, and the server wires
it to the user's list. Ant's own sign-in window is not gated: the user signed in there for Ant.

**On the page.** A site's "Your browser" way (Kling, Leonardo, SeaArt, Stable Diffusion Online,
Manus, Perplexity, MiMo, Suno) is the consent: one sentence naming the site and the browsers, and
**Allow and read**. Removing such an account takes the permission back instead of hiding it, so
allowing again brings it back. The server resolves the site from provider/mode/way; the page
cannot name an arbitrary domain.

**Where the permission lives.** With the page's other choices in `accounts.json`
(`browserSites`) until the import step moves those choices to the database; design 11 §8 R6 said
`supply_credentials`, which cannot hold a permission for a site whose credential hasn't been found
yet (finding it is what the permission allows).

**Not covered.** Alibaba Cloud's sign-in way also used to pick up an everyday-browser aliyun.com
session; it is now gated too and has no allow button, because its own sign-in (Bailian's console
login) works without reading the browser.

**Tests.** `TestBrowserIsNotOpenedWithoutPermission` (no store opened, no Keychain call, no browser
account found), `TestAllowingAndStoppingABrowserSite` (refuses a non-browser way; allowing finds
the account; removing revokes and doesn't hide). Existing browser tests run with every site
allowed. Page checked in demo mode: Add account → Kling AI → Your browser shows the permission
step; Allow and read saves `klingai.com` and closes.
