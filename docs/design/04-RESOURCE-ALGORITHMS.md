# Concrete Algorithms for Resources and Metering

Version: V0.7. For user pool dispatch ordering see [Resource Pool Design](07-POOLS-AND-CONFIG.md); provider entitlement ordering is independent of it.

All functions live in `server/internal/domain/resources/`; the inputs are injected time, verified rules, a transaction snapshot, demand, and reports; the output is a ResourceChangePlan. This document defines how first-version rules are computed; which rules the selected provider requires is determined by T0.

## 1. Units, Windows, and Upper Bounds

`toUnits(value, unit, quantum)` performs only explicit exact conversion, using the charging rounding defined by the preset; token/request/slot are integers, and monetary amounts are expressed in the currency's smallest quantum. Different unit/currency values are never added together; capacity, demand, and actual are compared only after being in the same unit.

Fixed windows are identified by `[startMs,endMs)`, with `windowKey = ruleRevision + ':' + startMs + ':' + endMs`. minute/hour/day/week/month specify their timezone/anchor at configuration time; the boundary function `windowAt(now,rule)` returns one deterministic window; months must not be computed as 30 days; DST follows that timezone's calendar boundaries; resource rule revision changes require explicit migration, and balance is not obtained by generating a new resourceId.

New-window capacity can only be created by compiled, verified reset/refill rules; ResourceChangePlan may not insert quota arbitrarily. Unknown cross-window attribution produces resource-level isolation, and reset does not delete it; after `windowAt` computes a new window, isolation is still checked first.

DomainPolicy computes `upperBound = enforceableBound(request,preset)` for each required metric and references valid BoundEvidence. For AI it must cover input, maximum output, reasoning/tool/cache/error usage, and charging; unverified dimensions are rejected. For email, request/recipient/amount are metered by actual submission and projected by failure rules. The first version does not use empirical averages or implicit proportional scaling.

### 1.1 Window Rule Identity (D1, 2026-10-03)

Identity is used for configuration instance resolution/verification, serving configurators by preventing wrongly reset rules from entering executable resources. It does not generate balance, does not modify WindowAt, and contains no resourceId, rule revision, or current time; revision is checked separately by the caller. It is forbidden to prove rule equivalence by WindowAt results being equal at some arbitrary finite date.

`NormalizeWindowRuleIdentity(rule) -> comparable identity | error` produces the canonical tuple per the table below; it first validates the valid fields by kind, rejects illegal input, and two errors do not count as equal. Fields not belonging to that kind continue the execution layer's ignore semantics; no new mutual-exclusion field requirements are added.

| kind / period | Canonical identity | Valid rule |
|---|---|---|
| lifetime | `(lifetime)` | No period/timezone/anchor dimensions |
| rolling | `(rolling, durationMillis)` | durationMillis is a positive int64; calling WindowAt on fixed boundaries for equality is not allowed |
| fixed / minute | `(fixed, minute, zone, ss*1000+ms)` | Anchor date, hour, minute do not participate |
| fixed / hour | `(fixed, hour, zone, (mm*60+ss)*1000+ms)` | Anchor date, hour do not participate |
| fixed / day | `(fixed, day, zone, clockMillis)` | Anchor date does not participate |
| fixed / week | `(fixed, week, zone, weekday, clockMillis)` | weekday uses Go Sunday=0 through Saturday=6; other date fields do not participate |
| fixed / month | `(fixed, month, zone, dayOfMonth, clockMillis)` | Original dayOfMonth preserved, clamped to each month's end at execution time; 30 and 31 must not be merged |

For fixed, `period` can only be one of the five values listed above; for `zone`: empty string, `UTC`, `Etc/UTC`, `Etc/GMT`, `GMT` are explicitly allowed UTC aliases, unified to `UTC`. All other IANA names must be loadable by time.LoadLocation and keep their exact name (including case); `Local` is forbidden (host dependence). Other names are not merged based on current offset, tzdata Link, historical sampling, or future prediction; for example, Africa/Casablanca and Africa/Lagos are always distinct identities, even if some date boundaries happen to coincide. For two aliases not on the allow-list, prefer not to merge; creating a second physical capacity through this conservative policy is not allowed, and physicalKey deduplication still takes effect independently.

