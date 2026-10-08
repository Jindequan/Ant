# Review of Phase 0 + Phase 1 (storage half) — findings for verification and correction

Reviewed: commit `020e965` ("Retire supply inventory; add supply store, design 11 phases 0–1") on top
of baseline `890052b`, by full git diff, three parallel deep-review passes (git archaeology /
migration chain / store layer), and fresh gate runs by the reviewer on 2026-10-08.

**Verdict: accepted with corrections.** The batch's claims held under adversarial checking; one HIGH
repo-hygiene defect must be fixed immediately, five MEDIUM items ride with Phase 2, and the LOW list
is either fixed or explicitly deferred with a note. Every finding below carries the evidence needed
to re-verify it; nothing requires re-litigating the design.

## Verified before you act (do not rework these)

- **Gates (reviewer-run, final tree)**: `make check` green — `check-deps: dependency boundaries OK
  (30 packages)` (= 31 − deleted `domain/supply`; the subtraction itself is evidence of clean
  removal); `test-contract` 22 packages `-race` (= 23 − supply); `test-integration`,
  `test-fault` pass.
- Phase 0 deletion is complete: 8 files gone, route unregistered, OpenAPI −168 lines at 1.1.22 with
  fixture manifest synced, no symbol references remain in Go sources; the only "inventory" strings
  left are the migration history (required), a capability-enum comment, and tests.
- Migration chain is correct: 34+1+1−1+6 = 41 = `expectedTableCount` (`migrate.go:46`); 005 is a
  minimal one-way drop with the 004 history retained for v3 upgrades; embedded copies are
  byte-identical to `server/migrations/` and CI-enforced (`TestLaterMigrationCopiesMatchCanonicalFiles`);
  the v4-with-inventory-rows upgrade test really seeds rows and asserts their loss alongside
  preservation of unrelated data.
- The five claimed store guarantees have substantive tests (not smoke tests): a real −5 balance, an
  append-only trigger exercised via deliberately raw SQL, three identity-conflict cases, and
  merge-returns-existing-id.
- The schema's deviations from design §2.4 are all strengthenings (readings' `run_id` FK,
  collector-run outcome pairing CHECK, window order CHECK) — keep them.
- The stated deviation (accounts.json import → Phase 2) is justified: `secret_refs.principal_id` is
  NOT NULL (`002_secret_ownership.sql`), so no import target exists before the R8 session work.
- Rulings R1–R8 are recorded faithfully in design 11 §8 (including the reviewer's R3 position marked
  "not adopted"), and the ENG-FINDINGS guard was a scoped rewrite with a dated correction note.
- "Nothing reads from the new tables yet" — confirmed: zero production references outside tests.

## Findings

### F-01 · HIGH · An 18 MB build artifact is tracked in the baseline commit (fix before Phase 2)

**Evidence**: `git ls-files` → `server/ant-server` (18,485,890 bytes, mode 100755, a stale Oct-05
build) was committed in root commit `890052b`. `.gitignore` covers `bin/` and `server/bin/` but not
this path. Being in the root commit, later deletion still leaves it in history.

**Required**: `git rm --cached server/ant-server`; add `server/ant-server` to `.gitignore`; decide
whether to rewrite the baseline (no remote exists; rewriting is cheap today and permanently reclaims
the 18 MB — recommended while history is two commits old).

**Acceptance**: `git ls-files` no longer lists it; `git status` clean; `.gitignore` entry present;
`du -sh .git` reflects the choice made.

### F-02 · MEDIUM · The supply-resource identity guard omits `template_ref` (fix with Phase 2 reconciler work)

**Evidence**: `UpsertSupplyResource` guards identity via `ON CONFLICT(namespace, resource_id) DO
UPDATE … WHERE physical_key=? AND package_id=? AND metric_id=? AND unit=? AND quantum_exp=? AND
currency=? AND semantics_ref=? AND window_json=? AND scope_json=?` (`supply_statements.go:122-129`).
`template_ref` is in neither the WHERE nor the SET: an upsert whose only difference is the template
ref "succeeds", updates `semantics_revision`/`updated_at`, silently keeps the stored template_ref,
and returns no `ErrSupplyIdentityConflict`. The physical_key hash (which includes the template)
usually catches this, but the guard is incomplete relative to the identity dimensions and the gap has
no test.

