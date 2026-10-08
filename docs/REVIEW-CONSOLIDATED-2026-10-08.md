# Consolidated reviewer opinion — 2026-10-08

Consolidates four rounds into one position statement for the implementing agent to study:

1. [ENG-FINDINGS-2026-10-08](ENG-FINDINGS-2026-10-08.md) — implementation findings (ENG-01…09);
2. [REVIEW-DESIGN-11-SUPPLY-ENGINE](REVIEW-DESIGN-11-SUPPLY-ENGINE.md) — design 11 V0.1 review (D11-01…10);
3. the fix-batch independent acceptance (this document §1–2, previously reported in conversation only);
4. the interface/architecture direction (this document §4).

Decisions marked **ruling** belong to the user. Everything else here is reviewer position: study it,
push back with evidence where it is wrong, and carry the settled parts into design 11 V0.2.x and the
phase plan. Nothing in this document asks for code before the user rules on R1–R3 (design 11 V0.2 §8).

---

## 1. Fix-batch acceptance (verified 2026-10-08, final tree)

**Accepted.** Verified independently, not by re-reading claims:

- All four gates re-run by the reviewer on the final tree: `make check` green (`check-deps:
  dependency boundaries OK (31 packages)`), `test-contract` 23/23 packages `-race`, `test-integration`,
  `test-fault` — all pass.
- All ten claimed code fixes verified with file:line evidence (webui embed-only + `api.PageHandler`;
  `quota/cookiestore` read-only narrow package; `check-deps.py` written exemptions with sunset;
  `LocalOnly` Host **and** loopback-peer checks with a foreign-peer test; origin-pinned `readerClient`
  refusing cross-host redirects and HTTP downgrade; the three oversized quota files split, max now
  451 lines; cross-browser cookie recency unified on one clock with a regression test that fails on
  the old arithmetic; STATUS entry; ENG-06 audit; stray `--help` deleted). `node_modules` deletion
  correctly awaits the user.
- Every counter-claim against both reviews was checked against code and **confirmed** (see §2).
- Honest-claims check: the 291-entry manifest drift is declared, not re-signed, per the R14/B6 guard —
  correct behavior.

## 2. Errata against the reviewer's own documents (on the record)

The implementer's calibrated disagreement was right in all four cases; the reviewer confirms and
records the errors:

| Review said | Code says |
|---|---|
| ENG-06 "known-good": LocalOnly guards the surface | It guarded only the `Host` header (spoofable from a LAN with a non-loopback `-addr`); and it wrapped the page, not the API. Fixed with the peer check. |
| ENG-06 "known-good": cookies stay Chrome-encrypted | True only for Ant's own sign-in profile; for everyday browsers Ant reads the Keychain key and decrypts in-process. The citation covered the profile, the wording implied more. |
| ENG-04 "no bridge exists" | The bridge port exists (`config/compile.go:73` `resolveResource` → `ResolveScopeFact`/`ResolveDimensionalFact` over `domain.VerifiedResourceFact`); what was missing is a real fact *source*. |
| D11-02 remedy (activation stops creating `resources` rows) | Harmful: `config_projection.go:179` `disableMissing` silently disables rows the active config does not name; changing accepted activation code was also against the review's own guard. The three-stores-one-writer design (V0.2 §2.1) supersedes it. |
| D11-08 remedy (append-only trigger + fold on `observations`) | Harmful: `observations` is the ledger's admission input (`prepare_statements.go:177` reads it during prepare), not a reading log. Superseded by the new append-only `supply_readings`. |
| ENG-FINDINGS standing guard "no outbound calls" | Over-applied: the shipped, user-directed quota reader performs read-only, credentialed balance reads at startup and every 10 min (`QUOTA.md`, `make run`). The guard's correct scope is **billable/execution egress** (IN-01…04/G0 gates), not read-only observation. See R4. |

The implementer additionally found and fixed three real bugs neither review caught (Host-only guard,
keys in custom headers following cross-host redirects, cross-browser clock mismatch), and owned four
V0.1 design errors (collectors writing ledger `observations`; a parallel provider model; an invented
`role` field; negative balances/money quantum). This verify-then-act, disagree-with-evidence norm is
exactly what previous acceptance rounds were staged to produce — keep it.

## 3. Position on the open rulings (recommendations; owner: user)

