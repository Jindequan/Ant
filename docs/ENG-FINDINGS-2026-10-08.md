# Engineering findings — current-tree review (2026-10-08)

Purpose: the complete problem list found by an independent review of the current tree, written for the
implementing agent (Claude) to **review → confirm → fix**. Each item carries evidence (paths and line
numbers verified on 2026-10-08), the ruling or rule it violates, a recommended disposition, and
acceptance criteria. Nothing in this document is yet a ruling; items marked **decision** need an
explicit user ruling before code changes.

Produced by: read-only tree review plus fresh verification runs on 2026-10-08 (see snapshot below).
No application code, schema, or data was changed by this review.

Standing entry rule: **re-read [STATUS.md](plans/STATUS.md) before starting any item** — it remains the
sole progress source. Evidence for fixes goes under `docs/evidence/WORK/<ITEM-ID>/`, and each fix adds
or updates its STATUS.md entry.

## Verification snapshot (2026-10-08, fresh runs)

| Target | Result |
|---|---|
| `make check` | **FAIL** — `check-deps.py` reports 4 dependency violations (see ENG-01) |
| `make test-contract` | pass — all 23 internal packages `ok`, `-race -count=1` |
| `make test-integration` | pass — `ok ant/server/tests/integration 21.2s`, `-race` |
| `make test-fault` | pass — fault suite + 3 targeted regression groups `ok`, `-race` |

There is no git history in this repository; source identity is established via the existing
manifest/hash convention (`make verify-manifest`, `scripts/snapshot.py`).

---

## ENG-01 · P0 · `make check` is red: dependency-boundary violations (fix now — type: violation)

**Evidence** (fresh run of `make check` on 2026-10-08):

```
check-deps: DEPENDENCY VIOLATIONS:
  -> ant/server/internal/quota -> database/sql (forbidden outside ['ant/server/internal/store', 'ant/server/internal/store/sqlite'])
  -> ant/server/internal/quota -> modernc.org/sqlite (forbidden outside ['ant/server/internal/store/sqlite'])
  -> ant/server/internal/quota -> net/http (forbidden outside ['ant/server/internal/api', 'ant/server/internal/transport'])
  -> ant/server/internal/webui -> net/http (forbidden outside ['ant/server/internal/api', 'ant/server/internal/transport'])
```

**What introduced it**: the in-flight quota/webui increment (files dated 2026-10-07 → 10-08).
`internal/quota/browsercookies.go:10` imports `database/sql` (reads the Chrome cookie SQLite store
directly); `internal/quota` imports `net/http` in its provider readers (`models.go`, `services.go`,
`volcengine.go`, …); `internal/webui` is a `net/http` handler package.

**Rule violated**: the Makefile header freezes target names per
[EXECUTION-STANDARD](plans/EXECUTION-STANDARD.md); `scripts/check-deps.py` enforces the T1 dependency
boundaries (SQL only in store layers, HTTP only in api/transport). Historical STATUS entries for every
prior batch claim `make check` green — the current tree no longer satisfies the project's own gate.

**Disposition (pick one, needs a written ruling in STATUS.md either way)**:

- **Option A — conform the code.** Move quota's durable data behind a store-layer port (or keep it
  file-based and eliminate the `database/sql` import); route provider reads through the transport
  package with per-provider endpoint allowlists, mirroring how DeepSeek balance observation is built
  (`application/deepseek_balance.go` + `transport.NewHTTPClientWithAllowedEndpoints`).
- **Option B — amend the boundary contract explicitly.** Keep quota as a self-contained observation
  subsystem: it may own `net/http` for read-only provider observation (never execution egress), and it
  must not use SQL (its persistence stays the `~/.config/ant/accounts.json` file store,
  `internal/quota/store.go`); encode the exception in `check-deps.py` with the justification, and fix
  the `database/sql` import regardless.

Recommendation: **B**, with the `database/sql` import eliminated in all cases (reading the Chrome
cookie store can be isolated behind a narrow platform helper so quota itself stays SQL-free), because
quota is an observation surface, not the execution core, and forcing 20 provider readers through the
controlled transport today would delay the direction the user chose. The rule change must be visible
(code + comment in `check-deps.py` + STATUS entry), not silent.

**Acceptance**: `make check` exits 0; the dependency rule text matches reality; a STATUS.md entry
records which option was taken and why.

