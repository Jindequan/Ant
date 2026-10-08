# Review of the design 12 slice (provider modules + route preview) — verdict and findings

Reviewed: commits `1666f9e`…`7731c6e` (six commits on top of `617f407`), by full git archaeology,
three parallel deep-review passes (commit purity / module layer / store+preview+page), fresh gate
runs, and an isolated flake reproduction. Date: 2026-10-08.

**Verdict: accepted.** Every step is one commit with a message that matches its diff; the claims
held under adversarial checking; the reviewer independently reproduced the flake result (10/10
isolated) and re-ran all four gates green on the final tree (`check-deps` 34 packages,
`test-contract` 25 packages, integration, fault). Carry the findings below as a housekeeping batch,
then continue per design 12.

## Verified — do not rework these

- Six commits, clean scopes, no unrelated files; `domain/observed` is a true rename with **zero
  alias shims**; no file over 500 lines.
- The Z.ai parser truly moved (quota's old parsing deleted, no copy); BigModel and Z.ai share it;
  the HTTP-200-with-`code:401` fix lives in the shared parser so both paths report "signed out",
  with the exact real reply kept as a fixture in both test files.
- Both modules have **zero `net/http`** in production code (boundary-enforced); origins are exactly
  the official domains. Adversarial probes on the observation Fetch all block: suffix hosts
  (`api.z.ai.evil.com`), scheme-relative URLs, userinfo, port variants, cross-origin redirects
  carrying keys. Response cap 1 MiB (stricter than the old reader's silent truncation).
- Migration v7: 42-table arithmetic correct; the v6→v7 upgrade test builds a real v6 database from
  the migration files; offer replace-set is transactional with per-offer credential binding
  (tested); `paused` survives upserts by construction (excluded from the UPDATE SET) and is tested.
- Preview engine: all exclusion rules and ordering classes verified at cited lines; `staleAfter` is
  a constant; `Decimal`/`Money` moved with no second implementation; the "mcp" substring guess is
  gone (structured `TIME_LIMIT` field instead).
- `/api/route` and `/api/pause` sit under LocalOnly (peer-loopback enforced) + `X-Ant` + Origin
  checks. Demo mode's no-network is structural: `DemoSource` has no Fetch field; endpoints are
  injected by the composition root (the boundary violation found and fixed mid-slice).
- Page: menu rendered above the page, reopens after the 30 s re-render, closes on view switch;
  Add-flow rule appears once; secrets never enter the DOM.
- Live outbound (two read-only requests with a fake key) is documented in the evidence with the R4
  citation. The models/alias-table deferral is recorded in design 12 §4 with its reason and landing
  condition — a correct refusal of speculative abstraction. The ENG-02 freeze held.
- Flake: honestly documented in the evidence; 10/10 isolated (reviewer-reproduced); gates re-run
  before committing.

## Findings

### M-1 · The observation Fetch inherits the environment proxy (fix now — one line)

`transport/observation.go:43-44` clones `http.DefaultTransport`, keeping `ProxyFromEnvironment`.
The runtime client deliberately sets `Proxy: nil` (`http_client.go:124`). TLS stays end-to-end so
keys are not cryptographically exposed, but `HTTPS_PROXY` can redirect the exit path — inconsistent
with the "keys go only to the declared origin" posture. Set `Proxy: nil` on the cloned transport,
and assert the field in a test.

### M-2 · Fixture parity is three hand-synced copies, not a mechanism

`zai/module_test.go:16-19`, `deepseek/supply_test.go:16`, and `quota/quota_test.go:116-119,244`
hold inline copies of the same fixtures; changing quota's fixture will not fail the module tests.
Make one canonical file (e.g., `internal/providers/zai/testdata/plan.json`, read by both tests via
relative paths — pick the layout, keep it mechanical). Parity by comment is not parity.

### M-3 · The flake has a record but no trackable item

The evidence README documents `TestPreparationConcurrentCapacityAndSingleConsume` (load-sensitive,
passes 10/10 isolated, not touched by this slice) — but STATUS.md carries only "Gates pass", so the
one gate failure + rerun is invisible at the entry document. Add a STATUS/task-tree follow-up item
referencing the evidence section.

### M-4 · The stale flag keys on the wrong timestamp

`preview.go:205-208` ages readings by the credential's `verified_at`; the copy says "reading is X
old". A reading whose value hasn't changed for hours but was re-verified a minute ago is shown
fresh. Key the staleness on the reading's own `observed_at` (you already store it), update the
tests, or reword the copy — data and text must agree.

### Low (fix opportunistically in the same batch)

1. `observation.go:44` — `http.DefaultTransport.(*http.Transport)` without the ok form; construct
   or assert explicitly.
2. No application-layer header validation on the Fetch's headers (`observation.go:72-74`); CRLF is
   caught at the transport layer only. Mirror `HTTPClient.validate` (`http_client.go:210-230`) so
   bad headers fail at construction.
3. `zai/module.go:105-107` — `Models()` sends `Authorization: Bearer ` on an empty key
   (currently unreachable; guard anyway for interface symmetry).
4. `capitalize` (`zai/quota.go:105-111`) and `planName` (`quota/zai.go:139-145`) are the same logic
   twice — consolidate.
5. `originOf` treats an explicit default port (`https://api.z.ai:443`) as a different origin
   (`observation.go:91`) — conservative, but document it or normalize default ports.
6. Card-menu lifecycle: when a filter removes the anchored card, an open menu is neither closed nor
   re-opened in the cards view (`app.js:530-534`, `:500`, `:547`). Close when the anchor
   disappears.
7. `judge` checks `declared` before `exhausted` (`preview.go:191` vs `:194`); design 12 §5 orders
   exhausted first. Align implementation to design (or amend the design note) — it changes the
   displayed reason for mixed accounts.
8. `DemoSource` has no unit tests; the no-network guarantee is structural but unverified. Add
   minimal tests (offers built from endpoint declarations, demo namespace, no Fetch field).
9. `TestMigrateFreshDatabaseCreates41Tables` (`migrate_test.go:18`) is named 41 and asserts 42 —
   rename.
10. Notes, no action required now: quota's own network path still runs on `quota.Local` under the
    ENG-01 exemption (parsers shared; transport moves when design 11 phase 4 retires quota). The
    "demo cannot import provider packages" constraint is enforced implicitly via the quotasource
    allowlist — an explicit comment line in `check-deps.py` would make it legible.

## Direction for the next batch

1. **Housekeeping commit(s) first**: M-1…M-4 plus the lows — small steps, one commit per step, four
   gates green per step.
2. **Then continue design 12**: port the next provider(s) as modules. The third module is the
   acceptance test for the real goal — a provider added without touching core routing or storage
   (the MAGPIE-AND-ANT stage-5 criterion). Which provider(s) to port next is the user's call; the
   two candidates to compare support for are Aliyun (Bailian) and Kimi/Moonshot.
3. Consumption (Magpie stage 4) stays gated on IN-01…04; the route preview is the current
   product surface and must remain read-only.
4. Standing guards unchanged: no wizard revival, no billable/execution egress, no frozen-decision
   amendments in drafts, no speculative abstractions (the model-table deferral is the model to
   follow), step-wise commits with auditable history.