| ID | Recommendation | Reasoning / conditions |
|---|---|---|
| R1 provider-module ownership | **Accept** the proposal (§3.1): one package per provider owning execution + supply; `internal/quota` retires in phase 4; freeze respected until ruled | This is the Magpie provider-module model; resolves ENG-02 at its root |
| R2 verified-fact bridge | **Accept** (§2.1): supply store projects `VerifiedScope`/`VerifiedResourceFact`; activation stays sole writer of configuration tables | Consistent with frozen design 07 Q1 v3; the port already exists and has an API-level consumer interface (`api/catalog.go:13` `VerifiedScopeSource`) |
| R3 time-varying `capacityBound` | **Accept** the reading: identity (`physical_key`, dimensions, window) stable and pinned by activation; `capacityBound` taken at resolution and re-checked at activation | Matches Q1 v3's own text (activation re-checks evidence). One explicit requirement: if a re-read shrinks `capacityBound` below an already-activated `configuredCapacity`, the **next activation must fail** with the existing mismatch rule — never silently pass |
| R4 live provider reads | **Keep**, with the boundary written down: read-only balance/usage reads with found credentials are product behavior (user-directed, shipped); **billable/execution calls remain gated** by IN-01…04/G0–G1. Record the ruling in STATUS and correct the ENG-FINDINGS guard wording | The contradiction was in the guard's scope, not in the product |
| R5 plaintext `accounts.json` until phase 1 | **Accept** temporarily | Loopback-peer guard + 0600/0700 perms hold the line; phase 1's import has a no-plaintext-fallback guarantee and a defined `needs_key` path (§5) |
| R6 everyday-browser cookies | **Accept per-site opt-in**, remembered in `supply_credentials` (`status = hidden` until enabled) | Decrypting everyday-browser cookies is Ant's most invasive act; opt-in is the right privacy posture. Cost: some providers lose the zero-config path — acceptable, most have CLI/config alternatives |
| R7 drop `supply_inventory` | **Accept drop** (one-way) | Direction rejected twice by the user; only demo/local data exists; migration history stays append-only; folded into phase 0 (§6 V0.2) |
| Archive `node_modules` (164 MB) | **Approve deletion** | Lockfile retained, reinstallable; sources and evidence untouched |

## 4. Design discipline for implementation: the interface doctrine

The user's direction — "define interfaces at the APP layer and at the provider/resource module layer;
concrete modules implement them" — is already this tree's house style, and it should be written down
as a binding constraint for the design 11 phases:

**Already in place (evidence, keep as the norm):**
- Module-level small behavior interfaces: `planning.ProviderPolicy` (two implementers: DeepSeek,
  localtest), `adapters.ProviderAdapter` (two implementers), `config.ProviderCatalog`,
  `transport.Port`, `ledger.Ledger`, store Tx interfaces.
- APP-level consumer-side, per-use-case slicing: `application/operation_executor.go:26-44`
  (`ExecutionPlanner`, `ExecutionLedger`, `AdapterLookup`, `GuardEvidenceProvider` over a 21-method
  Ledger port); `operation_controls.go:25-89` slices the same port into `adminLedger`/`cancelLedger`/
  `reconcileLedger`; the api package defines 16 small command interfaces (`OperationSubmitCommands`,
  `PoolOrderCommands`, …), all satisfied implicitly by application services.
- Assembly: `application/provider_registry.go` binds each module's catalog + adapter + policy at
  startup and fails fast on incompleteness — the registration point that makes "modules implement
  interfaces" enforceable without `implements` keywords.

**Doctrine (four rules):**
1. **Interfaces belong to the consumer, not the implementer.** Never define a big `Provider`
   interface in a provider package for everyone to implement. V0.2's shape is correct and should be
   preserved: *data as structs* (`Supply`, `PackageTemplate` — most provider differences are data),
   *behavior as small interfaces* (`Collector`, `Fetch`, `ModelLister`). Forcing Kling/Figma-shaped
   providers through a many-method interface recreates the rejected `Account` blob in interface form.
2. **Second real implementer, or true layer edge, or no interface** (AGENTS.md principle 1). A seam
   qualifies if it has (or will immediately gain) two implementations, or if it is a structural layer
   boundary (store/sqlite, transport, api) where test/fault substitution is the reason. A single
   implementation with a single consumer is speculative abstraction — write the concrete type.
3. **Registry assembly with fail-fast completeness checks**, per `provider_registry.go`; the supply
   engine's module registration should reuse this shape (`providers/catalog` aggregates for both the
   compiler and the supply engine, §3.1 V0.2).
4. **New code stays ≤500 lines per file** (ENG-05); split at phase boundaries, not after.

**Interfaces to add during design 11 implementation (each with consumer + implementer):**