**Scope guard**: do not refactor accepted store/ledger sources to achieve this.

---

## ENG-02 · P1 · Two parallel provider subsystems that do not share semantics (decision + freeze divergence)

**Evidence**:

- New subsystem: `internal/quota` — 41 Go files (32 non-test), ~20 provider modules (aliyun, chatgpt,
  claude, copilot, gemini, kimi, minimax, mimo, opencode, suno, volcengine, zai, deepseek, design
  tools, …), its own HTTP reading, its own JSON file store, its own sign-in discovery (browser
  cookies / other apps' config files), not registered in `ProviderRegistry`
  (`internal/application/provider_registry.go`).
- Old subsystem: `internal/providers/{catalog,deepseek,localtest,chatcompletions}` +
  `application/deepseek_balance.go` + `internal/adapters` + `internal/transport` — registered,
  allowlisted, execution-oriented; knows only DeepSeek and localtest.
- No shared model for account identity, credential/reference, or observation between the two; the
  same real-world provider is modeled twice with different types and storage.

**Why it matters**: the accepted research direction
([MAGPIE-AND-ANT](research/MAGPIE-AND-ANT.md) §Proposed architecture) requires **one provider module
to own its complete semantics** — configuration fields, access paths, plan interpretation, quota
scopes/time rules, observation collection, response interpretation. Two half-owners per provider is
the "warped style" failure mode the user rejected.

**Disposition**: **decision item.** Before any new provider is added on *either* side, decide the
target module shape (what a unified provider module implements, which subsystem survives as its host,
and how the DeepSeek execution adapter and the quota readers merge). Until ruled: **freeze** — no new
providers in either subsystem, no cross-wiring hacks.

**Acceptance**: a written ruling (STATUS entry + design note appended to MAGPIE-AND-ANT.md or a new
design doc) plus a migration plan; only then implementation.

---

## ENG-03 · P1 · The rejected supply-inventory direction is still shipped in the binary (retire — needs user confirmation on data drop)

**Evidence** (all live and wired):

- Table `supply_inventory` — `server/migrations/004_supply_inventory.sql`; table count hard-checked
  at `internal/store/sqlite/migrate.go:39` (`expectedTableCount = 36`).
- Route: `mux.Handle("/v1/inventory", …)` at `cmd/ant-server/main.go:385`.
- Code: `internal/api/inventory.go`, `internal/application/inventory.go`, `internal/domain/supply/`,
  `internal/store/sqlite/inventory.go` (+ `store.Port` inventory methods and their tests).

**Rulings in force**:

- STATUS.md 2026-10-07: "supply inventory product direction rejected; research/discussion now
  current … The previously delivered universal package wizard … cannot serve as the next product
  baseline."
- AGENTS.md principle 10: internal-path rework deletes; no compatibility shims.
- Constraint from [MAGPIE-AND-ANT](research/MAGPIE-AND-ANT.md) §First business slice step 2: retire
  "the rejected primary form only through the discussed replacement plan." The rejected React
  frontend is already archived (`/Users/devin/code/Ant/.archive/2026-10-07-old-frontend/`); this
  backend API is the remaining surface.

**Recommended disposition**: full retirement of the remaining backend surface —
remove the route registration, `api.InventoryHandler`, `application.InventoryService`,
`internal/domain/supply`, the store inventory statements/port methods, and add migration
`005_drop_supply_inventory.sql` dropping the table (`expectedTableCount` 36 → 35). Migration history
001–004 stays intact (append-only). Only demo/local data exists in `supply_inventory`
(`data/demo.db`, `data/ant.db` are local dev files) — **one-way data drop, so confirm with the user
before executing the migration part**. If the user prefers deferral, the lighter option is code
removal with the table left dormant; that keeps a dead table and is not the recommendation.

**Acceptance**: no `/v1/inventory` route or inventory symbol remains (`grep -ri inventory server/`
returns only unrelated matches); fresh migrate from v0 and upgrade from a v4 database both pass;
`make check test-contract test-integration test-fault` green; STATUS.md records the retirement and
the user's confirmation.

---

## ENG-04 · P1 · No bridge between observed supply and executable configuration (design gap — input to the next slice, not a same-batch fix)

**Evidence**: quota observations live in `~/.config/ant/accounts.json` + process memory
(`internal/quota/store.go`); the executable model is ConfigDocument v1 (`internal/config/document.go`,
`config_drafts`/`active_config` tables) with pools/planner reading only it
(`application/candidate_planner.go`). Nothing converts a provider-native observation (subscription
window, balance) into executable supply.

**Why it matters**: the product goal is configure-first
([USER-ORIGINAL-REQUIREMENTS](USER-ORIGINAL-REQUIREMENTS.md) §一.8: provider → account →
plan/package → resource/limits → pool → priority, consumption next), and MAGPIE stage 2–3
(provider-aware vertical slice + read-only route preview) depends on exactly this bridge.

**Disposition**: design item for the Magpie stage-2 slice. It will likely need a new versioned
executable model or a v1 extension plus migration — that design must reconcile with the ENG-02
ruling (who owns provider semantics). **Guard**: do not resurrect the rejected inventory document as
the bridge; do not bolt observations into ConfigDocument v1 ad hoc.

**Acceptance** (for the eventual slice, recorded here as target, not for this batch): a configured
provider-native account yields read-only route-preview candidates with reasons; no dispatch.

---

## ENG-05 · P2 · Thirteen non-test Go files exceed the 500-line rule (fix incrementally)

**Evidence** (measured 2026-10-08, non-test files >500 lines):

| File | Lines |
|---|---|
| `internal/application/config_service.go` | 911 |
| `internal/quota/service.go` | 818 |
| `internal/hooks/hooks.go` | 814 |
| `internal/quota/aliyun.go` | 712 |
| `internal/config/validate_graph.go` | 690 |
| `internal/store/sqlite/statements.go` | 668 |
| `internal/config/document.go` | 598 |
| `cmd/ant-server/main.go` | 590 |
| `internal/application/operation_executor.go` | 575 |
| `internal/quota/volcengine.go` | 543 |
| `internal/application/candidate_planner.go` | 541 |
| `internal/ledger/commands/prepare.go` | 527 |
| `internal/application/pool_queries.go` | 513 |

**Rule**: AGENTS.md principle 2 — single file ≤500 lines; refactor module boundaries when
approaching the limit (not mechanical splitting).

**Constraint**: several of these belong to accepted, hash-signed write sets (T1 manifest via
`make verify-manifest`; G1-DEV leaves). Refactoring them invalidates recorded hashes and re-opens
manifest drift that must be reviewed and re-signed (R14/B6). **Do not churn accepted sources purely
for line count.**

**Disposition**: split the new-code offenders first — `internal/quota/service.go`,
`internal/quota/aliyun.go`, `internal/quota/volcengine.go` (no acceptance history attached; natural
seams: per-mode readers, sign-in discovery, HTTP client helpers). Defer the accepted files
(`config_service.go`, `hooks.go`, `prepare.go`, …) until they are touched for functional reasons, and
split as part of that change with the manifest review it already triggers.

**Acceptance**: no non-test file in `internal/quota` exceeds 500 lines; `make check` + focused
package tests green; other entries unchanged.

---

## ENG-06 · P2 · Security audit of quota credential persistence (audit item — mitigations exist, leaks unproven either way)

**Known-good (verified by this review)**: sign-in runs in a dedicated Chrome profile
(`~/.config/ant/browser`); typed input is never seen by Ant; cookies remain encrypted as Chrome
encrypts them, key in the macOS Keychain (`internal/quota/signin.go:15-22`); directories 0700, files
0600 (`signin.go:199,287-288`, `store.go:54`); the dashboard API is wrapped `quota.LocalOnly`
(`cmd/ant-server/main.go:400`).

**Open questions to audit and answer in writing**:

1. What exactly is persisted in `~/.config/ant/accounts.json` for `Login`-type connections (e.g.,
   Alibaba Cloud console tokens) — and are connection values stored in plaintext?
2. Do `/api/quota*` / `/api/accounts` responses ever carry cookie or token material, or only masked
   state?
3. Can logs or error strings carry cookie/token values?
4. Do the per-provider HTTP readers confine credentials to the provider's own origin? There is no
   transport-level allowlist equivalent on this path (contrast ENG-01 Option A).

**Acceptance**: an audit note in `docs/evidence/WORK/ENG-06/` answering all four; fixes (masking,
perms, scoping, or a documented "stored plaintext like the apps Ant reads" ruling with user
confirmation) for anything found.

---

## ENG-07 · P2 · STATUS.md is no longer the sole progress source (process violation, fix immediately)

**Evidence**: STATUS.md's newest entry (2026-10-07) states "research/discussion now current … No
application/schema/runtime change … occurred in this research." Meanwhile `internal/quota` (32
non-test files) and `internal/webui` (3 files) were created/modified 2026-10-07 → 10-08;
`bin/ant-server` and `data/demo.db` were rebuilt 2026-10-08 12:49; `.claude/launch.json` adds an
`ant-demo` run configuration. `docs/evidence/WORK/` contains **no** directory for this increment.

