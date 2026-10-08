# Design 12 — provider modules and the route-preview slice (Z.ai + DeepSeek)

## Step 2: shared observation types and the origin-pinned Fetch (commit 4712f2e)

`domain/observed` (Inventory, readings, upstream models — moved from the supply engine, no
aliases — and `Fetch`/`Request`/`Response`); `transport.NewObservationFetch` (HTTPS only, declared
origins only, no off-origin or HTTPS→HTTP redirect, 1 MiB/20 s bounds; tests on a TLS server:
declared origin read, undeclared refused, plain HTTP refused, a redirect away answered as the
redirect without the key reaching the other server, oversize refused, bad origin lists rejected).

## Step 3: Z.ai and DeepSeek read through their own modules

- `providers/catalog/module.go`: the module contract (`Module`, `Supply`, `Endpoint` with the
  meters a call draws on, `Session`, `Collector`, `ErrSignedOut`) and the shared OpenAI-style
  model-list parser, now used by quota too (one parser).
- `providers/zai` (new): the plan parser moved from quota — quota's BigModel path calls it, so Z.ai
  and BigModel share one parser — and a collector on `Fetch`: one subscription package with
  `window/short`, `window/week` (calls draw on both) and the aside `window/mcp-month`, percent at
  10⁻²; a key without a plan is a pay-as-you-go package with a note. No identity is reported; the
  old reset-time grouping travels as `DisplayGroup`, display only.
- `providers/deepseek`: a supply collector on the existing exact balance parser: one prepaid
  package (`Usable` from `is_available`), a meter per currency in cents.
- The adapter reads these two providers through their modules and builds their cards from what
  the module read; the other providers are unchanged. The server wires both modules with their
  own `Fetch`.

**Found by the live check** (real binary, isolated home and PATH, a fake key in `ZAI_API_KEY`):
Z.ai refuses a bad key with **HTTP 200** and `{"code":401,"msg":"token expired or incorrect",
"success":false}`, not a 401 status, so both the new module and the old quota reader showed
"couldn't read" instead of "signed out" (which also means "retry soon" instead of "wait for the
user"). Fixed in the shared parser (`zai.ErrKeyRefused`) for both paths, with that exact reply as a
fixture. Re-run live: card, stored credential and run all say signed out. Two read-only requests
with the fake key were made to `api.z.ai` for this (ruling R4).

**Tests.** Module tests on quota's own fixtures (parity): plan inventory (bare-key Authorization,
values, resets, meters, draws), refused key both ways, no plan, models; DeepSeek inventory, models,
refused key, balance too low to call. `TestNativeModulesReadZaiAndDeepSeek`: both read through
their modules with the quota readers' client unable to reach anything; cards, stored facts, and
two Z.ai keys of one plan shown as one card but stored as two accounts. `TestDecimalIsExact`.

Also: the catalog's own test moved to an external test package (it imported `deepseek`, which now
imports `catalog`).

## Step 4: offers and pause in the store

Migration v7 (`supply_offers`, `supply_credentials.paused`; 42 tables): a native read records one
offer per (endpoint, listed model) with the meters a call draws on (Z.ai: both plan windows;
DeepSeek: the balances); the set is replaced each time; the model id is canonical by one rule
(`catalog.ModelID`). Pause is the user's: an engine re-recording a credential leaves it alone.
Models/aliases tables deferred (design 12 §4 note). Tests: migration v6→v7 on a real v6 database
built from the migration files (credentials kept, unpaused), rollback tests forget v7, offers and
pause in the store, offers end to end for both providers, the model-id rule.

## Step 5: route preview and pause/resume

`supply/preview.go`: for a canonical model, every offer is judged — excluded in design 12 §5's
order (paused by you; signed out / couldn't read, with the provider's words; not read yet; a drawn
meter used up, with when it resets; balance too low to call; declared only) — or ordered: use
before it's lost (a drawn window resetting within 24 h with ≥ 30 % left, unless the pace uses it
up first) → plan headroom (tightest drawn window, most left first) → prepaid (lasts longest at its
recent pace first; no spending lasts longest) → pay-as-you-go; ties to the fresher reading;
readings older than 30 min are marked. Only the meters an endpoint declares a call draws on are
judged (Z.ai's MCP calls never are). The package's `usable` flag is now stored.

Page API (loopback only): `GET /api/route?model=…` (any casing or vendor prefix), `POST /api/pause`
(pauses every connection on the card), `paused` in the snapshot.

**Tests.** `TestRoutePreview` (one account per rule: use-before-lost, two plans ordered by headroom
with the stale one marked, pay-as-you-go, used up with reset, paused; DeepSeek prepaid and too low
to call; an unknown model), `…SignedOutAfterReading`, `…OrdersBalancesByHowLongTheyLast`; end to
end through the page API with both modules: preview, pause both Z.ai keys, preview again, resume;
the page API without a router. `TestDecimalIsExact` and `TestSpan` moved with the shared helpers.

## Step 6: the page, and the walkthrough

- **Models view:** opening a model shows "What Ant would do" — the accounts in order, each with
  its class and one sentence, then "Not used" with why — and "A preview: nothing is sent to any
  model." The row's best account becomes the preview's first choice (the page's own guess and the
  server's ranking can't disagree on screen). Models no module covers yet say so in one line and
  keep the accounts' own list; accounts the preview doesn't cover are named as "not in the preview
  yet", never mixed into the order.
- **Card menu:** "…" opens Edit · Pause/Resume routing (only on cards that serve models) ·
  Remove/Hide (with confirmation); a paused card shows "Paused".
- **Add flow:** the plan rule appears once, on the step where it's chosen.
- **Demo mode** runs the real engine over the sample Z.ai and DeepSeek accounts (namespace
  `demo`, nothing read), so the preview is the real one there too.

**Walkthrough** (demo mode, built binary): `glm-5.3` → 1. Z.ai, use first, "Use before it's lost:
5 hours 82% left, resets in 2h 14m", plus four accounts named as not previewed yet;
`deepseek-v4` → DeepSeek, prepaid, "Prepaid: 23.40 CNY left"; `kimi-k3` → not covered, one line.
Card menu on Z.ai: Edit · Pause routing · Hide; Pause → card tagged Paused, preview "Not used:
Paused by you"; Resume → back. Kling (no models): Edit · Hide only. Escape closes.

**Found and fixed during the walkthrough:**
1. A sample balance read "23.400000 CNY" (adapter precision); amounts now read as people write
   them, still exact (`supply.Money`).
2. The card menu lived inside the card: the page's 30-second refresh destroyed an open menu, and
   the card's `overflow: hidden` could clip it. It now sits above the page, anchored to its
   button, reopens after a refresh, and closes when the cards leave the view.

**Gate note (step 6).** One full gate run failed in `tests/integration`:
`TestPreparationConcurrentCapacityAndSingleConsume` — "store: command exceeded budget: context
deadline exceeded" (a write budget under concurrent load). This slice doesn't touch the ledger or
preparation. Re-run alone with `-race -count=10`: 10/10 pass; the full gates were then re-run from
the start before committing. Recorded as a load-sensitive test in accepted code, not fixed here.
