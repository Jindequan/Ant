# Review of design/11-SUPPLY-ENGINE.md — findings for self-review

Reviewed: `docs/design/11-SUPPLY-ENGINE.md` (V0.1 draft, 2026-10-08), against the current tree
(code facts re-verified by grep on 2026-10-08 — citations below are greppable), the frozen rulings in
[design 07 §1](design/07-POOLS-AND-CONFIG.md), the user's original requirements
([USER-ORIGINAL-REQUIREMENTS.md](USER-ORIGINAL-REQUIREMENTS.md)), and
[ENG-FINDINGS-2026-10-08.md](ENG-FINDINGS-2026-10-08.md).

Purpose: self-review checklist for the design author. Every finding must end up either **resolved in
the text** or **explicitly listed as an open ruling question with an owner**. Add a V0.2 changelog
section listing what changed in response to each ID.

## Verdict

The entity chain (credential → account → package → resource → reading; binding → offer → model) is
the right answer to both ENG-02 (unify the two provider subsystems) and ENG-04 (bridge observation to
executable configuration), and it matches the user's original requirements. Six provider/resource
archetypes are covered by concrete examples. **But the design assumes two problems are already solved
that are not — the dependency gate is currently red (D11-01) and the `resources` table ownership
question is unresolved (D11-02) — and it leaves the namespace story, two resource roles, and the
retention fold without landing points.** Resolve D11-01/02 before any phase-1 code.

## What is right — keep these through revision

- §1's six-way decomposition of the old `Account` blob (credential / account / package / resource /
  offer / presentation) — this is the correct correction of both user vetoes.
- Reuse of the accepted pure algorithms: `domain.WindowRule`, `resources.WindowAt`
  (`internal/domain/resources/window.go:91`, DST fold/gap tested), `offer_resources` many-to-many.
- Collector as the only per-provider code; discovery as data-parameterized strategies; nil-collector
  rule-only packages (Figma/Stitch).
