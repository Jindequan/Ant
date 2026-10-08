# Resource Pools, Manual Priority, and the Configuration Closed Loop

Version: V0.7. User requirement: various providers/resources can be configured in the UI, resources enter pools, in-pool priority can be adjusted manually, and failover dispatch lives in the server. This design is a required capability for the first version.

## 1. Objects and the two orderings

| Object | Identity / concrete fields | Purpose |
|---|---|---|
| ProviderDescriptor | typeId, adapterRevision, supportedDomains, configuration/resource/capability schema, restricted extension schema, verified entitlement bucket list (EntitlementFacts), evidence requirements | Provider capability catalogue registered in server code; the UI generates fields from the catalogue and does not support new protocols itself; providerExtensions is a closed allow-list object and secrets are structurally inexpressible |
| AccountingAccount / ExecutionBinding | Existing accountId/bindingId, credential version, endpoint, capabilities and shared resource references | Clarifies the quota owner and the actual call path |
| Resource | resourceId, ownerRef, metric/unit/quantum, counting/concurrency/entitlement role, window/expiry/cap evidence | Physically meterable capacity; one resource identity keeps exactly one ledger |
| ResourceOffer | offerId, bindingId, domain, capabilityProfile, enabled, resourceRefs | One executable resource combination; references every constraint and entitlement path that a call must check |
| ResourcePool | poolId, namespace, name, enabled, version | User's logical pool; organizes offers of different types and selects a compatible path per request |
| PoolMember | poolId, offerId, enabled, priority, version | User's dispatch order; smaller priority comes first, ties are allowed, offerId breaks ties stably |
| ProviderEntitlementGroup | account/binding scope, metric, resource references, providerOrder/selectorEvidence | The provider's actual deduction rules; not rewritten by drag-and-drop ordering |

One AI offer may simultaneously reference token quota, monetary entitlement, request rate, and concurrency slots; omitting one required constraint is not a legal optimization. At offer compile time it must match the complete manifest verified with the Adapter; users are not allowed to delete constraints to bypass. Pool members reference offers rather than copying resource balances; the same offer can enter multiple authorized pools, and all pools share the same underlying capacity.

**Commercial fact identity (Q1 v3 frozen, resolution-based)**: physical commercial facts are server-verified instances. The server holds VerifiedScope (account scope or shared scope with alias) and its VerifiedResourceFact (physicalKey derived by the server and globally unique, dimensions, verified billing nature, semantic version, canonical window, capacityBound/initialRemaining). In configuration **every resource must resolve to exactly one instance**: with factId it resolves by name; without factId it resolves along the (metric, unit, execution-equivalent window) dimensions — 0 matches = draft issue (factUnresolved), multiple matches = must be declared explicitly (ambiguousFact); after resolution the resource dimensions must match the instance (mismatch is not executable). Duplicate physicalKey = clone. Window identity must use the kind-specific canonical rules; Q1 v3's "compare the WindowAt key at two reference instants" decision has been revoked by the D1 counterexample and cannot serve as the basis for the current implementation. Window identity is determined per the kind/canonical phase/conservative IANA identity in [Resource algorithms §1.1](04-RESOURCE-ALGORITHMS.md#11-window-rule-identity-d1-2026-10-03); rolling durations do not use fixed boundaries, and the allowed UTC aliases are limited to that section's list. Groups are unique per (scopeKind, scopeId, metric, unit); providerOrder only orders resources proven to be independent. Evidence is a typed declaration: metricId/bound are required and must match the verified instance, configuredCapacity ≤ capacityBound, and explicit dimensions (precision/currency/window/revision), when present, must match. The offer must cover the required dimensions registered in the profile. Cross-pool/offer references to the same physical capacity with the same physicalKey keep one ledger only.

**The user dispatch order** decides which offer executes first; **the provider entitlement order** decides which quota is deducted first within the selected account. These two fields, interfaces, and UI controls are separate. Users may adjust the former; the latter is mutable only with reliable provider selection parameters and evidence.

### 1.1 Trusted kinds and complete bundles (D2, 2026-10-03)

Kind constraints come from the server-registered `RegisteredSemantics`, with the new `ResourceKind` (JSON `resourceKind`) and `CommercialRole` (JSON `commercialRole`); they are not configuration-editable values, nor are they inferred backward from profile or user kind/role. Follow the verified fact's semanticsRef plus the accepted revision to find the registered semantics, and verify that the configuration's declarations match it exactly. Every resource is validated, including resources not referenced by any offer/pool; a kind conflict is located at `/resources/i/kind`, and a commercial role conflict at `/resources/i/commercialRole`; Compiled cannot be produced. Missing or illegal registered kind constraints are likewise not executable; falling back to trusting the user is forbidden.