**Required**: add `template_ref` to the WHERE guard (and confirm the design's identity dimension list
in §2.2 matches), plus a regression test "template_ref change → ErrSupplyIdentityConflict". While
there: the guard dimensions `metric_id/quantum_exp/currency/semantics_ref/package_id` are guarded but
untested — one table-driven test covering every dimension is cheaper than five bespoke ones.

### F-03 · MEDIUM · `secret_ref` foreign key is not namespace-bound (decide with Phase 2)

**Evidence**: `006_supply_store.sql:10` references `secret_refs(secret_ref)` single-column;
`secret_refs` PK is single-column (`002_secret_ownership.sql`). A supply credential in namespace A
can therefore reference a secret owned by namespace B at the schema level — a gap against D11-03's
namespace isolation, currently enforceable only by (not-yet-written) engine code.

**Required**: either (a) add `UNIQUE(namespace, secret_ref)` to `secret_refs` and bind the FK as
`(namespace, secret_ref)` — noting this touches an accepted migration table and needs the change
called out in STATUS — or (b) record a written decision that enforcement is engine-side, with a test
when the engine lands. Do not leave it implicit.

### F-04 · MEDIUM · Three live documents still describe the retired inventory in the present tense (fix as Phase 2 housekeeping)

**Evidence** (all verified):

- `docs/LOCAL-DEVELOPMENT.md:15-16` — "Their server APIs still exist" (the `/v1/inventory` API no
  longer exists); `:60` — "SQLite migration v4 adds the inventory table", with no mention that v5
  drops it one-way. **This is the worst of the three: a user reading it would not expect their local
  inventory rows to vanish on next server start.**
- `docs/README.md:7` — "The supply inventory correction **is implemented** and verified locally…"
- `docs/design/11-SUPPLY-ENGINE.md:527` — §6 phase 0 still says "**needs user confirmation**" while
  §8 records R7 as ruled and adopted; the document contradicts itself.

**Required**: update all three (past tense + retirement note; §6 row marked ruled). Add a sweep for
any other present-tense inventory descriptions outside `docs/evidence/` and dated review records,
which are historical and stay as written.

### F-05 · MEDIUM · Freshness ("checked, unchanged") has no read path (deliver with the Phase 2 scheduler)

