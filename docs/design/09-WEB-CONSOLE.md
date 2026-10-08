# Web Console Pages and Business Contracts

Version: V0.9. The user authorized an independent frontend and resource configuration; the first version may be plain. The current Web structure and the core configuration/operation chain are implemented, the visual language has been set as an AI resource routing console and has entered a page-by-page walkthrough; missing items are still filled in per these page contracts, and sample capacity must not masquerade as real balances.

Target users: administrators who maintain multi-provider credentials/resources themselves, and callers who submit and view operations. Core value: organize real resources in the UI, control the order of use within pools, let the server execute and explain the results. Real user needs already exist; actual usability still requires the T5 walkthrough.

## 1. Single main path and navigation

Navigation has only four items: providers, resources, pools, operations; the configuration guide is embedded in the first three, with no separate entry point of the same function. The first-run empty state's main CTA is "Add provider"; add → configure resources → join a pool and order → validate and activate → pool detail submit a minimal request → operation detail. Forms are multi-step, internal terms are collapsed, and pages do not lay ledger columns out verbatim.

The first batch has 7 page types in total (including login and detail pages); each page has one main task/main CTA, access permissions derive from the session projection, and the server remains the final authorization.

## 2. Page contracts

| Page | Single question / business and data | First screen / main CTA / secondary actions | Entry / success / failure recovery / leaving / permissions |
|---|---|---|---|
| Login | Which configurations and operations can I access? session identity and permissions | Admin token form; log in; view deployment connectivity status | Not logged in; success enters the first pending main path; invalid keeps non-sensitive input/retry; enter providers or the original target; body self-authorization forbidden |
| Provider list + configuration form | What real, usable call paths exist? catalog, account, binding, secret mask, explicit balance observations | Supported types, name, status; add provider; edit/deactivate; DeepSeek connection can explicitly query the balance | read to view, configure to write; after saving the draft the next step is resources; the query sends only one balance read request, results are kept by command ID and can be re-read; failure prompts retry/read, old configuration and old observations are kept; go to resources; secrets are write-only, shared accounts require confirmation |
| Resource list + configuration form | What quotas and rules constrain each path? resources, offers, units, windows, evidence | Owning path, type, quota source, availability; desktop table, 390px field cards retain real-time balances and evidence; configure a resource; manage identity can expand balance observation records | Requires a provider draft; complete bundle must be verifiable; missing evidence keeps the draft and lists the next step; go to pools; the ledger cannot be hand-edited nor unknown deleted; observations accept only admin records carrying a source number, never claiming automatic provider reads |
| Pool list + creation form | How are resources organized into call entry points? pools, capability grouping, members | Name, executable capabilities, status; create a resource pool; edit/deactivate | Requires offers / allows an empty draft; after creation go to detail; no members guides adding; go to pool detail; namespace authorization |
| Pool detail | Which resource does the next request use first? member/order/version, capabilities, blocking reasons | Current order, unsaved notice, status grouped by unit; save order when there are changes, otherwise execute a minimal request; add/remove members | Requires an existing pool; after saving the server version and order update; 409 keeps the draft and compares, failures are retryable; execution enters operation detail; configure orders, execute submits; the main CTA switches exclusively |
| Operation list | Which action needs review/handling? paginated OperationView | ID, type, result, unconfirmed reservations; view pending operations; filter; desktop table, 390px vertical info cards retain all fields | read; detail navigation; empty state returns to pool execution, errors retry; go to operation detail; unauthorized operations are not shown |
| Operation detail | What happened and what is the next step? result, output, actual/conservative consumption, old decisions, reconciliation and the manage-actionable basis | Factual result and next step; running can be cancelled / unknown can submit reconciliation; manage can adjust charges against persisted original deductions or clear a hold not blocked by open occupancy; expand history / download saved output | read; the cancellation ACK shows "requested" rather than "cancelled"; reconciliation and adjustment append records; manage cleanup is still atomically re-verified by the server; after losing connection, query by the original ID; return to the list; cancel/manage are controlled separately; one-click resending of unknown is not allowed |

The reconciliation form opens in the detail page only when it can map unambiguously to one unknown attempt; it submits the OpenAPI `ReconciliationInput` confirmed result, canonical unit quantities, and evidence references. On read-back it simultaneously shows the appended conclusion, evidence count, and per-resource corrections, while the original result continues to display as pending verification.

