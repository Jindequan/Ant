# One view, kept

Version: V1.0, 2026-10-09. Two asks: the panel and page should show what
Ant knew at once and then fetch what's new, like any app; and accounts,
providers, resources and quota should be built from parts that are written
once and reused.

## What the code showed

| | Finding | Evidence |
|---|---|---|
| 🔴 | **Nothing is kept between starts.** A card (the page's view of an account) lives in memory only, so every start shows an empty list until each account is read again (slow sign-ins: up to 20 s each) | `quotasource.Source.latest`: "accounts are presentation and are not persisted" |
| 🔴 | **One rule, four copies.** "Use it before it's lost" is written in the router preview (Go), notifications (Go), the page (JS) and the panel (Swift) | `supply/preview.go` `loseWithin`, `quota/notify.go` `loseLeft`, `app.js` `urgency`, `AntApp.swift` `urgent` |
| 🟠 | **The copies already disagree.** The panel's prepaid total leaves out group balances the page counts; the panel can't tell what's left from `used`/`limit`; the page lists a used-up grant as "use first" | `Model.prepaid` vs `renderSummary`; Swift `percentLeft`; `urgency` has no `left > 0` |
| 🟠 | **Percent left, three times** (Go notify, JS, Swift) plus the router's own over meters | as above |

## Decisions

1. **One rule, in the domain.** `internal/domain/usefirst` holds what will be
   lost unless used: a window resetting within a day with at least 30% left,
   or a grant ending within 14 days with anything left, unless the recent
   pace uses it up first. The router asks the stricter question ("lost
   today?"), from the same constants. Notifications use the rule and keep their
   own timing (tell at the last useful moment).
2. **The server says what it means; clients draw it.** `Snapshot.glance`
   (computed once per read, in `quota/view.go`) carries the use-first list,
   what needs you, money held by currency, and counts. Each window carries
   `percentLeft` and `useBy`. The page and the panel render these and keep no
   rules of their own; their copies are deleted.
3. **The dataset is kept.** A card is saved with its reading, in the same
   transaction (`supply_cards`, migration 008), restored on start and
   removed with its credential. `/api/quota` answers at once with the last
   cards, each with its `readAt`, while Ant reads again.
4. **The panel shows what it has, then what's new.** It keeps the last
   answer on disk and draws it at launch, before the server answers; opening
   the panel shows it and fetches new data in the background.
   Cache replacement is atomic and only a completed read may replace the
   saved answer. A zero-time startup response is not an empty result; a
   failed refresh keeps the last rows visible and offers a retry.
5. **One Open Ant action.** The panel keeps this action in its footer; the
   header is reserved for status and refresh.

## Not now

- Readers into modules (design 14's first gap): that is where the readers'
  own helpers (fetch, JSON, window building) become one set, so doing it
  twice would be waste.
- The page's Models view ranks routes itself; it should show the
  router's preview. It moves when the router does.