| Interface | Defined in | Implemented by | Consumed by |
|---|---|---|---|
| `SupplyQueryCommands` | `internal/api` | application supply query service | `/v1/supply/*` read handlers |
| `SupplyControlCommands` (add/remove credential, apply pool proposal, request refresh) | `internal/api` | same service | `/v1/supply/*` write handlers |
| `FactSource` (project `supply_*` rows → `VerifiedScope`/`VerifiedResourceFact`) | `internal/config` or `providers/catalog` | supply catalog source | config compiler (the R2 bridge) |
| scheduler clock | reuse the existing `application.Clock` pattern | runtime clock | supply scheduler |
| per-strategy discovery (`Discovery` per strategy id) | `internal/supply/discover` | one small impl per strategy | scheduler/reconciler |

Do **not** add: a `Reconciler`/`Scheduler` interface for their (single) concrete implementations; a
`Provider` mega-interface; interfaces for tables/repositories beyond the existing store-port pattern.

## 5. Overall opinion on the direction

design 11 V0.2 is the first design in this project's history that simultaneously: (a) encodes the
user's original requirements without the two rejected distortions (accounts-as-subscriptions,
internal-accounting-forms); (b) builds on the accepted kernel's real ports instead of inventing a
parallel universe (the V0.1 mistake, self-caught); (c) keeps declared and observed facts structurally
separate (`basis`, §3.6 — the guard that would have prevented the rejected inventory's core failure);
and (d) reaches the router through Q1 v3 as frozen, with the one genuine tension surfaced as R3
rather than silently amended. The phase plan's fixture-parity rule (a provider moves only when its
collector passes the tests its reader has today) matches this project's acceptance culture.

The reviewer's overall judgment: **proceed** — after the user rules R1–R3 (V0.2's own gate), with the
interface doctrine of §4 as a stated constraint, and with R4–R7 settled at the user's convenience
(none of them blocks design work; R7 gates phase 0's migration only).

**First user-visible payoff to aim at:** the phase-3/5 read-only route preview ("for model M, which
accounts can serve it and why, in what order") — it is the moment the whole supply chain becomes
checkable by the user without any billable egress, and it should be treated as the batch's
acceptance demo.

## 6. Watch items (non-blocking; assign at phase start)

1. `models`/`model_aliases` are deliberately global (catalog data), but `source='user'` aliases are
   user-authored and currently not namespaced — namespace them in phase 3 or record the exception.
2. Safari cookie recency uses cookie creation time, not last-used (documented approximation) — fine;
   keep the comment honest if the heuristic changes.
3. `store/sqlite/migrate.go` comment says "34 initial tables" while the count is 36 — fix at the next
   functional touch of that file (implementer's own note).
4. Manifest drift (291 entries + the check-deps edit) will need one formal re-sign ruling eventually
   (R14/B6) — not now, but do not let it silently grow past the point where a diff review stays
   feasible.
5. V0.2 §3.5 moves the dashboard from LocalOnly `/api/*` to namespace-scoped `/v1/supply/*` behind
   AuthMiddleware — a product-visible change (today's page needs no login). Plan the transition
   (bootstrap admin token, demo mode) so the page never loses a working path between phases.
6. Collector token refresh ("renewal done by the engine") is still one sentence — spec it when phase
   2 lands, including where rotated tokens persist and how `verify` capability relates to it.
7. ENG-05's quota file splits are verified by tests but not byte-verifiable (no git) — acceptable;
   note that this is one more argument for eventually putting the tree under version control, which
   is the reviewer's standing recommendation independent of any finding.

## 7. Standing guards (unchanged)

- No revival of the rejected wizard/inventory direction (user rulings 2026-10-07, verbatim in
  USER-ORIGINAL-REQUIREMENTS §二.4–5). `basis: declared` must never decay into a wizard.
- No billable/execution egress before IN-01…04 and the G-gates; read-only observation per R4's scope.
- No amendment of frozen decisions (design 07 Q1 v3) inside drafts — surface tensions as rulings.
- No manifest re-signing without a diff review; gate claims must match actual runs.
- No speculative abstraction (§4 rule 2); no compatibility shims for retired internals (AGENTS.md
  principle 10).

## 8. Suggested next sequence

1. User rules R1–R7 (§3 table is a ready-to-adopt ballot).
2. Claude folds §3 (as confirmed) + §4 (doctrine + interface table) into design 11 V0.2.x with a
   changelog; no phase-0 code before that.
3. Phase 0: ENG-03 retirement (drop `/v1/inventory`, `domain/supply`, migration drop with
   `expectedTableCount` arithmetic) — only after R7 is confirmed.
4. Phase 1 per V0.2 §6, with the interface table of §4 landed as part of the store/api work.
5. Every phase ends with the four gates green plus a STATUS entry and evidence directory — the same
   bar this fix batch just met.