| semanticsRef | ResourceKind | CommercialRole |
|---|---|---|
| sem.fixed-window-counter | counter | limit |
| sem.entitlement-pool | counter | entitlement |
| sem.occupancy-slot | occupancy | limit |

The above is the exact mapping of the three currently registered semantics, with no fuzzy matching on ID names. CompiledResource's kind/commercialRole is output from the registered constraints (the user's values have already been checked first); existing checks that the semantics revision matches the facts are retained.

The profile RequiredDimension.Role is a requirement view: `occupancy` only checks `resource.Kind == occupancy`; `limit`/`entitlement` check that `resource.CommercialRole` equals the corresponding value; all three also require metricId to match. An unknown role is an illegal registered profile and cannot be treated as metric-only matching. Complete bundle coverage targets the resources an offer actually references that pass the global verification above; an occupancy/limit resource may cover both an occupancy requirement and a limit requirement of the same metric, but cannot cover entitlement. Commercial role and storage metering type are two dimensions; entitlement must not be treated as a third Kind.

Given the three legal resource kinds and no offer, When compiling, Then each produced type matches the registered mapping; When only changing occupancy to counter or entitlement to limit, Then the resource gets a field issue and Compiled=nil. Given a profile requiring different roles on the same metric, When an offer references resources, Then coverage follows those role semantics, and none may be missing. The independent complete-entry counterexample is verified by CFG-04/CFG-07; this design's small examples do not replace real compile tests.

## 2. Heterogeneous resources pooled together

One logical pool may simultaneously contain AI, email, storage, and other capability groups, with each group keeping its own units and accounting. An ai/text request only filters AI offers that are implemented, verified, and semantically compatible; it never treats email message counts as tokens. Different billing shapes (token, monetary, request) can become alternative candidates as long as they supply the same verified capability; each candidate reserves according to its own per-dimension upper bounds.

The pool page displays quotas, status, and current order grouped by domain/profile/metric/unit; it does not display a cross-unit "total remaining". Same unit but different window/shared source must not be simply summed either; summaries with shared overlap are deduplicated with the constraint noted. Estimated business-executable counts may only carry mapping basis and a timestamp, and are not admission capacity.

Do not stitch half-outputs of different providers into one request. One attempt selects one offer and its complete bundle; a new attempt across offers only starts when the previous known result and domain rules prove it safe. When a domain protocol is not implemented, the user may still save its descriptor draft, the UI clearly marks "not yet executable", and activating that execution capability is forbidden.

## 3. Ordering and dispatch algorithm

```text
operation = claim(namespace, operationId, fingerprint(poolId + semanticRequest))
pool = read authorized current pool; reject disabled/unauthorized
members = filter enabled, domain/profile semantics, required affinity
for member in stableSort(priority ASC, offerId ASC):
    compile exact request with member binding + all required resources
    check current evidence, policies, guards, capacity
    if ineligible: record reason; continue
    quote is diagnostic, not reservation
    prepare atomically validates pool.version/member/binding AND all resources
    if race/version changed: rollback, bounded replan on latest pool
    if prepared: authorize -> consume permit -> one send -> settle
    only if known safe to continue: select next eligible offer
all blocked -> persist stable AdmissionBlocked; no hidden waiting
```

priority is the primary ordering; cost never quietly promotes a low priority to the front. Required affinity/request capabilities/allowMetered/evidence/hold are hard gates and cannot be relaxed by priority. Preferred affinity in the first version only records a preference diagnostic and does not reorder the chosen pool; use required to force a path. Cost display is advisory; explicit pay-disable remains an admission condition; no ordering across currencies.

After failure or contention, re-read the current order and exclude paths known to be unavailable in this run; planningMaxRounds bounds replanning without permits, and maxAttempts only counts send permits. A new attempt saves a new pool version and selection snapshot; the original attempt settles its consumption first, without refunding the old permit budget. unknown, already-visible output, cancellation, deadline, and budget prohibition conditions take precedence over "next resource".

## 4. Priority modification and effect boundary

The UI provides move up/move down/drag and numeric editing; the server receives the whole group order in one request. A submission includes the If-Match pool version, expectedConfigRevision, commandId, and the complete member list: each existing member's offerId together with its **user-explicit numeric priority** appears exactly once; the server validates exactly-once, permissions, and integer range, and in one transaction saves all priorities verbatim, updates the pool version, the configuration snapshot/activation pointer, controlVersion, and the audit — no 10/20/30 normalization (K4 frozen: dragging is only a client-side convention for generating explicit numbers; equal values sort stably by offerId). Numeric editing also goes through the full-order validation; it is not N isolated PATCHes.

