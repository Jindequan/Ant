# T5 · Onboarding, Queries, and Operational Disposition

Goal: integrators can make minimal calls, operations staff can explain / repair failures, and paid-for usage and history are not lost through misoperation. Prerequisites: preparation possible after T2, the management mechanisms complete after T3, phase exit after T4. Primary owners: onboarding / operations; integration and verification collaborate. Related: PRD N02/N03/N05–N07, A10–A14/A21.

## T5.1 Complete Web Configuration and Onboarding

1. On top of the T2 minimal real slice, complete the [7 page-type contracts](../design/09-WEB-CONSOLE.md): schema forms for provider / resource / pool, whole-group priority, operation queries / results / SSE. The frontend only calls the Go API; the CLI is only for bootstrap and operations assistance.
2. Provide the UI's single main path: provider selection, secure credential writing, account-sharing confirmation, resource / offer, pool-joining order, verification and activation, minimal request, and result queries. Integrators within the supported scope need not design accounting rules themselves.
3. Distinguish SSE disconnect from execution end; when the browser leaves, the server still settles, and on server exit it recovers per the lease; original IDs are preserved. Four states, field errors, conflicting drafts, zh/en, tokens, keyboard, and 390 adaptation are delivered in sync.
4. Errors state what happened, the next step, and whether results / consumption are retained; no capacity means no queuing, the suggested time is not a promise, and StoreUnavailable preserves the original ID.

Deliverables: real web pages / API client, `examples/` call samples, and top-level installation and usage guides. Acceptance: configuration errors / missing permissions / empty states / failures / successes all have executable next steps; samples contain no keys, no fake successes, and no quota data.

## T5.2 Queries, Management, and Repair

1. Complete the historical and current projections of getOperation/inspect: results, output availability, known / conservative usage, reservations, verification, and the choice at the time; queries never change state or trigger network sends.
2. Wire up and complete the operations query / UI entry points and audit for cancel, refreshObservation, adjust, reconcile, and clearHold; reuse the T2 cancellation and the T3/OPS-01 management transactions instead of implementing a second accounting path from scratch; permissions are checked first, accounting IDs are idempotent, and evidence has provenance.
3. Rehearse credential repair, account billing blocks, window recovery, config_changed, unknown evidence collection, and observation-pending-refresh; releasing a hold / removing from a pool / disabling a resource never adds balance / deletes unknown reservations / resends.
4. For email, rehearse unified suppression and sender-authorization repair; the host's responsibility for receipts is clear, and an accepted submission is never dressed up as delivered.

Deliverables: the authorization management entry point, the operations disposition manual, and redacted walkthroughs. Acceptance: A12/A13/A19/A21 (email) /A25; past results are never rewritten by current configuration, and management cannot bypass send permits.

## T5.3 Retention, Permissions, and Backup/Recovery

1. Per D05, deliver body / summary / outputRef retention, cleanup, access control, and metadataOnly; dedup identities and unknown evidence are not auto-deleted in the first version.
2. After consumer interruption / no consumer / host restart, the original ID remains queryable; verify that body availability is honest and that paid-for usage and result references are never silently dropped.
3. Real-database backups cover operations, versions, resources, evidence, and unreleased reservations; before restoring a historical gap, sending is sealed off, and restoration follows the evidence after verification. Rehearse failed interruptions and old backups, not merely tests that files can be read.
4. Verify that credentials never appear in logs / trace, and that the operational identity is isolated from business namespaces; a closed permission cannot be obtained by self-claiming it through browser body / CLI parameters.

Deliverables: retention / cleanup policy implementation, backup / recovery manuals, and rehearsal evidence. Acceptance: A11/A13/A14/R16/R17 consistent with the D05 specification; unknown reservations and dedup identities cannot be wiped by recovery or cleanup.

## T5.4 First Use and Phase Exit

1. Find an integrator who did not participate in the implementation and have them complete one minimal action or view real availability following a guide with complete prerequisites. Record actual time, sticking points, and evidence; if there is no independent integrator, N02 remains pending verification, and developer self-testing must not stand in for it.
2. The target is the 60 seconds of PRD N02; the starting point and prerequisites are explicit, and plan verification or credential application must not be counted as already solved by ANT. Any blockage must have a real cause and a next step.
3. Walk through call → query → cancel or failure / verification → repair → explicit new action, recording positive and reverse paths separately; avoid resending unknown under a new ID.
4. Take per-page screenshots of the 7 page types at desktop 1280/1440 and 390; walk the main path and reverse flows on a real Go + DB + Web production build; check that T5.1–T5.3, A10–A12/A26–A29, and email A21 are complete, and close D05 after operations verification. If usability falls short, fix the guide / presets / interfaces first; do not merely adjust the measurement criteria.

Deliverables: first-onboarding records, the complete operations walkthrough, D05 closed, and T5 exit. Next: [T6 Performance and Delivery](06-PERFORMANCE-AND-DELIVERY.md). The first-version graphical configuration console must be delivered; it does not expand into a public multi-tenant commercial platform.
