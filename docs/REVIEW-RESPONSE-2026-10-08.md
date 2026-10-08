# Response to the 2026-10-08 reviews

Responds to [ENG-FINDINGS-2026-10-08](ENG-FINDINGS-2026-10-08.md) (implementation) and
[REVIEW-DESIGN-11-SUPPLY-ENGINE](REVIEW-DESIGN-11-SUPPLY-ENGINE.md) (design). Every claim was
re-checked against the tree on 2026-10-08 before acting. Verdicts: **confirmed** (right, acted on),
**partly wrong** (the problem is real; a citation, scope or remedy is not), **wrong**.

## 1. ENG-FINDINGS — verdicts and actions

| ID | Verdict | What was wrong in the finding | Action |
|---|---|---|---|
| ENG-01 | confirmed, under-scoped | Quota's `net/http` is not only "provider readers": it also serves its own `/api/*` API and runs a one-off loopback listener for Bailian console login. Option B as worded ("read-only provider observation") would silently cover both. `webui` needed no exemption at all. | Fixed. `webui` is embed-only; the page handler moved to `api.PageHandler`. Chrome/Firefox cookie SQL moved to `quota/cookiestore`, the only SQL on the quota side. `check-deps.py` records two narrow, written exemptions; quota's is temporary (ends with design 11 phase 4) and lists all three uses. `make check` green. |
| ENG-02 | confirmed | Misses that the execution side already has the host: `domain.ProviderDescriptor`, `providers/catalog`, and the Q1 v3 verified-fact port. | Proposal in design 11 V0.2 §3.1; **ruling R1 needed**. Freeze respected. |
| ENG-03 | confirmed | — | Not executed: dropping data is one-way. **Ruling R7 needed.** |
| ENG-04 | partly wrong | "Nothing converts an observation into executable supply" — the *port* exists (`VerifiedScope`/`VerifiedResourceFact`, `ResolveScopeFact`/`ResolveDimensionalFact`); what is missing is a real fact *source*. | Design 11 V0.2 §2.1 makes the supply store that source; **ruling R2**. |
| ENG-05 | confirmed | — | Fixed for quota: `service.go` 818 → 297 (+ `service_edit.go`, `service_merge.go`, `service_http.go`), `aliyun.go` 712 → 442 (+ `aliyun_billing.go`, `aliyun_openapi.go`), `volcengine.go` 543 → 371 (+ `volcengine_signin.go`). Pure moves. Accepted files untouched, as advised. |
| ENG-06 | partly wrong | Two "known-good" items are not: (1) `main.go:400` wraps the **page**, not the API (the API's guard is inside `Service.Handler`), and that guard checked only `Host`, so with a non-loopback `-addr` another machine could send `Host: localhost`; (2) "cookies remain encrypted as Chrome encrypts them" holds only for Ant's own profile — for everyday browsers Ant reads the Keychain key and decrypts in-process. Q4 also missed that custom-header keys follow cross-host redirects. | Audit: [evidence/WORK/ENG-06](evidence/WORK/ENG-06/README.md). Fixed: loopback-peer check, origin-pinned reader client. Plaintext `accounts.json` → **ruling R5**; per-site browser opt-in → **ruling R6**. |
| ENG-07 | confirmed | — | STATUS.md entry added (2026-10-08 review-fix batch). |
| ENG-08 | confirmed | — | [design/10-HOOKS.md §2.1](design/10-HOOKS.md) states the production wiring; no framework code changed. |
| ENG-09 | confirmed | — | `--help` deleted (verified to be `snapshot.py` output with empty `generatedAt`). Archived `node_modules` (164 MB, lockfile present) **not deleted yet — awaiting your OK**. |
| Guard "no outbound calls" | wrong as applied | The shipped, user-directed quota reader makes credentialed provider reads at startup and every 10 min (`QUOTA.md`, `make run`). The guard and the product contradict each other; a review should flag that, not assume it. | **Ruling R4.** No behavior change. |

## 2. REVIEW-DESIGN-11 — verdicts

| ID | Verdict | Notes |
|---|---|---|
| D11-01 | confirmed | V0.2 §3.2: injected `Fetch` from `transport`, package/edge table, exemption sunset. |
| D11-02 | **remedy wrong**, problem understated | The review requires "configuration activation **no longer creates** `resources` rows". That rewrites accepted activation code (`config_projection.go`) and the frozen design it implements — against the review's own guard. The problem is also wider: V0.1 wrote `accounts` and `resource_offers` too, and activation's `disableMissing` would silently disable those rows. V0.2: separate supply store, activation remains sole writer, bridge through the existing verified-fact port (§2.1–2.2). |
| D11-03 | confirmed | Namespace on every supply table. |
| D11-04 | confirmed gap, remedy partly wrong | A `role` column with CHECK would duplicate `RegisteredSemantics{counter|occupancy, limit|entitlement}`. V0.2 drops `role` and uses registered semantics (§2.3). |
| D11-05 | confirmed | Occupancy/rate are execution-owned; reconciler rejects them. |
| D11-06 | confirmed | Out of scope for V1; never inferred. |
| D11-07 | (a)(c) confirmed; (b) partly wrong | `pools.FilterEligible` exists, but the primary guard is compile time (`SupportsDomain` in `config/document.go`): a web-only offer never becomes a `bindings`/`resource_offers` row. V0.2 names both. |
| D11-08 | fact right, remedy wrong | Right that `observations` has no append-only trigger. But the root error is V0.1's: `observations` is the ledger's watermark-bound admission input, not a reading log. Adding a trigger and folding into it would change accepted ledger schema and still mix display history into admission. V0.2: new append-only `supply_readings`, appended on change, no fold. |
| D11-09 | confirmed | 18 discover functions; provenance wording fixed. |
| D11-10 | confirmed | Arithmetic was right for V0.1 (5 tables); V0.2 gives both outcomes (§6). |
| Keep-list | mostly confirmed | "Reuse `offer_resources`" — that table is activation-owned; supply keeps `draws_json` until an offer is adopted. "Decision 1: master key in the Keychain" — kept as an optional later extension; the file key is the phase-1 mechanism (§5). |

## 3. Missed by both reviews

Mine to own — design 11 V0.1 errors the review did not catch:

1. **`observations` misuse.** "Collectors write observations" cannot work: FK to a configured
   `resource_states` window, ledger watermarks, `remaining ≥ 0` only, written by
   `RecordObservation` only. (Design 11 V0.2 §2.1.)
2. **Parallel provider model.** V0.1's `Provider` descriptor ignored `domain.ProviderDescriptor`
   and the verified-fact port — the same two-owner pattern ENG-02 rejects. (§1, §3.1.)
3. **Invented `role`** duplicating registered semantics. (§2.3.)
4. **Money and sign.** Balances can be negative (arrears); money needs `quantum_exp`. (§2.4.)

In the implementation (found while verifying ENG-06, all fixed with regression tests):

5. **Host-only local guard.** Fixed: `LocalOnly` also requires a loopback peer
   (`TestChangesAreGuarded`, "foreign peer").
6. **Keys in custom headers followed cross-host redirects.** Fixed: `readerClient()`
   (`TestReaderClientDoesNotCarryKeysAcrossHosts` and two siblings).
7. **Cross-browser cookie recency compared two clocks.** Chromium access times were converted to
   the 1970 epoch while Firefox/Safari used 1601, so Firefox/Safari always won. Fixed on one clock
   in `cookiestore` (`TestBrowserCookieRecencyComparesAcrossBrowsers`; confirmed failing on the
   old arithmetic).

Note only: `store/sqlite/migrate.go` comments describe the table count as "34 initial tables plus
the … index"; the query counts tables only (34 + `secret_refs` + `supply_inventory` = 36). Accepted
source; left until next functional touch.

## 4. Verification (final tree, 2026-10-08)

| Target | Result |
|---|---|
| `make check` | pass — `check-deps: dependency boundaries OK (31 packages)` |
| `make test-contract` | pass — all internal packages `ok`, `-race -count=1` |
| `make test-integration` | pass |
| `make test-fault` | pass |

## 5. Rulings needed (owner: the user)

R1 provider-module ownership (ENG-02) · R2 verified-fact bridge (ENG-04) · R3 Q1 v3 reading of
time-varying `capacityBound` · R4 live provider reads vs "no outbound calls" · R5 plaintext
`accounts.json` until phase 1 · R6 per-site opt-in for everyday-browser cookies · R7 drop
`supply_inventory` data (ENG-03). Details and proposals: design 11 V0.2 §8.

## 6. Response to REVIEW-CONSOLIDATED-2026-10-08

Every citation in [the consolidated review](REVIEW-CONSOLIDATED-2026-10-08.md) was re-checked;
all hold (`compile.go:73`, `config_projection.go:179`, `prepare_statements.go:177`,
`api/catalog.go:13`, `operation_executor.go:26-44`, `operation_controls.go:25-89`,
`provider_registry.go` fail-fast). Minor: `internal/api` has 15 interface declarations, not 16.
Adopted into [design 11 V0.2.1](design/11-SUPPLY-ENGINE.md): the §4 interface doctrine (§3.9),
watch items 1, 5 and 6 (§2.4, §3.5, §3.8).

Where it is wrong, with evidence:

| Item | Review position | Code says | Resolution |
|---|---|---|---|
| R3 condition | Accept "capacityBound re-checked at activation", failing the next activation only if it shrinks below `configuredCapacity` | `checkEvidenceQuantity` requires evidence to **equal** the fact (`evidenceConflictsVerifiedFact`), and `ActivateDraft` recompiles against the catalog inside the write transaction. Against a live source, *any* change fails activation; for balances, every spend does. My own V0.2 wording had the same flaw. | Design 11 §2.2.1: facts are immutable reading snapshots (`factId = resource@reading`); supply-made drafts pin one; freshness flows through `RecordObservation`. No Q1 v3 amendment. |
| `FactSource` placement | `internal/config` or `providers/catalog` | `config.ProviderCatalog` is already the compiler's interface and `providers/catalog` already aggregates facts — but only from the source that owns the provider type (`catalog.go`), which supply facts cannot satisfy | `providers/catalog`, as a separately registered input. |
| Interface table | five interfaces | misses the store capability (`store.SupplyWriterTx`, the `AdminWriterTx` pattern) and the scheduler's ledger seam (`RecordObservation`) | added to §3.9. |
| Watch 6 | renewal "is still one sentence" | V0.2 dropped the sentence entirely — the gap was larger | §3.8: found credentials are never renewed; Ant-owned ones rotate as new `secret_records` revisions. |
| Watch 5 | plan the move to `/v1` auth | needs a user decision on whether a loopback auto-session is acceptable | new ruling **R8**; proposal: keep the existing one-time bootstrap token → persistent session. |

Not acted on (user decisions): R1–R8 remain open; the reviewer's ballot is recorded next to each
proposal in design 11 §8. Archived `node_modules` deletion and putting the tree under git
(watch 7) await the user.

## 7. Response to REVIEW-PHASE0-PHASE1-2026-10-08

All sixteen findings were re-verified against the tree before acting; every one holds. One addition:
F-13 was confirmed with `EXPLAIN QUERY PLAN` (the tiebreaker forced `USE TEMP B-TREE FOR LAST TERM
OF ORDER BY`; without it the index alone serves the query).

History note: F-01 rewrote the two-commit history, so the review's hashes changed —
`890052b` → `bc97a4e` (baseline without `server/ant-server`, `.gitignore` gains `server/ant-server`
and `data/`) and `020e965` → `659d18c` (identical tree apart from those two). `.git` 17 MB → 3.1 MB.

| ID | Action | Commit |
|---|---|---|
| F-01, F-10 | binary removed from history; ignore entries added | rewritten baseline |
| F-04, F-09, F-15 | seven live docs put in past tense with the v5 data-loss note; future versions relative; ENG-03 evidence lists every grep match | `Housekeeping: …` |
| F-02, F-03, F-06, F-07, F-08, F-13, F-14 | see the commit message; v6 amended in place (never applied anywhere) | `Supply store: namespace-bound secrets…` |
| F-03 decision | (a) schema-enforced: composite FK via a unique index on `secret_refs`; the accepted table's definition is unchanged | same |
| F-06 decision | per-namespace uniqueness; key value stays globally derived (design 11 §2.2) — a global UNIQUE would also leak cross-tenant existence | same |
| F-05 | deferred to phase 2 with its first consumer; stated in the evidence README and design §3.3 | `Design 11: …` |
| F-11, F-12 | reading-order policy (`observed_at` is Ant's clock; out-of-order = skipped, run `ok`) and engine-side size caps in design §3.3 | `Design 11: …` |
| F-16 | adopted: one commit per planned step from here on (this batch: four) | — |

One thing the fixes surfaced: the v6 index on `secret_refs` also had to be removed by the
integration tests' rollback helper (`dropSupplyStore`), otherwise simulated v2–v5 databases failed
to upgrade. Real v2–v5 databases never have that index; the failure was in the fixture only.
