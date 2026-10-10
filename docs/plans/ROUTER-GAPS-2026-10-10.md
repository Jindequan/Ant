# Router gaps — 2026-10-10

Verified against the current Go tree after design 13/15/16. This is a work
checklist, not a replacement architecture.

- [x] The Z.ai, DeepSeek, Kimi, and Bailian native collectors feed the supply
  store; `/api/route` reads that store. The existing pool-member priority is
  consumed by `application.CandidatePlanner`, but that is a separate config
  execution path and does not order supply offers.
- [x] Fix supply-route eligibility first: an open free grant must not bypass
  provider-declared hard account constraints; inactive packages and readings
  past their reset/expiry must not be treated as current. Missing hard meter
  readings fail closed. Keep prepaid balances as alternative payment paths.
  SQLite-backed counterexamples cover open grants, exhausted/expired windows,
  and package start/end dates.
- [x] Return the offer version and exact resource-reading IDs/times/scopes used
  for every eligible and excluded candidate; expose those same reasons in the
  page. Keep eligibility separate from ranking.
- [x] Add persisted, server-applied manual order to supply routes. It is scoped
  by canonical model, orders eligible offers only, survives restart, and uses
  optimistic revisions to reject stale writes. Re-qualification shares the
  write transaction; stale candidates conflict without advancing revision.
  Empty order restores Ant's deterministic dynamic default. This remains a
  supply-route preference separate from the older configuration-pool priority.
  The API reports committed, not-committed, and unknown outcomes; `savedOfferIds`
  is returned separately from current eligible candidates so a read-only GET
  can confirm a save after candidate changes.
- [ ] Keep cooldown, session affinity, fallback/consumption and usage
  attribution out of preview-only claims. Before enabling consumption, connect
  provider adapters to controlled Transport and the operation/ledger outcome
  guards; paid fallback remains opt-in.

Eligibility, observation-version, and manual-order slices are implemented and
verified with SQLite and fake-provider API coverage. Manual order changes the
preview only; it does not authorize a model request. The next slice is to
connect actual consumption to controlled Transport and ledger outcome guards.
