# Supply route order — acceptance record

Date: 2026-10-10 (Asia/Singapore). This is a preview-only feature; no model
request was sent and no paid-use consent was changed.

## Configuration → observation → preview

- `quotasource.DemoSource` supplies the isolated demo's model offers and
  observed Z.ai / Bailian inventory. The real supply engine stores those offers
  and readings in a temporary SQLite database; `/api/route` then computes the
  eligible set from those stored facts.
- In the browser's Models view, `glm-5.3` showed two eligible rows: Z.ai's
  expiring plan and Bailian's free grant. OpenCode Go, OpenRouter and
  SiliconFlow appeared separately as “Also listed”; no unavailable account was
  silently ranked as a route.
- Moving Z.ai down saved revision 1 and moved Bailian above it. A second browser
  page still holding revision 0 attempted a change; it received HTTP 409, showed
  the refresh message, and left the saved list unchanged.
- “Use Ant's default order” submitted an empty offer list at the current
  revision, cleared the preference, restored Z.ai first, and displayed
  “Default order”. Reloading the page kept that default. The API revision still
  advanced, protecting against stale editors.

The browser exercise used a temporary `-demo` server and SQLite file in `/tmp`.
The real local database and provider accounts were not used for mutations.

## Atomic candidate validation

The SQLite writer transaction re-runs route qualification before saving a
non-empty order. Deterministic interleavings change state after the initial
preview but before the write transaction: pause, offer deletion, a new
exhausted reading, and a window expiry. Every case returns
`ErrSupplyCandidatesChanged` and leaves the order revision unchanged. The
supervisor's paused-between-validation-and-commit overlay reproduction passes.

Verification (Go commands run from `server/`):

```text
go test -race -count=1 ./internal/supply ./internal/supply/quotasource ./internal/quota ./internal/store/sqlite ./tests/integration
go test -race -count=1 -overlay /tmp/ant-supervisor-route-race-20261010/overlay.json ./internal/supply -run '^TestSupervisorPauseBetweenValidationAndOrderCommit$'
node --check server/internal/webui/static/app.js
sh scripts/build-app.sh
```

The native status-bar panel remains a separate acceptance item. Browser DOM,
API results, and offscreen renderer images are not evidence that its live menu
displays correctly.

## Save outcome and retry behavior

- The order API now distinguishes `committed`, `not_committed` and `unknown`.
  A committed write with an unavailable follow-up preview returns success with
  the saved revision and a refresh action. Validation, candidate-set and
  revision conflicts say `not_committed`.
- A real SQLite integration fault makes the preflight read pass, commits the
  route order, then fails the next preview read. The handler still reports
  `committed`; a subsequent GET confirms the new order.
- A second integration path commits through SQLite but drops the writer
  receipt. The handler reports `unknown`, and a GET confirms the saved order.
  The web UI's unknown path performs GET-only checks; it never repeats POST.
- The isolated demo Models UI was exercised through a local response-injection
  proxy; all writes landed only in its temporary SQLite database:
  - A failed first route GET showed “Try again”; retry loaded the real preview.
  - A reset POST whose committed response was replaced with `unknown` was
    confirmed by GET at the next revision, with `savedOfferIds: []` while the
    default preview still had eligible offers.
  - A manual-order POST whose response was replaced with `unknown` was
    confirmed from persisted `savedOfferIds` after the proxy paused one offer;
    the live `use` list shrank to one while the saved preference still held
    both IDs, and the UI unlocked the reset action.
  - A reset with both the POST response and immediate GET unavailable kept a
    “Refresh status” action. Switching away from and back to Models rebuilt
    the DOM; opening the model performed a GET, confirmed the reset, and
    restored enabled ordering controls.
  - A later order revision superseding an unknown save was shown as the current
    version with fresh controls; the UI did not claim the earlier request was
    confirmed.
- `go test ./...` from `server/` passed after these changes;
  `node --check server/internal/webui/static/app.js` passed. After the final
  UI reconciliation change, the server resource was rebuilt and installed:
  `go build -o ../bin/Ant.app/Contents/Resources/ant-server ./cmd/ant-server`,
  ad-hoc signed, copied to `~/Applications/Ant.app`, and verified with
  `codesign --verify --deep --strict`. The installed server SHA-256 was
  `40065ebdccfb3cdc32be942ea1352261cd287f8d2434d88ab61d1064d7a8f10d`.
  Actual provider consumption and usage attribution remain separate
  unfinished work; these route changes only affect preview preferences.