**Rule**: STATUS.md (+ D01–D07 + gates) is the only progress source; every batch re-reads it. An
in-flight implementation increment with no STATUS entry and no evidence breaks the project's own
traceability contract — the exact "gate claims must be honest" standard prior acceptances were held
to.

**Disposition**: whoever continues the quota/webui increment adds its STATUS entry (what exists,
what is verified, what is claimed — currently: no evidence beyond package tests) and an evidence
directory, or explicitly declares the increment exploratory and pauses it. Do this **before or with**
the first fix batch from this document.

**Acceptance**: a STATUS.md entry describing the quota/webui increment's true state exists; evidence
path recorded.

---

## ENG-08 · P3 · Hooks: documented lifecycle coverage exceeds production wiring (doc-honesty fix)

**Evidence**: 12 built-in points defined (`internal/hooks/hooks.go:48-97`); production invokes 5 —
4 Guard points and candidates ordering (`internal/application/operation_executor.go:159,226,242,273,350`);
the composition root seals an **empty** registry (`cmd/ant-server/main.go:304-306`); the post-commit
Observer dispatcher is test-only; `request.validated` and Transformer capability have no production
call site; permit consumption is a direct callback, not a hook point.

**Disposition**: annotate [design/10-HOOKS.md](design/10-HOOKS.md) (and the STATUS entry) with the
actual wiring map — points defined vs. invoked, sealed-empty default — so no reader infers end-to-end
lifecycle extension exists today. Wiring more points belongs to RUN-04's gated scope; do not attempt
it here.