**Evidence**: design 11 §2.4 assigns freshness to `collector_runs`; Phase 1 only exposes
`StartCollectorRun`/`FinishCollectorRun` writes. `SupplyReaderTx` (`store/supply.go:116-122`) has no
collector-runs read method, and no `ListSupplyPackages` either (credentials and resources read,
packages don't — asymmetric surface).

**Required**: when the scheduler lands, add the read methods (`latest run per credential`,
`ListSupplyPackages`); until then, the evidence README should list this as deliberately deferred, not
implied as available.

### F-06 · MEDIUM · Cross-namespace behavior of the globally-unique `physical_key` is undefined at the failure boundary (confirm at Phase 2 design)

**Evidence**: `physical_key TEXT UNIQUE` is global (`006:38`), matching design §2.2 ("globally
unique"). But when two namespaces observe the same physical resource, the second gets a raw driver
UNIQUE error — not `ErrSupplyIdentityConflict` — and cannot hold its own observation row. Whether
two namespaces may each observe the same real account is a product-semantics question (today's app
is single-user; §3.5 makes supply namespace-scoped anyway).

**Required**: before the reconciler is written, decide and record: either physical resources are
single-observation (and the UNIQUE collision is a typed, documented outcome), or scope the uniqueness
to the namespace (schema change now, while v6 is the newest migration and nothing depends on it).
The error must be mapped to a typed error in either case.

### F-07 · LOW · Two CHECK branches and the `secret_refs` FK path are untested

The "found + secret_ref rejected" negative case and the "added + valid secret_ref passes" positive
case (the only exercise of the `secret_refs` FK on this table) have no tests
(`supply_statements_test.go:167-186` covers the other branches). Add both — the FK path especially,
since it crosses into the 002 schema.

### F-08 · LOW · Three error styles for the same class of failure

Typed (`ErrSupplyIdentityConflict`, `ErrSupplyReadingOutOfOrder`), raw driver errors (duplicate
`reading_id`, account_id colliding with a different identity's PK, package colliding
`UNIQUE(namespace,account_id,template_ref)`, all FK violations), and a non-sentinel `errors.New`
(`FinishCollectorRun` double-finish, `supply_statements.go:189`). Also: replaying an identical
reading_id surfaces as `ErrSupplyReadingOutOfOrder` (the `<=` check precedes the PK check) — the
error text says "older" but the condition is "not newer" (`store/supply.go:19` vs
`supply_statements.go:241`). Map the common cases to typed errors and fix the wording.

### F-09 · LOW · `migrate_test.go:105` still hardcodes the future version (`VALUES(7, 42)`)

Same smell as the codex regression test that was correctly changed to `currentSchemaVersion+1`. When
schema v7 ships, this fails loudly (not silently — acceptable but noisy). Use the same relative form.

### F-10 · LOW · `.gitignore` lacks a root `data/` entry

Root `data/` is currently ignored only via `*.db`/`*.db-*` matching its files; a future non-db file
there becomes untracked noise. Add `data/`.

### F-11 · LOW · The out-of-order policy is implemented but unspecified in the design

Append requires `observed_at` strictly greater than the latest (same-millisecond readings are
rejected; provider clock skew is the realistic trigger). Record the chosen policy and the caller's
expected handling of `ErrSupplyReadingOutOfOrder` (write to run detail/outcome?) in design 11 §3.3
so the engine is not designed against an unstated contract.

### F-12 · LOW · JSON columns have no length caps

`evidence_json`/`definition_json`/etc. are uncapped; a giant row can only be bounded by the writer's
50 ms command budget. Consistent with house schema practice, so the cap belongs in the engine/API
layer — note it as a phase-2 requirement, not a schema change.

### F-13 · LOW · The latest-reading query's tiebreaker is dead code that defeats the index

`ORDER BY observed_at DESC, reading_id DESC` (`supply_statements.go:210-211`): the append rule makes
`observed_at` unique per resource (equal-or-older is rejected), so the second key never fires, but it
forces a temp B-tree sort outside the `(namespace, resource_id, observed_at DESC)` index. Drop the
tiebreaker.

### F-14 · LOW · Test helper calls `t.Fatalf` inside the writer goroutine

`supplyWrite` (`supply_statements_test.go:24-27`) type-asserts and calls `t.Fatalf` inside the `Run`
callback that executes on the writer goroutine — `FailNow` from a non-test goroutine is undefined.
Currently unreachable, but if it ever fires it hangs `SubmitWrite` instead of failing the test.
Return the error from the callback and fail in the test goroutine.

### F-15 · LOW · ENG-03 evidence wording is inaccurate

The evidence README says `grep -rni inventory server --include='*.go'` matches "only migration
tests". It also matches `migrate.go` (migration history — required and legitimate),
`store/supply.go:108` (the collector capability enum `inventory|models|verify`, matching design
§2.4's CHECK), and `supply_statements_test.go:55`. The acceptance criterion's intent ("only
unrelated matches") holds; the literal claim does not. Correct the README to enumerate the matches
and why each is legitimate.

### F-16 · PROCESS · Squash-per-phase commits make process claims unauditable

The report states two bugs were found and fixed during development ("nonsense evidence comparison",
"inconsistent credential-identity check"). With a single squash commit, there is no diff, no reflog
trace, no way to audit the fix process — only the (verified) end state and its regression tests.
Going forward: one commit per planned step (Phase 2's five steps ⇒ at least five commits), so
mid-phase self-corrections are visible in history. This is also what adopting git was for.

## Standing guards (unchanged)

No revival of the rejected wizard/inventory direction; no billable/execution egress before
IN-01…04/G-gates (read-only observation is product behavior per R4); no amendment of frozen decisions
inside drafts; no manifest re-signing without diff review; no speculative abstraction (interfaces per
the §3.9 doctrine and REVIEW-CONSOLIDATED §4).

## Suggested sequencing for the next batch

1. **Housekeeping commit**: F-01, F-04 (docs sweep), F-09, F-10 — small, fast, no schema work.
2. **Phase 2 steps as planned** (discovery/scheduling → reconciler + quota-reader adapter → R6
   opt-in → R8 `/v1` session → accounts.json import), each step its own commit, each ending with the
   four gates green. Fold in: F-02 (identity guard), F-05 (freshness + package reads), F-07 (missing
   tests), F-08/F-11/F-13/F-14 (store refinements — cheap now, expensive later), F-06 (the design
   confirmation the reconciler depends on), F-12 (engine-side cap).
3. F-03 needs its schema-vs-engine decision made before the reconciler writes credentials — do it at
   Phase 2 design, not mid-step.
4. Every step: STATUS entry + evidence directory; gate claims match actual runs (the bar this batch
   met).