409 returns the current version/order and the conflict reason; the UI keeps the local draft, prompts re-comparison, and does not auto-overwrite. The same commandId with the same body returns the original result; a different body is rejected. It must be committed before the success reply; the frontend then re-fetches the real projection, and optimistic animation must not masquerade as saved.

Attempts already consumed during execution are not migrated and are not interrupted to switch to higher priority. New permits that are prepared/not yet sent must recheck poolVersion: on mismatch no outbound call may be made; once reliable not-yet-sent facts are obtained, revoke the old reservations and replan within bounds; permits already authorized with no proof of not-sent converge conservatively, and reordering must not cause an automatic resend. External residual races between submission and the checkpoint are still handled by the DESIGN guardrails.

## 5. Configuration writes and the reverse path

An empty database with no active_config on first use: expectedConfigRevision uses the empty string, and activation only happens when nothing is currently configured; an existing activated version requires its exact revision, and the empty value must not act as a wildcard override. Without capacity evidence, a draft can initially be configured, but no outbound call may happen.

The single main path: provider/credential → account/binding → resources and offers → pool members/priority → validate → activate → minimal execution → operation result. The UI selects registered types, the server provides field schemas, units, and required evidence; the user may configure multiple counters, windows, expiry, concurrency, entitlements, and shared references, but "capacity values" need provenance — editing the current balance to erase the books is not allowed.

ConfigService.SaveDraft → ValidateDraft (references, capabilities, units, shared identity, windows, evidence, billing, complete resource mapping) → ActivateDraft(expectedRevision). Network verification happens outside the transaction, carrying version/expiry evidence; the activation transaction re-verifies evidence and the current revision, switching over only when everything succeeds. An invalid new configuration keeps the old activated version; an erroneous draft can be saved in order to fix it, but never becomes executable configuration.

Enable/disable/removing members affects new admissions and does not delete existing references, events, or unknown reservations; disabling a resource must not make other offers forget shared constraints. Removing a resource is only allowed in a new draft with no historical references; used resources are archived. Rolling back configuration activates as a new version through current validation; balances/audit do not roll back; executed side effects are not undone.

Provider endpoints are allow-listed addresses restricted to supported protocols, configured by admins only; the server blocks intranet/metadata/redirect SSRF, and re-verifies DNS resolution and connection targets; the local fault endpoint is only enabled under an explicitly enabled test combination root. Secret write endpoints return only ref/revision/mask, never echo back, and drafts and logs contain no secret.

## 6. First batch of explicit use cases

| Given | When | Then |
|---|---|---|
| token and email offers in the same pool | submit an AI request | only compatible AI is selected; the page displays units separately, with no mixed total balance |
| A priority 10, B 20, both available | manually change to B 10/A 20 and submit | new requests select B; already-executed A does not switch; history still records the old order |
| Two tabs with the same version | simultaneously save different orders | one submission succeeds, the other gets 409 with its draft kept, no partial order |
| A shared slot1 across P1/P2 | A in P1 is unknown | P2 cannot start A again by borrowing the other pool |
| High-priority paid A, free B | allowMetered=false | skip A and select B; the explanation is that spend is a hard gate, not a quiet order change |
| One account's paid entitlement ranks before free quota and the provider pool is not selectable | user adjusts offer order | does not change the account's real deduction; the strict-free policy rejects that path |
| pool v1 prepared, not yet consumed | save pool v2 | v1's final permit check rejects; only reliable not-yet-sent facts release and replan |
| New resource configured but missing billing upper bound | save and activate | the draft can be saved, activation fails naming the field; the original configuration remains in effect |

## 7. This round's evidence compile boundary (D3)

This round's pure-compile input is one fixed server-verified snapshot; it does not handle choosing among or merging multiple historical observations. For bound/hard_stop, bound, and for initial_state, remaining are both required canonical non-negative quantity strings: the former must equal the resolved instance's capacityBound, the latter must equal that snapshot's initialRemaining. Missing, malformed, negative, or inconsistent values produce a field issue and cannot produce a valid Compiled. Zero values are accepted when the verified value is truly zero; configuredCapacity may be below the verified upper bound, and the evidence declaration may not increase the amount. When historical observations are needed later, a separate version/instant selection rule will be defined; silently ignoring conflicts in this round on the grounds of possible future support is not allowed.
