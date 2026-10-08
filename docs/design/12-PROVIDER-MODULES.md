# Provider modules and the first route preview (Z.ai + DeepSeek)

Version: V0.1, 2026-10-08. Status: **design for the next slice; code follows step by step (§8).**
Basis: [design 11](11-SUPPLY-ENGINE.md) (rulings R1–R8), [Magpie research](../research/MAGPIE-AND-ANT.md)
§First business slice steps 1–3, [ENG-FINDINGS](../ENG-FINDINGS-2026-10-08.md) ENG-02/ENG-04.
User choice (2026-10-08): the first two providers are **Z.ai** (GLM Coding Plan) and **DeepSeek**.

## 1. What this slice delivers

One provider-native vertical slice on the existing Go/SQLite stack, ending in a **read-only route
preview**: no request is sent to any model.

- **Given** a Z.ai coding-plan key and a DeepSeek key (found on this Mac or added on the page),
  **when** Ant reads them, **then** the page shows the plan's windows or the balance, how fresh
  each reading is, and which models each account serves.
- **Given** a model the user names (e.g. `glm-5.3`, `deepseek-v4`), **when** they open its route
  preview, **then** Ant lists every account that could serve it, in the order Ant would use them,
  with the reason for that order, and every account it would not use, with the reason why not.
- **Given** an account the user paused, **then** it is listed as "paused by you" and never chosen;
  resuming restores it. Pausing changes only Ant's own choices; it doesn't touch the subscription.

The two providers are deliberately different: Z.ai is a windowed subscription whose windows are
shared by several models; DeepSeek is a prepaid balance spent per token, and already has Ant's
execution adapter. Together they exercise both kinds of supply and join the two subsystems
(ENG-02) at one provider each.

## 2. The provider module (ruling R1)

One provider = one package `internal/providers/<id>` that owns everything about it. It exports one
value, registered in `providers/catalog`:

```go
type Module struct {
    ID, Name  string
    Supply    *Supply              // reading what an account holds; nil if none
    Execution *ExecutionRegistration // existing descriptor + adapter + policies; nil if not callable yet
}

type Supply struct {
    Origins   []string          // every https origin the collector may contact (enforced by Fetch)
    Endpoints []Endpoint        // {Base, Protocol}: where its models are called
    Packages  []PackageTemplate // the meters an account can hold, as data
    Collector Collector
}

type Collector interface {
    Inventory(ctx context.Context, s Session, fetch observed.Fetch) (observed.Inventory, error)
    Models(ctx context.Context, s Session, fetch observed.Fetch) ([]observed.UpstreamModel, error)
}
```

- **Data, not functions,** for everything that is data: templates, origins, endpoints. Behaviour
  is two small methods. Parsers move from `internal/quota` unchanged in substance and keep their
  fixture tests (design 11 §6: nothing is rewritten blind).
- **`internal/domain/observed`** (new, leaf) holds the shared value types: `Inventory`,
  `PackageReading`, `ResourceReading`, `UpstreamModel`, and the network seam
  `Fetch func(ctx, Request) (Response, error)` with plain `Request`/`Response` structs. It imports
  nothing but `domain`, so the supply engine, provider modules and transport can all use it. The
  engine's `supply.Inventory` types move there.
- **`Fetch` is implemented in `internal/transport`** (`NewObservationFetch(origins)`): HTTPS only,
  only the module's origins, no cross-origin or HTTPS→HTTP redirects, bounded size and time. A
  module never imports `net/http` (check-deps enforces it for `providers/*`).
- **`Session`** is the resolved credential a collector reads with: its values (decrypted for added
  credentials, read from the owning app for found ones) and its locator. Collectors never persist
  anything and never see another credential.
- Discovery (where a found credential lives) stays in `internal/quota` for these two providers in
  this slice; moving discovery strategies is design 11 phase 4's later work.

## 3. The two cases (provider contracts)