fixed anchors reuse the execution layer's `parseAnchor`: non-empty values are parsed into an instant by Go time.Parse(time.RFC3339), the nanosecond value must be evenly divisible by 1,000,000, then converted to the rule's timezone, and then the phase from the table is extracted. Trailing zeros in a fraction are acceptable as long as they represent milliseconds exactly; rejection is not based on the string's decimal digit count. An empty anchor means zero clock, Monday, dayOfMonth=1; an explicit anchor equals the default only when the relevant phases agree. An explicit Thursday midnight for a week does not equal the default week, and an explicit 15th midnight for a month does not equal the default month.

The same fixed tuple means the same local boundary definition, using the existing execution policies: minute/hour treat every phase occurrence as a boundary; day/week/month fold takes earliest and gap takes transition; month clamp. Identity is judged here through structure and fixed policies, without re-sampling or rewriting DST boundaries. Different IANA names are conservatively kept apart, so this judgment only promises equivalence within the allowed normalization scope, not discovery of all mathematically equivalent rules.

See [CFG-01](../evidence/WORK/CFG-01/README.md) for a runnable small example and its raw output. Given rolling/60000 with itself, natural midnight and explicit midnight, allowed UTC aliases, and the same hour phase on different dates, When normalized, Then the identity is the same; Given Casablanca/Lagos, different millisecond phases/weekdays/days of month, Then the identity is different; Given unknown kind/period/zone, Local, malformed anchors, sub-ms, and non-positive rolling duration, Then an error is returned and no executable instance can be parsed.

## 2. Counting Limits and Observation

For a fixed-window counter:

```text
netCommitted = committed + adjustment
localAvailable = capacity - netCommitted - reserved
if valid observation for same resource/window/adjustmentVersion:
  providerAvailable = observedRemaining - (committedNow - committedAtStart) - reserved
  available = max(0, min(localAvailable, providerAvailable))
else:
  available = max(0, localAvailable)  # only when the local baseline is trusted and policy does not strongly depend on observation
admit(q) = q >= 0 AND q <= available AND no scoped hold/encumbrance
```

committed is appended monotonically; justified accounting corrections enter adjustment (positive values add consumption, negative values reduce the original consumption), update adjustmentVersion, and invalidate old observations. A negative adjustment must not exceed the provably correctable consumption and must not be treated as newly granted quota.

Prepare: `reserved += upperBound`; known settlement: `reserved -= upperBound; committed += actual`; actual unknown but window attribution trusted: append upperBound; real overrun: append the full actual, mark overrun, and hold. Whether a failure counts request, token, or amount is projected separately, not uniformly zeroed.

Observation collection first reads window, committed, and adjustmentVersion; the network runs outside the transaction; on write, the same window/version and the source's point-in-time/freshness are checked. Concurrently added consumption deltas are conservatively deducted; collection/accounting lag must not infer consumption that never happened. When the watermark cannot be reliably matched, it is diagnostic only. Strict candidates without a reliable initial state are rejected; reopening by "ignoring observations" is not allowed.

## 3. AND Constraints and the Entitlement Waterfall

A single request may simultaneously be constrained by account total tokens, key request rate, concurrent slots, and free entitlements. Limit kinds are AND: each is checked against the same business usage, but they are control views; the counts of all constraints are not summed as multiple charges.

The entitlement group `pool_waterfall` only adopts the deduction order already verified for the provider. Concrete allocation:

```text
need = upperBound
for pool in providerVerifiedOrder:
  skip expired pool
  if pool.available > 0 AND pool.metered AND NOT operation.allowMetered: reject
  q = min(need, pool.available)
  reserve(pool, q); need -= q
  if need == 0: break
if need > 0: reject whole prepare, rollback all other constraints
```

