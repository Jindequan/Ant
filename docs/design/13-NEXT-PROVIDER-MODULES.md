# The next provider modules: Kimi, then model-scoped meters, then Bailian

Version: V1.1, 2026-10-08. Status: **plan, executing step by step (§7). Phase A done: Kimi added
without a core change (acceptance check empty, A5 evidence).**
Basis: [design 12](12-PROVIDER-MODULES.md) (module contract, route preview), its review
[REVIEW-DESIGN12-SLICE](../REVIEW-DESIGN12-SLICE-2026-10-08.md), [design 11](11-SUPPLY-ENGINE.md)
(rulings R1–R8), [Magpie research](../research/MAGPIE-AND-ANT.md) stage 5 ("a third provider tests
that provider code/config can be added without modifying core routing/storage algorithms").
User direction (2026-10-08): "make a detail and perfect plan, then do it continuely".

## 1. Goal and the acceptance test

Two goals, in order:

1. **Prove the module design** with a third provider: Kimi/Moonshot is added as a module **without
   touching the core**.
2. **Extend it deliberately** for what the first three can't express — meters that belong to one
   model — and use that extension for the provider that needs it most, Alibaba Cloud Bailian.

**What "core" means here** (so the test is mechanical, not a judgement):

| Core — must not change while adding Kimi | Module layer — may change |
|---|---|
| `internal/supply` (engine, reconciler, outlook, preview) | `internal/providers/*` (catalog contract, modules) |
| `internal/store`, `internal/store/sqlite`, `server/migrations` | `internal/supply/quotasource` (the adapter) |
| `internal/domain/observed` | `cmd/ant-server` (wiring), `internal/quota` (pointing at shared parsers) |

**Check:** `git diff --stat <before-A2>..<after-A5> -- server/internal/supply/*.go
server/internal/supply/*_test.go server/internal/store server/migrations
server/internal/domain` is **empty**. Contract changes to the module layer that Kimi needs land
*before* that range (step A1), as their own reviewed commit, so the range contains only "add a
provider".

## 2. Verified provider facts

**Kimi Code** (subscription for coding tools) — official docs:
- Keys from the Kimi Code console (up to 5). Both protocols: OpenAI-compatible base
  `https://api.kimi.com/coding/v1`, Anthropic-compatible base `https://api.kimi.com/coding/`.
  Third-party tools use model id `kimi-for-coding` (newer pages also list K3 ids).
- Usage: `GET https://api.kimi.com/coding/v1/usages` — **observed** (kimi-cli, Magpie), not in
  the official API reference; today's quota reader reads it. Shape: `usage` (the week:
  limit/used/resetTime) and `limits[]` (`window.duration`+`timeUnit`, `detail.limit`/`remaining`/
  `resetTime`); numbers may be strings. Ant records what it reads and labels the source.
- Docs warn that changing a tool's User-Agent may suspend benefits: Ant never sends someone
  else's client identity.

**Moonshot / Kimi open platform** (prepaid API balance) — official balance reference:
- `GET /v1/users/me/balance`, Bearer key. China: `https://api.moonshot.cn` (platform.kimi.com),
  amounts in **CNY**; international: `https://api.moonshot.ai` (platform.kimi.ai), **USD**. Keys
  are not interchangeable between the two.
- Reply `{"code":0,"data":{"available_balance":49.58894,"voucher_balance":46.58893,
  "cash_balance":3.00001},"scode":"0x0","status":true}`. Available = cash + voucher; voucher is
  never negative; **cash below zero is arrears**, and then available = voucher. Available ≤ 0 →
  inference calls fail (`exceeded_current_quota_error`). 401 = missing or invalid key.
- Models: `GET /v1/models` (OpenAI-style list). Calls: OpenAI-compatible at the same origin `/v1`.

Today's quota reader converts these balances through floating point (`money2`): the module parses
them **exactly** (decimal strings from the JSON number token), keeping five decimals and negative
cash.

