# Ant core extension increment

Date: 2026-10-07 (Asia/Singapore). R02/R05/R07/R18, I01–I10. Backend preparation, not real-provider or release acceptance.

## Business definition

Users: application developers adding integrations and operators managing shared quota on one host. Value: supply a provider's protocol, resource vocabulary, request policy and metering without rewriting reservation, dispatch, settlement or recovery.

Objects: provider catalog, adapter, provider-specific policy, resource dimension, immutable metering program, usage sample, report and verified bound. Existing configuration, operation, resource and report wire/storage schemas stay compatible.

Closed loop: provider/resource configuration (implemented) → compile/validate (implemented; registration consistency strengthened here) → plan (implemented; resource coverage strengthened here) → reserve/send (implemented) → meter/settle (implemented; shared calculator added here) → query/cancel/reconcile (implemented). Real account admission and usage reconciliation retain existing evidence gates.

Non-goals: frontend changes, dynamic plugins, invented free quota, live price updates, new services, multiple database hosts, second business domains, or bypassing admission. Infrastructure remains Go + local SQLite, with no required paid service. Provider usage charges depend on verified account facts.

Quota authority stays with the ledger. Extensions propose plans/reports; every configured resource must be covered. Estimated charges retain their conservative basis. Unrepresentable calculated usage is preserved as an exact decimal for existing ledger isolation, never wrapped or clamped. Cancellation cannot reverse external charges or rewrite history.

## Design and write set

1. `application.ProviderRegistry` assembles explicit startup registrations; checks catalog/adapter revision and profile/policy identity; rejects duplicate/incomplete executable registrations; supplies one catalog, policy list and adapter lookup. Catalog-only registrations stay draft-capable. No mutable registration API, SQL, secrets or network capability. Catalog descriptors freeze at composition; verified scope facts retain their existing read boundary.
   Policies implement the neutral `planning.ProviderPolicy` port, with existing application aliases retained for compatibility. Integrations can depend on this small contract instead of importing the application runtime.
2. `domain/metering` compiles bounded immutable formulas: output = fixed minimal units + sum(input dimension × rational rate). Units are explicit; arithmetic uses exact standard-library integers/rationals; rounding occurs once per output with explicit exact/ceiling mode. Missing, duplicate, negative or incompatible samples fail; zero-rate terms still require their input. Projection checks mapping revision, preserves conservative provenance and out-of-int64 amounts. DeepSeek reuses the engine with existing frozen rates/mapping; tariff facts are not claimed as freshly verified.
3. Planner checks unique bound identities, supported modes, dimension/role consistency and exactly-once coverage of configured resources. A policy cannot omit token, money or concurrency constraints. Multiple constraints on the same metric (e.g. minute/day windows) may share a single reported counter; the Chat Completions codec validates the unique report dimensions rather than equating report count with resource requirement count. Ledger/adapter safeguards remain authoritative.

Write set: registry/metering and tests; catalog and tests; planner and tests; DeepSeek pricing and tests; server composition and tests; dependency gate; this document, DESIGN §11, STATUS and `docs/evidence/WORK/CORE-EXTENSIONS/`. Inspection also found preparation required reservation bounds to equal verified ceilings, although the planner and adapter permit a smaller bounded demand. Preparation now checks demand ≤ verified ceiling and the active report mapping; real SQLite tests cover both sides of this boundary. Router request validation uses the shared protocol validator, with provider-specific restrictions at planning/encoding, so a local fixture's output cap cannot restrict other providers.

No page contracts, visual tokens or navigation change; visual gates do not apply. Main path stays configuration → operation → query; existing cancel/reconcile reverse paths remain regression-tested.

## Given/When/Then acceptance

- Given providers sharing a domain/profile, when registered, then each resolves its own adapter/policy; mismatched or missing executable components fail startup.
- Given a composed catalog, when the source or a returned descriptor is mutated, then registered metadata stays unchanged.
- Given counters and rational rates, when metered, then arithmetic stays exact until specified final rounding; absent counters fail and conservative samples cannot become exact reports.
- Given an intermediate product beyond int64, when the final quotient fits, then calculation succeeds; otherwise canonical usage survives in `ActualOutOfRangeDecimal`.
- Given an offer with three resources, when a policy omits/duplicates a resource or uses an invalid mode/role, then planning fails before reservation/send.
- Given existing SQLite flows, when tested after this increment, then atomic reservations, single-send, cancellation, unknown occupancy, recovery and permissions still pass.

Commands: targeted `go test -race -count=1`, `make check`, `make test-contract`, `make test-integration`, `make test-fault`, server composition tests. Save actual logs/source digest. No live calls/deployment are needed.

Risks: registration usability (startup errors and extension recipe), extension correctness (negative contract tests), overflow (exact calculation and isolation), billing feasibility (retain evidence gates). Unsupported semantics fail closed.

## User ruling

2026-10-07: “I think the frontend is not the most important area, it can be implement by golang or other method, you must build the greatest core of Ant! free infrastructure, strong ability, extensible models. we will add more and more providers, resources, and usage calculations. So think more, do best!”

This authorizes the backend increment along existing R18 boundaries ahead of T7. Correctness invariants and real account/performance/deployment gates remain in force.