The order here is providerOrder / provider expiry rules, not user pool member priority; it must match provider facts, and a commercial deduction order must not be invented for "cheapness". When a metered pool has available balance in the provider-mandated order, skipping it to pick a later free pool is not allowed; only when the provider supports an enforceable pool-selection parameter and evidence already exists may the compiler generate another real consumption path. When actual is less than the reservation, settlement follows the same real order and releases the unused difference; when actual exceeds the reservation, the fact/overrun is likewise recorded. When the provider may have chosen a different pool or deducted across periods and attribution cannot be proven, pool attribution is not guessed; verification and corresponding isolation are formed instead.

Spending policy: cost ordering uses only known incremental costs in comparable currencies; allowMetered=false does not actively admit known chargeable allocations. Whether a truly zero bill is achieved still depends on D03 evidence such as upper bounds, initial state, complete consumption entry points, and provider hard stops.

## 4. Concurrent Reservations

```text
availableSlots = capacity - reserved - active - uncertain
prepare(q):          reserved += q
authorize(q):        reserved -= q; active += q
endProved(q):        active -= q; reservation -> released
unknown(q):          active -= q; uncertain += q; create open encumbrance
laterEndProved(q):   uncertain -= q; encumbrance -> released (once)
```

held/reserved, active, and uncertain are mutually exclusive by reservation/encumbrance sets, and summary is a derived aggregation of these sets. The Ledger verifies that deltas are consistent with item state transitions; it must not merely subtract from summary.

Cancel/abort/local timeout/lease expiry do not call endProved. End proof can only come from explicit provider termination, a trusted enforcement ceiling, or verified termination facts, and must cover the corresponding attempt/resource. An ordinary clearHold does not delete uncertainty; a concurrency-1 resource lacking evidence may remain permanently unavailable, and queries must show the evidence-gathering path.

## 5. Rolling Windows Where Necessary

Implement `rolling_counter.go` only if the selected resource truly needs it. Valid consumption is summed from indexed events: `chargedAt in (now-duration,now]`, plus all unfinished related reservations; reservations are not released just because their age exceeds the window. rolling's windowKey is fixed as that rule revision + rolling; a new balance row must not be generated for every now; ResourceContext.rollingUsed injects the net consumption of the valid window, while snapshot.committed remains the monotonically accumulating watermark. The actual point in time is decided by D03 evidence; booking at the moment the response is received must not be the default.

Unknown chargedAt must not be forgotten as an out-of-window event; use a window_attribution encumbrance to conservatively isolate the affected resources until attribution is confirmed. Negative adjustments are associated with the original consumption and enter the valid window at the original reliable chargedAt; summary caches may only be updated by events and must not grow capacity independently. Indexing and necessary bucket caches are decided by measurement; substituting a non-equivalent fixed window for a rolling rule up front is not allowed.

## 6. Example: Shared Quota and Unknown Concurrency

Account A has two bindings k1/k2 sharing daily-token=1000 and concurrency=1, each with its own key-request-limit. Operation X reserves token=300, slot=1 on k1; after authorization slot active=1. The response is lost: daily-token committed += 300, reserved returns to zero; slot active returns to zero, uncertain=1. Operation Y switches to k2 but must still check the account-shared resource at the same time; slotAvailable=0, rejected; multiple keys do not add capacity.

Later it is verified that X actually used token=180 and has ended: append Adjustment=-120 (the committed watermark remains 300), release slot uncertainty=1, and leave the original unknown operation unchanged; new logical actions can be admitted based on the current state. Without end evidence, fixing only the token accounting does not release the slot; even the next day's reset must not delete the slot or the unknown cross-period attribution.

## 7. Change Plan Validation

`validate_change.go` recomputes every delta from the current snapshot: resources come from the original plan, unit/window/version are consistent, reservations do not exceed available, set states are legal, each summary is conserved with item transitions, every event ID is unused, there is no new capacity command, and consumer/resolver/permit conform to their responsibilities. Bypassing capacity with "negative reservations" is not accepted.

Extensions may only return the defined reserve/activate/consume/release/encumber/adjust/reconcile/window_move changes; these changes are not a general SQL patch. Accounting overrun is a legitimate fact path and settle must not be rejected because available is insufficient; the subsequent hold is another explicit change.
