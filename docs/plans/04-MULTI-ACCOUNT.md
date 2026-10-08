# T4 · Two Accounts and Safe Dispatch

Goal: prove that two providers / two real accounts in the same domain are selected by rule, and that reassignment happens when safety is provable. Prerequisite: T2/T3 exit. Primary owner: dispatch / adapter; integration keeps the Router core unchanged. Related: DESIGN §3/§6/§8/§11, A09/A19.

## T4.1 Second Binding and Real Differences

1. Independently review the D03 evidence for the second provider already selected by T0; wire in the second preset, Adapter, and binding / resource references, without copying the Ledger or the sender.
2. Use a unified port to express metering, reporting, error scope, and end-of-call evidence; output in the same domain keeps the contract, with no out-of-scope protocol translation.
3. Verify account independence and sharing relationships; dual-key verification on a controlled shared account must not add quota, and real credential errors must not damage unrelated bindings.
4. Run the common adapter / resource contracts and first-binding regressions, explicitly rejecting missing upper bounds or guard capabilities.

Deliverables: the second adapter / preset, real-difference notes, and evidence. Acceptance: the newly added capability implementation does not modify the dedup, single-send, or accounting core; independent account balances and shared resource references are correct.

## T4.2 Manual Priority, Resource Pools, and Historical Explanation

1. Implement filtering by capability, enabled, affinity, the current safety gate, resource quote, and cost constraints; required never bypasses.
2. The Web provides move-up / move-down / numeric editing and saves pool order as a whole group (reusing the whole-group priority API delivered in T2.1 and the T1 pure ordering rules; no second write entry point is created); the server selects stably by priority ASC / offerId ASC. Version conflict returns 409 and the draft is preserved; required / capability / spend are hard gates, and the manual order is never silently overridden by preferred / cost. This item delivers the second provider's real differences and the complete ordering UI, not the first implementation of the selection mechanism (K5 ownership ruling).
3. Verify capability grouping / independent units for different offer types in the same pool, and that multiple pools on the same resource do not add capacity; use two competing requests to verify that quote does not reserve quota, prepare re-checks, failure moves on to the next legal candidate, and planning / deadline caps; when all are temporarily blocked, fail fast and stably.
4. DecisionRecord stores the candidates at the time, the ordering keys, the configuration / plan / resource versions, and the competition outcome; historical queries after configuration or balance changes do not re-derive.

Deliverables: internal rankCandidates, selection / preparation coordination, and historical explanation. Acceptance: A09/A19/A27/A28, with bounded traces marked as truncated; the chosen permits and key decisions are durably consistent. Do not extract a public policy interface when there are no real ordering variants.

## T4.3 Safe Reassignment and the Real Closed Loop

1. Per the DomainPolicy whitelist, distinguish known-incomplete-and-safely-retryable, confirmed success, unknown, and cancel / budget / deadline / visible output. Prohibition conditions override the adapter's allow suggestion.
2. Settle the resources of a failed attempt faithfully first; while the operation can still continue, keep one active attempt, and the next outbound call must reserve / authorize again; budget is never returned without justification.
3. Use the controlled network to verify auth failure, rate limiting, partial consumption, unknown, and first-chunk streaming; allowed scenarios may switch to the second binding, prohibited scenarios keep the original terminal state and reservation.
4. Execute controllable selection scenarios with two real accounts, checking the chosen binding, the provider requests, and each account's accounting; real production side effects are never used as a fault-injection means.

Deliverables: the safe-reassignment implementation, two-account real closed-loop evidence, and original-order binding regressions. Acceptance: A09/A19/A27/A28 and the applicable conditions of R04/R08 pass; there is no automatic resend of unknown, no streaming concatenation, no double deduction, and no multi-key capacity growth.

## Phase Exit

T4.1–T4.3 complete; both providers share the core path in the same domain, and selection / reassignment is explainable with real call evidence. Next: [T5 Onboarding and Operations](05-OPERATIONS-AND-ONBOARDING.md). Do not implement adaptive ordering or unverified new-domain real outbound calls; heterogeneous configuration and logical pools were already delivered in this round.