Sources: [Kimi Code overview](https://www.kimi.com/code/docs/en/),
[Kimi Code providers and models](https://www.kimi.com/code/docs/en/kimi-code-cli/configuration/providers-and-models.html),
[Kimi platform balance API](https://platform.kimi.com/docs/api/balance.md),
[Kimi platform balance API (international)](https://platform.kimi.ai/docs/api/balance.md).

## 3. Phase A — Kimi as the third module (the acceptance test)

| Step | Delivers | Acceptance |
|---|---|---|
| **A1** Module contract: products and regions | `catalog.Session` gains `Product` (the connection's mode: "code", "api"); `catalog.Endpoint` gains optional `Product` and `Region` selectors; `catalog.Offers(s, session, models)` uses only matching endpoints. The adapter passes the product. Z.ai/DeepSeek declare none and behave as before. | Existing tests unchanged and green; new tests for selection (product, region, unselected = all). |
| **A2** `providers/kimi` | Module `kimi` with origins `api.kimi.com`, `api.moonshot.cn`, `api.moonshot.ai`; collector by product: **code** → windows (5 h-style windows + week, percent) from `/coding/v1/usages`, models from `/coding/v1/models`, endpoint `https://api.kimi.com/coding/v1` (openai-chat) drawing on its windows; **api** → one prepaid package per region (CNY or USD), exact balance with voucher/cash in evidence, `Usable` = available > 0, arrears noted, endpoint per region drawing on the balance. Parsers move from quota; quota's Kimi readers call them (one parser each). Canonical fixtures in `providers/kimi/testdata`, read by module and quota tests. | Module tests on the fixtures (windows, both regions, arrears, available ≤ 0, refused key, no key sends nothing, models); quota's Kimi tests read the same files; mutation of a fixture fails both. |
| **A3** Wiring | Server reads Kimi through its module; demo samples Kimi too. | End-to-end adapter test with Kimi Code + Moonshot on TLS fakes: cards, stored facts, offers per product, preview for `kimi-for-coding`. |
| **A4** Live check | Real binary, isolated home/PATH, fake keys for both products: one read-only request each (R4). | Both cards and stored credentials say "signed out"; the evidence records the requests. |
| **A5** Acceptance record | The `git diff --stat` check of §1 over A2–A4, recorded with its output; page walkthrough of the Kimi preview in demo. | Check output empty; walkthrough recorded. |

If A2–A4 cannot be done without a core change, **stop**: that is the design failing its test, and
the finding goes to the user before anything else.

## 4. Phase B — model-scoped meters (a deliberate core change)

Today an endpoint declares the same meters for every model it serves. Bailian's new-user free
quota (one grant per model) and MiniMax's per-model windows can't be expressed: a call to model M
draws on M's own grant, and on nothing scoped to another model.

| Step | Delivers | Acceptance |
|---|---|---|
| **B1** Design note (§ in this file, updated) | The rule: an endpoint may declare `Draws` entries that are template refs (as today) **and** the token `scope:model` = "every meter of the account whose scope is this offer's model". Model ids compare canonically (`catalog.ModelID`). A meter scoped to another model is never drawn. | Note reviewed in the commit; no code. |
| **B2** Core: preview resolves `scope:model` | `supply/preview.go` resolves the token per offer; outlook and reasons unchanged. | Tests: a model's own grant is drawn, another model's isn't, canonical matching, empty grant → excluded "used up", grant + balance → grant first (use before lost / free first). |
| **B3** Order class for free grants | Design 11 §3.5's order already puts "use before it's lost" first; a **free grant** (`free_grant` package) with room is ordered with it (it expires and costs nothing). | Test: free grant ahead of plan headroom and prepaid. |

## 5. Phase C — Alibaba Cloud Bailian as the fourth module

Bailian has three products and three ways to read them; the module owns the reading, while
sign-in (console login handshake, browser sessions) stays in quota's discovery (design 11 phase 4
moves it later).

| Step | Delivers | Acceptance |
|---|---|---|
| **C1** Contract: time for signed requests | `catalog.Session` gains `Now` (the engine's clock) so a collector can sign requests (Alibaba Cloud RPC signing) without reading the clock itself. | Existing modules unaffected. |
| **C2** `providers/aliyun`: Token Plan | Collector over the Bailian CLI's console token (the gateway the official `bl` CLI uses): windows, add-on credits, tool benefits as packages; parsers moved from quota with fixtures. | Fixture parity with quota's Bailian tests; refused token → signed out. |
| **C3** Pay-as-you-go | Collector over an AccessKey: BSS OpenAPI with RPC signing (signature test vector from Alibaba's docs, already in quota's tests), this month's and last month's charges, the account balance. | Signature vector test; bill parsing parity. |
| **C4** New-user free quota | One `free_grant` package; one meter per model, scoped to that model, lifetime ending on its expiry; endpoint (DashScope OpenAI-compatible) draws `scope:model` (phase B). | Preview for a model with a grant uses the grant; another model doesn't. |
| **C5** Wiring, live check, walkthrough | As A3–A5 for Bailian (fake token / fake AccessKey: read-only, refused). | Recorded. |

Origins are taken from the reader as it is today and listed in the module; nothing new is
contacted. What the console guards with its anti-bot layer stays unread, as today.

## 6. Risks and how they're handled

- **Undocumented endpoints** (Kimi `/usages`, Bailian console gateway): labelled "observed" in the
  module; a shape change surfaces as "couldn't read" with the provider's words, never as wrong
  numbers (parsers reject what they don't understand).
- **Money precision**: Moonshot's five-decimal amounts and Bailian's bills are parsed exactly; no
  floating point on the module path (tests with negative and many-decimal amounts).
- **Scope creep**: no new providers beyond Kimi and Bailian; no execution; no page features beyond
  showing what the modules read and preview. Every step is one commit, four gates green, a STATUS
  line, evidence in `docs/evidence/WORK/PROVIDER-MODULES/`.
- **Live calls**: only with fake keys, read-only, one request per product (R4); recorded.
- **The flaky ledger test** (STATUS follow-up): if it fails a gate run, it is re-run alone and the
  full gates re-run before committing, as before; never a reason to skip a gate.

## 7. Order of work

A1 → A2 → A3 → A4 → A5 (Kimi; stop if the acceptance check fails) → B1 → B2 → B3 (model-scoped
meters) → C1 → C2 → C3 → C4 → C5 (Bailian). Each step: code, tests, four gates, STATUS line,
commit; the evidence file grows with each.

## 8. Not in this plan

Execution of any model call; providers other than Kimi and Bailian; moving discovery and sign-in
out of quota (design 11 phase 4); the models/aliases tables (still no feature needs them);
BigModel as a module (it shares Z.ai's parser already); page features beyond the walkthroughs.
