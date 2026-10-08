# Design 11 phase 1 (store) — supply store schema and repositories (2026-10-08)

Migration `006_supply_store.sql` (schema v6, 41 tables): `supply_credentials`, `supply_accounts`,
`supply_packages`, `supply_resources`, `collector_runs`, `supply_readings` (append-only by trigger),
all namespace-keyed, per design 11 V0.2.1 §2.4. Port: `store.SupplyReaderTx`/`SupplyWriterTx`
(`internal/store/supply.go`); implementation `internal/store/sqlite/supply_statements.go`.

Guarantees, each with a test in `internal/store/sqlite/supply_statements_test.go`:
- readings are appended only when the value changes, never out of order, and never rewritten
  (`…AppendOnlyOnChangeAndInOrder`, `…CannotBeRewritten`); negative balances are stored;
  a pinned snapshot stays resolvable by reading id (§2.2.1);
- identities are immutable: same provider identity merges into one account; a resource's physical
  key, unit or window cannot change; a credential cannot change origin (`…IdentitiesAreImmutable`);
- secrets: found credentials carry no `secret_ref`; added ones need one unless `needs_key`;
  there is no plaintext column (`…CredentialSecretRules`);
- a collector run finishes once (`TestCollectorRunFinishesOnce`).

Not in this step, by design: the `accounts.json` import (§5). `secret_refs` requires an owning
principal and today's page has none; the import lands with the page's `/v1` session (ruling R8),
i.e. with phase 2's engine shell. No consumer of the port exists yet; phase 2's reconciler is the
first.

## Corrections after REVIEW-PHASE0-PHASE1 (2026-10-08)

Commit `Supply store: namespace-bound secrets…` (migration v6 amended in place; it had never been
applied — both local databases were at v4): composite secret FK `(namespace, secret_ref)`;
`UNIQUE(namespace, physical_key)`; `template_ref` in the resource identity guard; typed errors for
UNIQUE/PK/FK and closed runs; no tiebreaker in the latest-reading query (query plan has no temp
B-tree). New tests: every identity dimension (mutation-checked on `template_ref`), all secret paths
including cross-namespace and unknown secrets, per-namespace physical keys, typed errors.

**Deliberately not yet available** (phase 2, with the scheduler as first consumer): reading
collector runs (freshness) and listing packages. The port today reads credentials, resources and
readings only.
