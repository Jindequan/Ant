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
