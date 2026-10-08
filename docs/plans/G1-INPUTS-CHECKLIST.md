# G1 Exit Inputs Checklist (IN-01–04 and IN-05)

Established 2026-10-03. Purpose: before G1, Codex verifies the real facts of the IN chain, deployment decisions, and specifications to be frozen; this document distinguishes user inputs from the executor's work and does not treat all specifications as materials the user must fill in. Once inputs are obtained, verified, designed, and accepted, execute IN-01–04 (G0 exit) and IN-05 (G1 exit); then Codex implements the T2 leaves per [TASK-TREE](TASK-TREE.md).

Hard constraints (quoted from [TASK-TREE](TASK-TREE.md) and the task-card acceptance; applicable throughout):

- Real materials must not be impersonated by local/test snapshots; each item must note its source and date and be traceable.
- Secret plaintext is stored only in approved credential sources (environment variables / secret management); documents, STATUS, chat, and the repository may only hold references (mask/ref/revision); any appearance of plaintext violates IN-01 acceptance.
- Unapproved numbers remain candidates and must not masquerade as frozen metrics; before test actions are authorized, no real outbound calls may be executed.

Received as of 2026-10-03: first-domain AI calling, single-host deployment, and candidates DeepSeek API / Alibaba Cloud Token Plan and free 1M models / Volcano Ark Coding Plan. Codex drives public evidence gathering, specification, and acceptance preparation; the user does not need to re-answer choices already made. Still to be obtained: the actual calling application, exact plans and account details, credential references, and test count / cost authorization; for the latest progress and proposals see [BASELINE](../implementation/BASELINE.md) and STATUS. Three candidates do not equal three admittable accounts; the plans' applicability to ANT, enforced upper bounds, and private balances must be verified.

## Collect the Minimum User Inputs First

The user provides four items to start the collection/design work; filling in every technical item below at once is not required (tightened per user enumeration on 2026-10-03; public facts and specifications are borne by the executor; progress in [BASELINE](../implementation/BASELINE.md)):

1. The actual AI application/workflow and the definition of success: which application/process is the first caller, and what counts as one successful business operation.
2. One real redacted request and its usage context: a minimal hint without customer data may come first, but real usage context must corroborate that the scenario exists.
3. Test call authorization: the allowed number of calls or cost cap, and whether cost-incurring calls are allowed (including the authorized scope of the credential "test connection" action).
4. Two accounts' facts: actual plan name/tier and account/project IDs, the ref of the approved credential source (no plaintext), and initial balance/usage evidence with acquisition timestamps (item by item see [PROVIDER-EVIDENCE §5 account cards](../implementation/PROVIDER-EVIDENCE.md)).

These are the entry point of the collection work and do not mean G0/G1 automatically holds. Actual success boundaries may be drafted by the executor based on the calling process and then clarified; public plan / metering / cycle / limit facts are researched by the executor from official materials. For facts that cannot be publicly verified, such as account balances, private quotas, and sharing relations, the executor first locates the gaps, then asks the user to provide or authorize reading the corresponding account materials.

Host liveness/sweeping, the identity and permission scheme, disconnect/exit handling, Clock/lease/cancellation guards, buffer/save/restore parameters, and the load validation scheme are proposed with justification by the executor and verified and frozen per existing roles/milestones. Behavior already specified by existing norms is adopted directly; the user only rules on real business boundaries, authorization/costs, and necessary choices affecting data retention, and does not design algorithms for the executor one by one or approve routine technical implementations.

If there is no real traffic, the load record is marked not obtained; the executor may first design the validation scheme using the PRD's existing candidate targets, but must not fabricate real statistics or write candidates as frozen. Before stage acceptance, baseline/specification freezing is still completed per IN-04/05. While inputs are missing, public evidence gathering on the IN chain, specification proposals, and acceptance design may proceed, but these must not be called T2 coding having started.

The tables below are the **verification content to be completed jointly**, not the user's homework list.

## IN-01 First Domain, Caller, and Success Boundary (closes D01)

| # | Content to complete | Explanation |
|---|---|---|
| 1 | First real outbound-call business domain | Selected from the PRD candidates: desktop AI calling, or SaaS server-side transactional email; another may be designated, but a rationale is required |
| 2 | Real caller | Which application / business process issues requests; who pays and who consumes the results |
| 3 | Definition of success | What counts as one successful logical operation; one minimal real request sample (once redacted, usable directly as the seed of the T1.4 real sample) |
| 4 | Allowed test action scope | Which calls may really be sent; whether cost-incurring calls are allowed; an explicit list of forbidden actions |
| 5 | Failure handling requirements | The caller's expected behavior on blocking, unknown, and partial success |

## IN-02 Deployment, Identity Source, and Tooling Baseline (closes D02)