| | Z.ai GLM Coding Plan | DeepSeek |
|---|---|---|
| Setup | API key, found (ZCode, Claude Code settings, `$ZAI_API_KEY`, OpenCode) or added; region global (`api.z.ai`). BigModel (China) is a separate provider and out of this slice. | API key, found (ZCode, `$DEEPSEEK_API_KEY`) or added |
| Observed | plan level; 5-hour window and weekly window as **percent used** with provider-reported next reset; monthly MCP tool calls (aside: never limits models) | balance per currency: total, granted, topped-up (exact cents); `is_available` |
| Meters | `window/5h`, `window/week` (unit percent, limit 100); `window/mcp-month` (aside) | `balance/<currency>` (money, quantum −2) |
| Time | resets as the provider reports them (`reset_basis = reported`); a key with no plan has no windows | lifetime; never resets |
| Identity | **none reported** (today's merge is inferred from reset times): each key is its own account in the store (design 11 §2.6) | none reported: each key is its own account |
| Models | listed by `GET /api/coding/paas/v4/models` | listed by `GET /models` |
| Endpoint | `https://api.z.ai/api/coding/paas/v4`, protocol `openai-chat` | `https://api.deepseek.com`, protocol `openai-chat` |
| Spending | included in the plan: calls draw on both windows; no money per call while the plan lasts. A key **without** a plan is pay-as-you-go (spends money) | every call spends the balance, priced per million tokens |
| Unavailable | key refused → signed out; plan windows used up → excluded until reset; no plan → pay-as-you-go | key refused → signed out; `is_available = false` → excluded ("balance too low to call") |
| Origins | `https://api.z.ai` | `https://api.deepseek.com` |

## 4. Models and offers (design 11 phase 3)

- **`models`** — canonical models from a small curated `models.json` shipped with Ant (vendor,
  family, what it makes, context window); this slice seeds GLM and DeepSeek families only.
- **`model_aliases`** — upstream id → canonical id: from the catalog, from rules (lower-case, strip
  a `vendor/` prefix), or set by the user (namespaced). An upstream id that resolves to nothing is
  kept and shown as itself.
- **`supply_offers`** — one row per (credential, endpoint, upstream model): the canonical model it
  resolved to, and `draws`: the meters a call through it draws on (Z.ai: both plan windows;
  DeepSeek: the balance). Written by the reconciler from `Collector.Models`; refreshed every 6 h.

Migration v7 adds `supply_offers` and `supply_credentials.paused` (0/1); table count 41 → 42.
**Changed while building (step 4):** canonical model ids come from one rule in code
(`catalog.ModelID`: lower-case, drop a `vendor/` prefix, nothing guessed beyond that), and there
are no `models`/`model_aliases` tables or curated `models.json` yet: nothing in this slice shows
model metadata or lets the user set an alias, so storing them would be speculative. They land with
the first feature that needs them.

## 5. Route preview

`GET /api/route?model=<id>` (loopback-only page API, ruling R8) → candidates and exclusions,
computed by the supply engine from the store; nothing is sent to a provider.

**Candidates** are the offers whose model is the requested one (by canonical id or alias).

**Exclusions**, checked in this order; the first that applies is the reason shown:

1. paused by you;
2. the account's last read failed: signed out ("sign in again") or couldn't be read;
3. a meter it draws on is used up: "5 hours used up · resets in 1h 20m";
4. the balance can't be called (`is_available = false`): "balance too low to call";
5. nothing about it was ever read (declared only).

**Order** of the eligible, best first (design 11 §3.5's policy; it proposes, the user decides):

1. **Use before it's lost** — an included allowance that resets or ends within 24 h with at least
   30 % left, unless the recent pace uses it up first (stage-1 rule);
2. **Plan headroom** — included allowances, the one whose tightest window has the most left first;
3. **Prepaid balance** — the balance that lasts longest at its recent pace first;
4. **Pay-as-you-go** last.

Ties go to the fresher reading. An account last read successfully more than 30 minutes ago stays
eligible and is marked "last checked 2h ago". Freshness is the last successful read, not a
reading's own time: readings are stored only when a value changes, so a reading's time says when
it last *changed* — a balance unchanged for six hours but confirmed a minute ago is current
(review M-4, resolved by wording; the data was already right). Each candidate carries one sentence of why ("Plan · 5 hours 82% left, week
54% left") and the exclusions carry theirs.

## 6. Pause and resume

The card menu gains **Pause routing / Resume routing**. It sets `paused` on every credential of
that card; reading continues, and the preview shows "paused by you". Pausing doesn't cancel,
downgrade or refund anything at the provider.

## 7. The page

- **Models** view: each model row opens its route preview inline: the chosen account first, then
  the order, then "Not used" with reasons. One sentence each.
- **Cards**: the "…" menu offers Edit, Pause/Resume routing, Remove — reviewed as a menu, not only
  as edit (stage-1 open item).
- **Add flow**: the final step no longer restates the plan rule shown on the step before (stage-1
  open item).

## 8. Steps (one commit each; four gates green on each)

1. This design note.
2. `domain/observed` types + `transport` observation `Fetch`; engine types moved; check-deps
   rules for `providers/*`.
3. `providers/zai` and `providers/deepseek` supply modules (collectors, templates, parsers moved
   with fixture tests); the engine reads these two through their modules, the rest through the
   quota adapter; the page's cards for them are projected from the module's inventory.
4. Migration v7, model catalog, aliases, offers from model listings.
5. Route preview in the engine + `GET /api/route`; pause/resume.
6. Page: route preview in the Models view, card menu, Add-flow fix; walkthrough recorded in the
   stage-1 record.

**Acceptance:** a test per §5 rule (each exclusion, each order class, ties, staleness, aliases);
fixture parity for both moved parsers; the page walkthrough in demo mode and in an isolated live
run; no request to a model endpoint anywhere in the slice.

## 9. Not in this slice

New providers; BigModel; moving discovery strategies; the other 25 providers (they stay on the
quota adapter); calling models (dispatch, gateway, failover); config activation from observed facts
(design 11 phase 5's `FactSource`); price-based ordering between balances (DeepSeek's prices are
known to the execution side but the preview orders balances by how long they last).
