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

## Review REVIEW-DESIGN12-SLICE: housekeeping (2026-10-08)

One commit per step, four gates green each: (1) Fetch without environment proxy, checked headers,
explicit transport, default port (M-1, L1, L2, L5) + the STATUS follow-up item for the
load-sensitive ledger test (M-3); (2) one canonical fixture per provider reply, proven by mutation
(M-2); (3) "last checked" wording and design-order exclusion reasons (M-4 — pushback on the
suggested data change, with reasons in design 12 §5 — and L7, with a test that fails on the old
order); (4) one plan-name helper, no request without a key (L3, L4); (5) the card menu follows its
card through filters (checked in demo: closes when a search hides the card, stays when it doesn't),
DemoSource tests including "has no network field", the table-count test renamed, and the
quotasource/provider-package rule explained in `check-deps.py` (L6, L8, L9, L10).

## Design 13 phase A: Kimi as the third module (2026-10-08)

**A1** (`15bb7c3`, the base of the acceptance range): products and regions in the module contract.

**A2 — `providers/kimi`.** One module, two products, two regions:

| Session | Reads (GET, Bearer key) | Holds | Offers at |
|---|---|---|---|
| `code` | `api.kimi.com/coding/v1/usages` (observed), `/coding/v1/models` | subscription `kimi/code`: `window/short` (`-2`, `-3` if reported), `window/week`, percent | `https://api.kimi.com/coding/v1`, drawing every window |
| `api`, region `cn` | `api.moonshot.cn/v1/users/me/balance`, `/v1/models` | prepaid `kimi/balance-cn`: `balance/cny` at 10^-6 | `https://api.moonshot.cn/v1`, drawing `balance/cny` |
| `api`, region `ai` | `api.moonshot.ai/…` | prepaid `kimi/balance-ai`: `balance/usd` | `https://api.moonshot.ai/v1`, drawing `balance/usd` |

- Exact money: the balance is read from the JSON number's text, never floating point; the
  official example `49.58894 / 46.58893 / 3.00001` keeps every digit (quota used to show
  `49.58 / 46.58 / 3.00`). `catalog.ParseDecimal` (moved from the adapter) is the one exact
  parser; amounts with exponents are refused.
- Arrears (cash < 0) and used up (available ≤ 0 → not usable, "balance too low to call" in the
  preview) are notes from one function, used by the module and by quota.
- A key without a platform is sent nowhere (keys don't work across `.cn` and `.ai`); no key sends
  nothing; 401/403 → signed out for both products.
- Quota's two Kimi readers now call the module's parsers; the window-naming rule MiniMax shares
  moved to `catalog.WindowName`.
- Fixtures `providers/kimi/testdata/{usages,balance,arrears,empty}.json`, read by the module's
  tests and by quota's. **Mutation check:** `remaining 15→10` in `usages.json` and
  `49.58894→49.58895` in `balance.json` failed `TestCodeWindows`, `TestBalanceByRegion`,
  `TestParseBalanceRejects` (module) and `TestKimiCodeWindowsAndMoonshotBalance` (quota);
  restored, both green.

**Gate note (A2).** The first full gate run failed in `make test-contract`:
`TestReorderPoolUpdatesLiveAndCompiledPriorityAtomically` (`internal/application`) — "store:
command exceeded budget: context deadline exceeded", the same store write budget under concurrent
`-race` load as the ledger test in M-3. Nothing in A2 touches the application, config or store
packages. Re-run alone with `-race -count=10`: 10/10 pass; full gates re-run from the start before
committing. Added to the M-3 follow-up: the store budget under gate load, not one test.

**A3 — wiring.** The server reads Kimi through its module (`providerModules`, a Fetch limited to
its three origins); demo samples Kimi Code with the module's endpoint. End-to-end test
`TestNativeModuleReadsKimi` (TLS fakes; the quota readers' own client reaches nothing, so a card
can only be OK through the module): a Kimi Code key from the environment, a China-platform key from
the environment, an international-platform key from OpenCode's `auth.json` →

- three cards: Kimi Code windows 5 hours / Week at 25 % used; `49.58894 CNY` (vouchers
  `46.58893`, cash `3.00001`); `2.50 USD` with cash `-1.25` and the note "Cash is in arrears:
  -1.25 USD.";
- each key read at its own platform only (the test fails if a key reaches the other one);
- stored: three accounts; `window/short` at 7500 (percent ×100); `balance/cny` 49588940 and
  `balance/usd` 2500000 at quantum −6;
- offers: `kimi-for-coding` at `…/coding/v1` drawing the plan's windows; `kimi-k3` once per
  platform, each drawing only its own currency;
- `GET /api/route?model=kimi-for-coding` → the plan, "5 hours 75% left, Week 75% left";
  `?model=Kimi-K3` → both balances as prepaid, "Prepaid: 49.58894 CNY left" and "Prepaid: 2.50
  USD left".

Found while building: the card showed balances at the reading's quantum ("49.588940"); the
adapter now writes them with `supply.Money`, as the preview does. An added key is recorded only
once sealed in the vault (its secret row is a foreign key), so the test finds the international
key in OpenCode rather than faking a vault; the add path keeps its own vault tests.

Acceptance check so far: `git diff --stat 15bb7c3 -- <core paths>` → empty.

**A4 — live check** (real binary built from `cb3c4ab`, `env -i` with HOME and PATH in a scratch
directory and an empty PATH; fake keys: `KIMI_CODE_API_KEY`, `MOONSHOT_API_KEY`, and a
`moonshotai` key in the scratch home's OpenCode `auth.json`). Requests made with fake keys (R4):
one inventory read each, to `api.kimi.com/coding/v1/usages`, `api.moonshot.cn/v1/users/me/balance`
and `api.moonshot.ai/v1/users/me/balance`; a refused key reads no model list. Results:

| Credential | Card | Stored credential | `collector_runs` |
|---|---|---|---|
| Kimi Code (`$KIMI_CODE_API_KEY`) | signed out: "Kimi Code refused the key. Check it in your Kimi Code console." | `signed_out`, same detail | 1 inventory run, `signed_out` |
| Moonshot China (`$MOONSHOT_API_KEY`) | signed out: "Moonshot refused the key. Check it in platform.kimi.com › API keys." | `signed_out` | 1 run, `signed_out` |
| Moonshot international (OpenCode) | signed out: "… platform.kimi.ai › API keys." | `signed_out` | 1 run, `signed_out` |

No accounts or offers were recorded (nothing was read); `GET /api/route?model=kimi-for-coding`
→ `{"use":[],"notUsed":[]}`. All three real hosts answered a bad key with a 401 status (unlike
Z.ai's HTTP 200), so no parser change was needed.

**Found by the live check:** the two Moonshot cards' "Open" link went to Kimi Code (the
provider's link); quota's reader used to set each platform's console itself, which the module
path doesn't run. A mode may now give its link from the account's values (`quota.Mode.Link`),
used by both paths; the end-to-end test checks all three links and fails without the rule
(mutation-checked). Not re-run live: it would mean three more requests with fake keys for what the
test already shows.

**Gate note (A4).** The first gate run failed `TestNativeModuleReadsKimi` under `-race`: the test's
fake servers appended to a request log from concurrent reads (the engine reads credentials in
parallel). A test bug from A3 that passed A3's gates by chance; the log is now guarded and copied
before checking. `-race -count=5`: 5/5 pass; full gates re-run.