The manage user's adjustments and hold handling appear only on the operation detail page. Offsettable events and remaining quantities are computed from the persisted ledger and read back; a negative adjustment must bind the selected original event and must not exceed the offsettable balance, while a positive adjustment is recorded truthfully and can form an overrun hold. The hold read-back includes reason, scope, existing evidence, and current occupancy blocks; the clear command carries evidence and the server's write transaction re-checks open encumbrances. On success the detail page, operation list, and resource pool status refresh; ordinary read users do not receive admin projections.

The resource form includes: owning provider/account/binding, resource type, metering unit/precision, limit or entitlement, upper-bound/capacity source, fixed or required rolling window, expiry/replenishment rules, shared references, concurrency, and whether incremental payment is allowed. Only fields supported by the schema can be activated; unsupported types show a savable draft that is not executable. All of an offer's required constraints are preset, and users cannot uncheck them.

Ordering supports keyboard move up/down and numeric input, with dragging as a supplement; the UI states "used first" and "smaller priority number first". Before/after comparison of reordering, save state, and scope of effect are explicit. The provider's internal deduction order is displayed read-only with its source, and never shares controls with the manual priority. disabled and blocked are not the same word: the former is manually deactivated, the latter currently cannot execute.

## 3. Four states, errors, and the reverse path

Every page implements loading/empty/error/success simultaneously, plus the applicable states of no permission, missing credentials, expired evidence, no capacity, unsaved changes, version conflict, unknown, and more. Unknown balances are written "pending verification", never shown as 0; drafts are not shown as activated. Errors explain what happened, how to fix it, and whether the old configuration/results are kept.

Every button has a prerequisite disable reason, a backend action, success feedback, failure recovery, and a next hop. Deactivating/removing references shows the impact; undoing unsaved edits is easy, and rolling back saved state is a new version that goes through validation, without regressing balances or undoing outbound calls that already happened. A failed cancel keeps the original ID; a disconnected subscription shows reconnection and querying, and does not create a new operation. A failed secret configuration does not save plaintext to localStorage or state logs.

Client validation only assists; the server adjudicates with the same schema; priority does not use optimistic saving. GET read-back of the actual projection is required before showing success. Mobile 390 uses vertical member lists and move up/move down, desktop 1280/1440 uses tables, and business fields are never lost on narrow screens. Primary buttons 44px, color is never the only state signal, semantic alert/dialog and keyboard focus.

## 4. Frontend implementation boundary

React Router organizes routes; page data uses one query layer (TanStack Query, version locked by T1), and forms are generated from the catalog schema with supported controls; arbitrary catalog JavaScript must not be executed. The shared api/client unifies cookie/CSRF/errors/429 backoff queries; business submissions do not auto-switch IDs or auto-resend. The events hook deduplicates sequences and reads back output and the final projection on demand.

`tokens.css` centralizes font sizes, spacing, colors, states, focus, and reduced-motion. The visual direction is a cool-fog page ground, deep-sea navigation, teal primary actions, and amber pending-verification status; path node marks match the resource routing theme. Avenir Next works with system Chinese fonts, and the Operations and Resources desktop tables become vertical info rows with field labels at 390px. zh/en copy is kept in sync, times/units localized. Page visuals must still go through the page-by-page walkthrough in §5; current screenshot evidence is in the [visual system walkthrough](../evidence/WORK/WEB-VISUAL-SYSTEM/README.md).

### Visual direction

| Token | Value | Role |
|---|---|---|
| Fog | `#edf2f1` | Cool page ground, keeps dense data comfortable to scan |
| Paper | `#ffffff` | Main reading surface |
| Marine | `#17383e` | Navigation and product frame |
| Route teal | `#08766d` | Primary action and active focus |
| Signal lime | `#c8e47b` | Small route-node mark and active navigation indicator |
| Review amber | `#8a5a10` | Unknown outcomes that require a human check |

Typography uses Avenir Next for Latin with PingFang SC / Hiragino Sans GB fallbacks for Chinese. Page titles are left-aligned and compact; long explanations stay secondary to the current task.

```text
Desktop 1440
┌ ANT · suppliers · resources · pools · operations ───────── identity ┐
│ Page title and one-line context                                      │
│ Filter                                                               │
│ Full-width operation table                                           │
└──────────────────────────────────────────────────────────────────────┘

Mobile 390
┌ ANT · identity ─────────────────────────────────────────────────────┐
│ suppliers · resources · pools · operations                            │
│ Page title and context                                                │
│ Filter                                                                │
│ Operation record: field labels beside values                         │
└──────────────────────────────────────────────────────────────────────┘
```

The route-node mark is the one identity accent because ANT coordinates provider paths. Status color carries meaning only where a decision is needed; the rest of the console stays low-noise. This was reviewed against the generic dashboard treatment: there are no gradient washes, decorative metric tiles, or repeated shadow cards. Screenshots are a first visual pass on the Operations page, not acceptance of all seven page types.