| # | Content to complete | Explanation |
|---|---|---|
| 1 | Deployment shape | Single host or cross-host (cross-host requires a separate evaluation of a shared transactional Store; the current reference Store integrates as single-host SQLite) |
| 2 | Host liveness and sweeping method | How the server stays resident (systemd / container / manual); after a crash, who cleans up external execution reservations |
| 3 | Identity and permission source | How Web users/namespaces are identified; who grants configuration modification rights |
| 4 | Disconnect and exit semantics | How in-flight attempts are handled when the browser disconnects; what in-flight states are allowed when the server exits |
| 5 | Deployment environment verification | Compatibility of the target host's OS / Go / Node versions with the D07 tooling baseline |

## IN-03 Two Providers' Facts and Admission (D01 closure + D03 verified closure)

One per provider account (**two** real accounts required, spanning different providers; multiple call channels sharing one account must explain the sharing relation):

| # | Content to complete | Explanation |
|---|---|---|
| 1 | Provider and account identity | Which provider, account/project identifier, plan name; no secrets included |
| 2 | Call binding | Endpoint, credential reference (a ref pointing to the approved source), and the protocol shape required by the adapter |
| 3 | Metering and cycle rules | Billing dimensions, units, deduction order; kinds of cycles/windows and time zone (evidence must match the window semantics already accepted in T1) |
| 4 | Upper-bound and guard evidence | Hard upper bounds per dimension, rate/concurrency limits, billing hard stop; note the evidence source (plan documentation / console pages) and acquisition date |
| 5 | Sharing relations | Which channels/projects share the same quota; initial balance/usage snapshots and their dates |
| 6 | Closure and observation basis | How external closure is determined; the observation source, query method, and freshness for usage/status |
| 7 | Authorized actions | The test actions allowed on this account (aligned with IN-01 item 4) |

The executor must locate the source for each dimension; when the necessary basis still cannot be obtained, write "not admittable" explicitly, and never fill in estimates. The user need not research all provider rules personally.

## IN-04 Load and Applicability Scope Freeze (freezes D04 candidate values)

| # | Content to complete | Explanation |
|---|---|---|
| 1 | Real load | Daily request volume, peak concurrency, typical request/response sizes |
| 2 | Latency and endurance metrics | p95/p99 targets; continuous runtime requirement |
| 3 | Stream backpressure and buffer budget | Behavior and buffer caps on disconnect / slow consumption in AI streaming scenarios (if the first domain is not AI, state not applicable) |
| 4 | Environment | Differences between the measurement environment and the production environment |

Until then, the RuntimeSettings candidate values in the design documents are all treated as candidates.

## IN-05 Real Presets and Runtime Parameter Freeze (executed after G0 exit; freezes D04 + D05/D06 specifications)

After G0 (all of IN-01–04 accepted), the executor completes the following compilation and technical specification freezing based on verified facts, with no new per-item user approval gates. Necessary rulings are requested from the user only when authorization, costs, actual deployment / business boundaries, or data-retention trade-offs are involved; everything else is proposed, verified, and accepted by the responsible roles:

| # | Executor to complete | Explanation |
|---|---|---|
| 1 | Real bundle compilation | The two accounts' real facts are mapped into the verified compiler; a full Compile passes or a concrete blocker is given |
| 2 | Runtime parameter freeze | Clock, lease/renewal/sweep deadlines, cancellation guards, unknown reservation recovery, on-demand collection frequency/timeout — the executor justifies against design candidate values, states failure behavior, and freezes per the original milestones |
| 3 | Save and cleanup policy | Scope and cleanup of saved output bodies, deduplication, unknown evidence saving and replay (D05) |
| 4 | Credentials and runtime permissions | Secret rotation and least-privilege explanation (D05) |

## How to Provide

- Document-type materials (scenarios, rules, upper-bound evidence) may be pasted directly or placed under `docs/implementation/` (the formal artifacts of the IN leaves are filled in at execution time; the user's original materials may be attached in that directory in files noting the dated source).
- For secrets, only register "credential reference + source + validity period"; once the plaintext has entered the approved source, the ref is communicated back.
- Once facts and the executor's specification work are both complete, Codex converges in the order IN-01→IN-02/IN-03→IN-04 and exits G0, then executes IN-05 to exit G1, and then implements the remaining T2 leaves per dependencies. RUN-01/02, API-01, WEB-01/02 may be prepared in advance per their dependencies; AUTH-01/CFG-08 and LED-01/02/04 have been implemented locally in G1-DEV and independently accepted, see `docs/evidence/WORK/G1-DEV/INDEPENDENT-REVIEW.md`. LED-03's original acceptance has been reopened due to an over-int64 reporting gap; the ruling and fix are in `docs/evidence/WORK/LED-03/REOPENING-REVIEW.md`; the original six-leaf batch must not be described as fully accepted today.