- Phase-2 adapter wrapper with fixture-test parity per provider before porting ("nothing is rewritten
  blind").
- Ranking as a policy that proposes pool order while the user's order stays authoritative (design 07).
- Decision 1: added secrets encrypted in `secret_records`; found secrets stay where they live, only a
  `locator_json` is stored — never the secret.
- Web-only bindings "shown, tracked, never routed" as a concept (make the enforcement typed — D11-07b).
- §2.3's example rows: they double as the archetype coverage proof. Keep them current as the model
  evolves.

---

## Findings

### D11-01 · P0 · The design is silent on the dependency boundary it depends on

**Evidence**: §3.2 says collectors are "pure HTTP + parse"; §3.1 strategies include browser, keychain,
env, cli-config readers. Current tree, fresh run of `make check` (2026-10-08):

```
check-deps: DEPENDENCY VIOLATIONS:
  -> ant/server/internal/quota -> database/sql (forbidden outside ['ant/server/internal/store', 'ant/server/internal/store/sqlite'])
  -> ant/server/internal/quota -> modernc.org/sqlite (forbidden outside ['ant/server/internal/store/sqlite'])
  -> ant/server/internal/quota -> net/http (forbidden outside ['ant/server/internal/api', 'ant/server/internal/transport'])
  -> ant/server/internal/webui -> net/http (forbidden outside ['ant/server/internal/api', 'ant/server/internal/transport'])
```

`database/sql` enters quota via `browsercookies.go:10` (reads the Chrome cookie SQLite store
directly). `net/http` enters via the provider readers. Implementing design 11 phases 1–4 as written
re-creates these violations under a new package name.

**Required**: one section stating the ruling — either (A) collector HTTP goes through an injected
transport port with per-provider endpoint allowlists (the `deepseek` balance pattern:
`application/deepseek_balance.go` + `transport.NewHTTPClientWithAllowedEndpoints`), or (B) the
boundary contract in `scripts/check-deps.py` is formally amended with a written rule ("observation
subsystem may own net/http; never SQL; never execution egress"). In both cases the Chrome cookie
store read is isolated behind a narrow platform helper so the supply package stays SQL-free.

**Acceptance**: the design names the package layout and the boundary ruling; phase-1 code keeps
`make check` green (or records the amendment). This is ENG-01's resolution — coordinate the wording
with [ENG-FINDINGS](ENG-FINDINGS-2026-10-08.md) ENG-01.

### D11-02 · P0 · `resources` gets a second writer, and the frozen Q1 v3 identity model is claimed but not reconciled

**Evidence**: today `resources` rows are written by configuration activation
(`config_revisions` JSON → `Ledger.ActivateConfig` → normalized tables; design 02 "All sequential
updates, configuration snapshots, controlVersion, audit, and receipt go in one transaction"). Design
07 §1 freezes Q1 v3 (resolution-based identity): server-derived globally-unique `physicalKey` on a
`VerifiedResourceFact`, `VerifiedScope` (account or shared-with-alias), typed evidence obligations
(metricId/bound required and matching the verified instance, configuredCapacity ≤ capacityBound,
explicit dimensions must match). Design 11 §3.3 derives `resource_id` from (account, template, scope)
and asserts "physicalKey per design 07 §1" without stating the mapping.

**Why it matters**: two writers on one table (activation vs reconciler) will conflict; and a frozen
ruling's identity vocabulary cannot be borrowed by assertion — the mapping must be explicit or the
two designs drift. The underlying direction is good: collector-verified facts are a *better* source
for 07's Verified instances than the rejected manual forms ever were.

**Required**: a section mapping supply-engine entities onto 07's model — VerifiedScope ≈ account
(+ shared scope when D11-06 admits it), VerifiedResourceFact ≈ reconciler-created resource rows,
evidence = collector reading + `collector_runs` + `observations`; and stating explicitly that
configuration activation **no longer creates** `resources` rows, only resolves against them. If any
part of Q1 v3 must change to fit, flag it as an open ruling for the user — do not amend a frozen
decision inside a V0.1 draft.

**Acceptance**: the mapping exists in the text; the write-path ownership of `resources` is stated in
one sentence; any Q1 v3 tension is listed under open rulings.

### D11-03 · P1 · New tables have no namespace / ownership

**Evidence**: §2.2 `credentials`, `packages`, `models`, `model_aliases`, `collector_runs` — none
carries a namespace or owner column. The rest of the engine is namespace-scoped
(`resource_pools.namespace` at `001_initial.sql`, `namespace_grants` at `001_initial.sql:62`, the
5-permission principal model), and AGENTS.md principle 4 requires multi-tenant-by-default design.
Today's quota API is `LocalOnly` single-user (`cmd/ant-server/main.go:400`), but the `/v1` engine is
not.

**Required**: either add namespace/owner to every new table and define the grant interaction
(who may read whose credentials), or state the single-user ruling explicitly and record what it
would take to lift later.

**Acceptance**: every new table has an ownership story consistent with the grant model, or a written
single-user ruling.

### D11-04 · P1 · The `role` enum has no landing point in the schema

**Evidence**: §2.1 gives Resource a role (`allowance`, `balance`, `rate`, `concurrency`), but the
§2.2 migration adds only `package_id` and `scope_json` to `resources`. The existing table has
`kind CHECK IN('counter','occupancy')` (`001_initial.sql:68`) and no window-rule or role columns —
windows/semantics live in `definition_json`/`semantics_*` today.

**Required**: state where role lives (new column with CHECK, or a definition_json field) and, in the
same breath, where the per-resource window rule and semantics version live. The §2.2 sketch must
round-trip every attribute §2.1 promises.

**Acceptance**: a reader can reconstruct each §2.1 entity's full storage from §2.2 with no gaps.

### D11-05 · P1 · `rate` and `concurrency` roles have no owner

**Evidence**: both roles appear in §2.1, but there is no collector pattern or example producing them,
and the execution side already owns concurrency (`metric.concurrency.request` in the DeepSeek
policy; dispatch permits; the ledger's reserved/active accounting). Rate limits surface as execution
outcomes (429s, provider rate headers), not as inventory.

**Required**: one paragraph: rate/concurrency readings are **execution-side facts** written by the
ledger/transport paths; collectors never poll or enforce them; the collector interface must not be
able to produce these roles (or templates mark them execution-owned). Specify how a provider-reported
rate signal (e.g., response headers) maps to an observation, if at all.

**Acceptance**: ownership stated; the `Collector` interface as sketched cannot fabricate rate/concurrency
readings.

### D11-06 · P2 · Shared / family / team plans are unaddressed

**Evidence**: design 07 has `VerifiedScope` "shared scope with alias"; design 11 merges credentials
only by per-credential provider identity (§3.3). Two seats on one family plan, or one org pool
billied to many keys, have no representation.

**Required**: state in/out of scope for V1. If out, record it as a non-goal with the Magpie guard
restated: shared billing identity only ever comes from provider-reported facts, never inferred from
coincident reset times.

**Acceptance**: scope decision + guard recorded.

### D11-07 · P2 · Three obligations named but not enforced or specified

**(a) Rule-only packages must be labelled declared-not-observed.** §2.3's Figma row has "no reading"
— but the package's size comes from user entry. The rejected inventory failed partly by letting
declared capacity masquerade as balance (design 09 already states "sample capacity must not masquerade
as real balances"). Name the UI/query rule that keeps `rule-only`/user-declared values visually and
API-wise distinct from collector readings.

**(b) Web-only "never routed" needs a typed guard.** Today it is a caption (§2.3). Name the enforcing
component: pool eligibility filters `binding.protocol` server-side (the `pools.FilterEligible` path),
so a web-only offer can never enter a candidate list regardless of UI.

**(c) Master key story.** Decision 1 says "a master key in the Keychain"; the current mechanism is
`-secret-master-key-file` (`cmd/ant-server/main.go:70`), and localtest mode force-pairs with it.
State whether Keychain storage is an extension or replacement, and what phase 1's `accounts.json`
secret import does when no master key is configured (bootstrap prompt? defer with explicit
credential status? Import must never store plaintext as a fallback).

**Acceptance**: three sentences in the text, one per item, each naming a mechanism not an intention.

### D11-08 · P2 · The retention fold has no landing table — and observations are not actually protected

**Evidence**: §3.3 "old readings are folded into daily summaries after 30 days, for burn rate and
forecast." There is no summary table in §2.2. Note: `observations` has **no** append-only trigger —
the immutability triggers exist on `resource_events` and `operation_events`/`operations` only
(`001_initial.sql:241-263`). So "fold" would mean mutating/deleting reading history with nothing
preventing it, which weakens provenance and audit (the burn-rate evidence chain).

**Required**: either add a daily-summary table and make `observations` append-only (trigger, matching
`resource_events` discipline) with the fold writing summaries and keeping raw rows, or define the
fold as a projection over retained raw rows (no deletion). Either way: name where burn rate and
forecast read from, and keep `source_ref`/`evidence_json` intact for whatever remains.

**Acceptance**: the fold's write path and the forecast's read path are both named; reading history is
either immutable-by-trigger or explicitly projected-only.

### D11-09 · P3 · Factual corrections (the doc's credibility rests on these being right)

| Claim in design 11 | Measured 2026-10-08 | Fix |
|---|---|---|
| "the ~30 `discover*` functions" | 17 free functions + 1 method in `internal/quota` | correct the number (or state the counting basis) |
| "`resource_states` and `observations` already store windowed readings with provenance (`source_ref`, `evidence_json`, `valid`, `expires_at`)" | provenance columns exist **only** on `observations` (`001_initial.sql:206`); `resource_states` (`:111`) is the aggregate with no provenance | reword to "observations stores provenance; resource_states is the ledger-owned aggregate" |
| (implicit) resources already model role/window | `resources.kind` is CHECK `('counter','occupancy')` only; role/window would be **new** | make explicit what is reuse vs new |

Verified-correct claims (no action): `mergeSame` (`quota/service.go:300`), `hideFailedWays`
(`quota/service.go:345`), `parseKlingPoints` (`quota/creative.go:93`), `parseManus`
(`quota/services.go:117`), browser `routes()` (`webui/static/app.js:344`), `WindowAt`
(`domain/resources/window.go:91`), accounts.json stores only credentials, `secret_records`/`secret_refs`
exist, design 05/09 references valid.

### D11-10 · P2 · Coordination: migration numbering, the still-shipped supply inventory, and the STATUS entry

- §2.2 numbers its migration `005_supply_engine.sql`; [ENG-FINDINGS](ENG-FINDINGS-2026-10-08.md)
  ENG-03 proposes `005_drop_supply_inventory.sql` (retiring the rejected direction's table/route —
  data drop needs user confirmation). One batch should carry one coordinated migration plan; note the
  table-count arithmetic for `migrate.go:39` (`expectedTableCount`): 36 + 5 new − 1 dropped = 40.
- Design 11 replaces `internal/quota` but never mentions the **supply_inventory** surface
  (`/v1/inventory` at `cmd/ant-server/main.go:385`, `internal/domain/supply`, migration 004's table)
  — the phase plan should fold its retirement in (ENG-03), or state why it lingers.
- When implementation starts, STATUS.md needs the increment entry + evidence directory (ENG-07's
  standing violation). A design draft alone does not satisfy it.

---

## Standing guards for the revision

- Do not revive the rejected wizard/inventory as the bridge or baseline (user rulings 2026-10-07,
  verbatim in USER-ORIGINAL-REQUIREMENTS §二.4–5).
- Do not amend frozen decisions (design 07 Q1 v3) inside this draft — list tensions as open rulings.
- Collectors read accounts' own usage/balance only; nothing spends quota; no real egress beyond
  read-only observation, and no real credentials in examples.
- New code respects the ≤500-line rule (ENG-05) — keep descriptor/parsers/strategy files small.
- Keep gate claims honest: if the design's package layout cannot pass `make check` as written, the
  design must say which rule changes and why (that is D11-01).

## Acceptance for the V0.2 draft

1. Every D11-01…D11-10 resolved in text or listed as an open ruling with owner.
2. A changelog section mapping each finding ID to what changed.
3. All code citations re-verified (the appendix above is the checklist).
4. Migration sketch arithmetic consistent with the ENG-03 coordination.
5. The keep-list (top of this document) survives intact.
