# Menu bar panel — recovery and clarity

Date: 2026-10-10. Scope: the existing native Ant panel and its cached `/api/quota`
view; no page navigation or account data model changed.

## Changes

- A zero-time startup response is treated as loading, not as a completed empty
  list. Existing cached rows stay visible until a completed server read arrives.
- Completed reads replace the panel cache atomically at
  `~/Library/Caches/local.ant.app/view.json` (demo uses `view-demo.json`); the
  directory is enforced at mode `0700` and the file at `0600`. The startup
  placeholder is never written over the last good result.
- HTTP, decoding, and cache-write failures are visible. The panel offers
  “Try again” and keeps already loaded rows on screen.
- The semantic colors now have explicit light and dark values based on Ant's
  existing route teal and review amber. Section labels and meter tracks are
  easier to scan in both appearances.
- Removed the header's second “Open Ant” control; the footer remains the one
  place to open the full app.

## Evidence

- `sh scripts/build-app.sh` — passed; produced `bin/Ant.app`.
- Demo-rendered previews (23 sample accounts; no real account reads):
  [light](light.png), [dark](dark.png).
- Read-only check of the local server returned 10 accounts. The screenshot
  had nonzero “use first” and “need you” counts but no rows, isolating a native
  layout issue that offscreen picture mode cannot catch because it omits
  scrolling. A ScrollView minimum height did not constrain the MenuBarExtra
  window; the live panel now reserves a 420 pt viewport for its scroll area.
- In the earlier iteration, the bundle was installed to `~/Applications/Ant.app`
  and relaunched. `/api/quota` still returned 10 accounts and
  `/api/route?model=glm-5.3` returned two choices, but the user's next menu
  screenshot still had no rows.
- The screenshot showed one Open Ant action in the footer and a refresh arrow
  at upper right. The footer remains the single Open Ant action; refresh stays.
- Rebuilt after the viewport fix with `sh scripts/build-app.sh` — passed and
  produced `bin/Ant.app`; copied it to `~/Applications/Ant.app`, where the app
  signature verifies and both executable hashes match. The already-running
  process still has its old code loaded; the viewport fix takes effect on the
  next app launch. No post-fix native screenshot is available, so the live
  popover layout remains unverified. The Mac is locked; no UI workaround was
  used.
- No native menu UI test was run; the live popover remains unverified.