## 5. Acceptance

| Given | When | Then |
|---|---|---|
| Empty database, admin prerequisites complete | first login | honest empty state and the add-provider main path, no fake data |
| Two real provider offers in the same pool | adjust and save priority in the UI, then execute | GET read-back shows the same order, the real server selection matches the path; not just a list swap |
| 390 screen / keyboard users | adjust order, submit, view results | all operations completable, disable reasons present, no horizontally hidden key fields |
| Activation failure / version conflict | save configuration or order | old activated version stays valid, draft kept, field reasons and comparison entry visible |
| SSE interrupted, server still alive | reconnect / query the original ID | returns persisted events/results, no repeated provider outbound calls |
| Read-only identity | attempt to write configuration / ordering / adjustments | UI restriction + real API 403, dual-identity verification |
| configure user selects their own current credential, observation already saved | reload the provider page with the commandId | GET shows the same observation and sends no provider request; currencies displayed separately, local observation time preserved |
| configure user selects a credential not their own or an old revision | request a balance observation | API rejects and the provider request count is zero; existing drafts unchanged |
| A user with manage permission reads the provider balance, the page shows the current window and committed/adjustmentVersion | submit observation and validity period | the API saves the same source number and read watermark; GET /pools reads back the observation status; if the ledger watermark changes during the read, the stale observation is rejected and the original value kept |
| Read-only user visits the resources page | view balance actions | no observation form is shown; a direct POST still gets 403 from the server |
| Multiple pages of operation records exist, containing running, success, failure, cancelled, and pending-verification states | select one state, keep paging, open detail and return | each page contains only the selected state; cursors cannot be reused across filters; returning keeps the filter and page position; clearing the filter restores all states; when nothing matches, the filter can be cleared |

T2 first walks the real DB → Go API → minimal Web pages → configuration → pool → real execution → result; before this slice, no site-wide pages are built out. T5 completes the 7-page contracts, four states, and handling, with page-by-page desktop/390 screenshots, the real main flow plus one reverse flow, and N02 records the time taken by someone who did not participate in the implementation. No evidence, no done marking.

## Configure-first amendment (2026-10-07 user ruling)

**Rejected implementation record, not the accepted main-flow design.** On
2026-10-07 the user rejected the delivered page: “what a shit! do you know what
should you build??” The account-as-subscription terminology and raw ID/unit/
precision forms below describe historical behavior. They cannot supersede §1's
guided configuration requirement or PRD's requirement that provider presets
absorb accounting complexity. The corrected flow is now implemented separately in [SUPPLY-INVENTORY](../implementation/SUPPLY-INVENTORY.md), with current page contracts and [local evidence](../evidence/WORK/SUPPLY-INVENTORY/README.md). The description below is retained as historical behavior.

The resource form now lives in `/resources?draft=<id>`. Resources → Configure
resources creates a draft from the active revision or empty document. An account
is required for an account-owned resource, but a credential or executable binding
is optional during configuration. Subscription accounts for unregistered providers
and declared resource rules are savable, visibly pending verification. The saved
URL and identity/namespace-scoped last-draft pointer provide resumption. Existing
Providers forms can save and continue to the same resource draft.

The form covers owner, stable resource ID, metric, unit, precision, commercial
role, exact capacity, fixed/rolling/lifetime window, period/time zone/renewal
anchor, metering, currency and expiry. Unknown providers/semantics and missing
evidence remain activation-blocking server issues. Removing a referenced resource
is blocked; unreferenced removal is confirmed and committed only on draft save.
Leaving or reloading with edits offers a deliberate save/keep/discard choice.
A CAS conflict keeps local edits and their original version. Read-only identities
receive saved definitions with no mutation forms. Saved quotas are declarations,
not balances. Consumption acceptance remains a separate stage.


## Current configure-first inventory (2026-10-07 correction)

Providers shows named accounts and packages. Resources groups real subscription,
credit, free and pay-as-you-go packages by account; setup asks account, package,
then allowances and reset rules, with one save followed by server readback.
Shared account limits automatically constrain every package. Pools saves declared
compatible package references and their intended priority. Editing, version
review, cancel/leave, retirement/restoration, archive/restoration and read-only
access have explicit flows. The ten-part page contracts and four UI states are
in [SUPPLY-INVENTORY](../implementation/SUPPLY-INVENTORY.md). zh/en and
desktop/390px walkthroughs are recorded in its evidence. Legacy draft and
executable views are explicitly advanced views; supply pools do not execute.
