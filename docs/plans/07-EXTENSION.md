# T7 · Extension After Real Needs Arise

Status: optional; not starting it in the first version does not affect completion. Prerequisites: T6 exited and clear new requirements exist. Primary owner: project / adapters; integration and verification maintain the core boundaries. Related: PRD R18, DESIGN §2/§11, A23.

## T7.1 Requirements and Impact

Determine the real users, inputs / results, reasons for change, and acceptance. Per DESIGN §11, judge the landing point: provider → preset / Adapter; binding → identity and references; resource → ResourceSemantics; domain → DomainPolicy/Guard; deployment → Store/SecretResolver; ordering → verify the real variant first, then decide whether to extract an interface.

Update the affected requirements / designs and task cards, obtain new admission evidence, and explain compatibility and migration. If authorization, idempotency, consumption / occupancy, or provider-guarantee boundaries change, handle it explicitly — never disguise it as an ordinary configuration change.

Deliverable: sourced scope and acceptance, module impacts, evidence, and a migration plan. Acceptance: implementation can proceed along existing boundaries, or new core semantics have been explicitly designed; interfaces are never finalized for "might be needed later."

## T7.2 Implementation and Regression

Implement along the prescribed extension points, using the new requirements to verify real differences; new code only makes proposals — the core uniformly guards transactions, permits, terminal states, and permissions. Check the applicable items of I01–I10 and A23, run the existing two-account / recovery regressions, then add the real slice and performance verification for the new capability.

Deliverable: the new capability, migration / compatibility, contracts, and real-usage evidence. Acceptance: core invariants are not modified or disabled, and Router / Ledger / senders are not duplicated; the added deployment does not reduce business consistency, and the new domain does not inherit inapplicable success / safety retry semantics.