**Acceptance**: the design doc states the production wiring truth; no framework code changed.

---

## ENG-09 · P3 · Repository hygiene

1. **Stray file `/Users/devin/code/Ant/--help`** (24 KB JSON, `scripts/snapshot.py` manifest output
   with empty `generatedAt` — artifact of a mis-invocation, sitting at repo root since 2026-10-02).
   Delete it.
2. **`.archive/2026-10-07-old-frontend/web/node_modules`** — the archived frontend carries its full
   dependency tree. Deleting `node_modules` from the archive keeps all sources, lockfile, and the
   recorded evidence intact; reclaim the disk.
3. Note only (no action now): Makefile has no `test-web` target since the frontend archive, while
   some evidence READMEs still say "make test-web … passed" — that is historically accurate for their
   snapshots; correct such references only when those documents are next edited.

**Acceptance**: `--help` gone; archive slimmed; nothing else touched.

---

## Standing do-not-do guards for the fixing agent

- **Do not revive the rejected wizard/inventory direction** in any form — user rulings of 2026-10-07,
  verbatim in [USER-ORIGINAL-REQUIREMENTS](USER-ORIGINAL-REQUIREMENTS.md) §二.4–5 ("what a shit! do
  you know what should you build??", "ridiculous form and stupid config progressing").
- **No billable or execution egress**: IN-01–04 user inputs are still missing (STATUS.md input
  table); no execution calls to real providers without authorization; fixture/loopback only for
  execution. *Corrected 2026-10-08 by user ruling R4:* read-only, credentialed usage/balance reads
  (the user-directed quota reader, [QUOTA](QUOTA.md)) are product behavior and outside this guard.
- **Do not rewrite or re-sign historical acceptance records or manifests** as part of these fixes;
  any drift goes through `make verify-manifest` review per R14/B6.
- **No speculative abstraction**: fix what the findings require; new abstractions wait for a second
  real use case (AGENTS.md principle 1).

## End-state gate for the fix batch

On the final tree: `make check`, `make test-contract`, `make test-integration`, `make test-fault` all
exit 0; every ENG-xx item is either fixed with evidence under `docs/evidence/WORK/ENG-xx/` or
explicitly ruled with a STATUS.md entry; STATUS.md carries one consolidated entry describing what
changed, what was decided, and what remains open.
