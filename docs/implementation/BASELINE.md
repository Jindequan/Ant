# AI First Slice Implementation Baseline

Date: 2026-10-04. Plan P1.3 / Design V0.8. Owner: Codex has full authority over implementation, verification, and acceptance. This document registers real choices and reviewable proposals, and **is not a G0/G1 exit proof**; the actual state of leaves is tracked only in [STATUS](../plans/STATUS.md).

## Business Choices Already Obtained

Source: the 2026-10-03 session, in which the user said "you drive and push this forward" and answered the input questions as follows.

| Item | Fact already obtained | Scope still requiring verification |
|---|---|---|
| First domain | AI calling | The actual calling application, one real sanitized request, and its usage context have not been obtained |
| Deployment boundary | Single host | The actual runtime host/OS, residency mode, and identity source are unconfirmed; multi-host shared SQLite is not the default assumption |
| Provider leads | User's original words: DeepSeek (API), Alibaba Cloud (token plan, free 1M models), Volcano Ark (coding plan) | The three are candidates; the two admissible real accounts have not yet been selected based on evidence, and plan leads must not be treated as initial quota |
| Test authorization | No allowed count / cost cap given yet | For now, only public-document verification and local read-only checks; real provider tests await authorization, and an existing subscription must not be used to infer permitted costs |
| Credentials | No reference / source registered | Do not search private credential files, and do not write secrets into this document |

The first version keeps the PRD's choices: Go resident server + standalone Web, HTTP + SSE, and configuration / heterogeneous pools / manual ordering / server selection. The AI choice does not remove the heterogeneous configuration requirement; real outbound calls for the second domain remain outside the current slice.

## IN-01 Scenario and Closed-Loop Draft

Target closed loop: the actual application submits an AI request with a stable logical ID; ANT selects among verified compatible bindings, reserves, sends exactly once, records streamed output and consumption, and then provides final-state / history queries. Same ID with same content retrieves the original operation; same ID with different content conflicts. The client does not read provider secrets directly.

The success boundary is suggested to follow the current PRD / design: submission acceptance, the first stream chunk, and complete business completion are recorded separately; success is judged from the fact of business completion and the persisted final state, and truncated or partial output must not pass itself off as complete business success. Even after business success is confirmed, actual usage or external termination evidence may still be missing: with an authenticated upper bound, account conservatively; without a reliable termination proof, keep the reservation pending confirmation. The credibility of accounting and of external reservations is surfaced separately; success labels must not be used to fabricate consumption or termination facts.

Failure handling carries over: return quickly with a stated reason when there is no capacity; after partial output becomes visible, do not automatically stitch in another provider; for unknown, do not automatically resend or switch IDs; cancellation records intent and must not be read as a refund or that the provider has stopped; queries / verification may append facts but never erase the original history. Sources: PRD §6.1/6.2/6.4, design/03-EXECUTION.md, design/05-RUNTIME-AND-ADAPTERS.md. This draft must be checked against the actual calling application before acceptance.

Real requests, input / output ranges, and frequency are not yet obtained. Later acceptance seeds may use minimal prompts containing no customer data, but preparing seeds cannot substitute for the real usage context or prove the scenario exists. Which API protocol a real request maps to is decided jointly by the caller and provider facts; ANT must not claim in advance that it already supports a direct compatibility endpoint of some desktop tool.

## IN-02 Existing Engineering and Deployment Proposal

The first-version Store is suggested to keep the modernc SQLite accepted in T1: one write queue + a read pool, with a single-host persistent directory. Host, permissions, exit, and recovery specs are in [RUNTIME-PROPOSAL](RUNTIME-PROPOSAL.md); the proposal parameters there are not yet frozen.

Local verification for this round (2026-10-03; this development machine, which does not represent the yet-unconfirmed deployment target):

- `uname -sr`: Darwin 27.2.0.
- `sw_vers`: macOS 27.2, BuildVersion 26B5091g.
- `go version`: go1.27.1 darwin/arm64; `node --version`: v22.18.0.
- `make verify-manifest`: `manifest clean (184 files identical)`.

Existing verification of tool compatibility, real SQLite migration / writer, and process health / exit is in [T1-CODEX](../evidence/T1-CODEX/README.md). The implementation has not changed, so the full test suite is not re-run. D02 has only obtained the single-host business boundary; D02/D07 are not closed until actual residency and permission verification is complete.

## IN-03 Evidence Gathering and Candidate Selection

The public evidence for the three providers and the concrete reasons they currently cannot be admitted are in [PROVIDER-EVIDENCE](PROVIDER-EVIDENCE.md). The executor checks plan terms, metering dimensions, sharing rules, endpoints, upper bounds / stops, and observation evidence; the user only supplies account-private facts, the actual application, and authorization. Public rules are not account balances, and multiple keys are not multiple account quotas.

Verify first the standard API paths and the real product attribution of Alibaba Cloud's stated Token Plan / free quota; for the Volcano Coding Plan, separately check the applicable callers and dedicated endpoints — it must not be automatically replaced with pay-as-you-go APIs. Which two are finally chosen is decided by implementable evidence; the third stays as a candidate, and admission conditions are not lowered just to make up the count.

## IN-04 Load and Applicability Preparation

The verification plan is in [LOAD-ACCEPTANCE-PROPOSAL](LOAD-ACCEPTANCE-PROPOSAL.md). Real daily call volume, concurrency, and request / output sizes are not yet obtained; the PRD candidate metrics serve only as design inputs — not frozen, not measured. Without real load, first propose an executable verification workload and a realistic way to obtain it from real sources; do not pass it off as actual statistics.

## Implementation Baseline and Current Responsibilities

The final T1-CODEX acceptance baseline of 184 files carries over. IN-01–04 and IN-05 still proceed through the T0/G0/G1 stage gates; preparation work does not mean real inputs are complete and does not authorize real provider requests. Codex is responsible for public evidence gathering, spec verification, implementation integration, and acceptance; the user only needs to supply private facts such as the real business, accounts, credential references, and cost authorization.

Order of progress: complete and accept IN-01 → merge existing preparation into IN-02 / IN-03 and obtain real accounts and deployment verification → IN-04 freezes targets, G0 exit → IN-05 real compilation and freeze D04/D05/D06, G1 exit → Codex advances the T2 thin slice per the task tree. There is currently no committed arrival date for external account materials, and completed documents do not substitute for coding / outbound-call acceptance.

## Preparation Deliverables of This Batch

Codex has organized public provider evidence, single-host runtime specs, and the AI load acceptance proposal. They support subsequent IN-02/03/04 decisions but do not substitute for private accounts, real calling scenarios, cost authorization, or stage acceptance evidence. The sources, dates, and admission judgments for provider facts follow [PROVIDER-EVIDENCE](PROVIDER-EVIDENCE.md).

## Public-Fact Independent Review and Candidate Plans (2026-10-03)

Codex completed an independent review of the IN-02/03/04 facts supportable by public sources and updated the preparation documents; leaf states remain pending and are not passed off as exits. This section records the admission analysis, the IN-05 candidates, and the private facts still to be supplied by the user.

### Spot-Check Independent Review of Evidence Gathering (IN-03 input)

- **Alibaba Cloud international-site Token Plan blog: successfully verified by both parties independently.** The browser-source independent review succeeded, consistent with the record in [PROVIDER-EVIDENCE](PROVIDER-EVIDENCE.md) §1/§3: compatible with any OpenAI/Anthropic-compatible tool (a protocol fact); the same page's FAQ explicitly says "designed for interactive use … not for automated scripts" and recommends standard pay-as-you-go API keys for programmatic production loads; Credits use a 5-hour + 7-day double rolling window with no carryover; article dated 2026-07-19. Incremental public evidence: the official FAQ itself directs users to "backend/production programmatic loads → standard pay-as-you-go API", directly supporting the conditional recommendation combination below.
- **The Volcano subscription dedicated terms page: still single-party reading.** Static fetching once obtained only an empty body (the page renders dynamically), so the §2.8 original text could not be independently verified. The cautious judgment in [PROVIDER-EVIDENCE](PROVIDER-EVIDENCE.md) §1 is unchanged, but the source is marked "previously read by a single party only"; before Coding Plan admission, the original terms text must be independently reviewed by a human or tool via a renderable method, and admission must not rest on secondhand paraphrase. (Correction on the evening of 2026-10-03: the "must be renderable-reviewed" condition stated in this item was satisfied the same day — a second-party verification was completed via browser rendering, and Codex re-read the official text to review it; §2.8 matched, and §2.9/§2.11/§3.1 were newly registered; the not-admissible judgment is strengthened. See [PROVIDER-EVIDENCE §1.1](PROVIDER-EVIDENCE.md).)

### IN-02 Converged Conclusion (not exited)

[RUNTIME-PROPOSAL](RUNTIME-PROPOSAL.md) §1 serves as the execution checklist for D02/D07 host verification: verified = this development machine (Darwin 27.2/go1.27.1/node 22.18.0/manifest clean, see above); to be verified = target host OS/architecture/disk persistence, residency mode (settled once systemd or container is chosen), identity source, and stop timeout. D02 remains open; all proposal parameters are candidates.

### IN-03 Not-Admissible Markings (under current public evidence; re-reviewed against account evidence before activation)

| Candidate | Marking | Basis |
|---|---|---|
| Alibaba Cloud Token Plan (domestic/international) | **Not admissible (ANT backend architecture)** unless the user additionally provides evidence of an applicable permit / written exception | Both sites' FAQs exclude automated scripts / backend integration; protocol compatibility does not constitute a permit |
| Volcano Ark Coding Plan | **Currently not admissible** | The subscription terms limit usage to interactive use with approved tools (§2.8–§3.1 render-reviewed and Codex-reviewed against the official text, see [PROVIDER-EVIDENCE §1.1](PROVIDER-EVIDENCE.md)); standard `/api/v3` calls are billed separately rather than deducted from the plan; Coding Plan has no proven balance / observation API (GetAFPUsage is Agent Plan only) |
| DeepSeek standard API | **Technical candidate (one of the top choices)**; account / cost authorization not obtained | Public Chat protocol, itemized billing, balance API, and finish_reason/usage termination evidence are all in place |
| Alibaba Cloud standard model real-time API (Beijing, free quota optional) | **Technical candidate (second choice)**; requires the user to explicitly choose this combination | The standard channel and the plan Key/Base URL are not interchangeable; free quota is 1M per model / 90 days / shared between main and sub accounts, and stops when exhausted (403 FreeTierOnly) or switches to pay-as-you-go (protection has an activation delay) — this is a user ruling item |

The conditional recommendation combination stands: **DeepSeek standard API + Alibaba Cloud standard model real-time API**; the third (Volcano) stays as a candidate, and admission is not lowered just to make up the count.

### IN-04 Converged Conclusion (not exited)

[LOAD-ACCEPTANCE-PROPOSAL](LOAD-ACCEPTANCE-PROPOSAL.md) stands as the acceptance-design preparation deliverable for IN-04: the four record states are registered (real load = not obtained; candidate targets = PRD §8 + design05 starting values; frozen / measured = none); span definitions, the backpressure matrix, and the A01–A30/N01–N07 applicability mapping are ready. The 36,000 submissions (20/s×30min) are a scale calculation, **not call authorization**. D04 remains open; freezing happens at the IN-04 exit after IN-02/03 acceptance.

### IN-05 Candidate Proposal (executed after G0; currently a proposal, not frozen)

Per the task card, IN-05 = real bundle compilation + closing D04 + freezing D05/D06 specs. Candidate composition (all pending G0 and account facts):

- **Candidate bundle A (DeepSeek standard API)**: the descriptor must register itemized billing dimensions (input/cache-hit/cache-miss/output tokens + usd, peak/off-peak periods), a balance-based entitlement (granted credit takes precedence over top-ups, no monthly window), and concurrent occupancy tiers; usage/finish_reason serve as termination-evidence projections. Gaps: initial account balance snapshot, model version freeze, proof of whether reasoning counts toward the output cap.
- **Candidate bundle B (Alibaba Cloud standard API + free quota)**: Beijing region, per-model 1M/90-day independent expiry (not a fixed window), main/sub account sharing facts, stop-when-exhausted (403) or switch-to-pay-as-you-go chosen by the user with slack left for the activation delay; `stream_options.include_usage` metering projection. Gaps: account/model snapshot, evidence that the protection switch is in effect, verification of the thinking cap.
- **D05/D06 freeze candidates** (citing RUNTIME-PROPOSAL §2–§4; frozen by the responsible role after G0 per the original milestones): body retention 7 days (a user trade-off ruling item), 7 daily backups kept, maxAttempts=1, default deadline 120s, graceful 30s, settlement ctx 5s / 30s total per invocation / at most 3 replays, lease 30s / heartbeat 10s / sweep 5s.
- **Credential test action**: wired per [CONFIG-ONBOARDING-COVERAGE-REVIEW](../plans/CONFIG-ONBOARDING-COVERAGE-REVIEW.md) (explicit, controlled system_probe / accounting, user-triggered); the probe API comes after RUN-01/02, and until then WEB-02 shows "not yet verified online".

### User Private-Fact Manifest (minimal set after convergence; consistent with [G1-INPUTS-CHECKLIST](../plans/G1-INPUTS-CHECKLIST.md))

1. The actual AI application/workflow + success definition; 2. one real sanitized request + usage context; 3. test call count / cost cap + whether paid calls are allowed; 4. the two accounts' actual plans/IDs + approved credential refs + initial balance evidence (item by item per the [PROVIDER-EVIDENCE](PROVIDER-EVIDENCE.md) §5 account cards). Technical parameters (Clock/lease/buffering/retention/load plan) are the executor's responsibility; the user is not asked to fill them in.

## G0 Readiness State (2026-10-04, G0-READINESS converged independent review)

A full reconciliation was done between each T0 preparation deliverable and current evidence. Everything the executor side can complete has been completed; all remaining gaps are user/account private facts, item by item as follows:

**Completed (executor side, no private data):**

- Public evidence gathering for all three providers is complete and two-person verified: DeepSeek billing/balance/error codes/Chat; Alibaba Cloud Token Plan on both sites + free quota + Base URL; Volcano terms render review ([PROVIDER-EVIDENCE](PROVIDER-EVIDENCE.md) §1.1 + the Codex review section); the 2026-10-04 freshness recheck found the three sets of key official pages still consistent (end of §5). The not-admissible markings and the conditional recommendation combination (DeepSeek standard API + Alibaba Cloud standard real-time API) stand and have been strengthened twice.
- The runtime spec proposal ([RUNTIME-PROPOSAL](RUNTIME-PROPOSAL.md), D02/D07 host verification checklist + D05/D06 candidate parameters) and the load acceptance design ([LOAD-ACCEPTANCE-PROPOSAL](LOAD-ACCEPTANCE-PROPOSAL.md), four record states + A/N mapping) stand as IN-02/IN-04 preparation deliverables; all parameters are candidates and not frozen.
- The development machine environment verification is on record (Darwin 27.2/go1.27.1/node22.18/manifest clean); the G1-DEV six-leaf batch (AUTH-01, CFG-08, LED-01–04) once received independent acceptance ([original summary acceptance](../evidence/WORK/G1-DEV/INDEPENDENT-REVIEW.md)), but LED-03 was reopened because usage exceeding int64 cannot retain a precise report under the storage design, see [reopening review](../evidence/WORK/LED-03/REOPENING-REVIEW.md). The other leaves' acceptance scope, and the local synthetic evidence for ledger cancel/renewal/sweep/unsent convergence and the permit lifecycle, remain valid.
- The inputs manifest matches this document's minimal private-fact set; the user is not asked to re-answer choices already given (first-domain AI calling, single host, three provider candidates).

**Not completed (all blocked on private inputs, which the executor cannot substitute for):**

| Gap | Blocked on | Unblocking condition |
|---|---|---|
| Actual AI application/workflow + success definition | IN-01 exit→D01 | The user provides the concrete calling application and the business success criteria |
| One real sanitized request + usage context | IN-01 exit→D01 | The user provides a minimal real sample and context free of sensitive data |
| Test count / cost cap + whether paid calls are allowed | IN-01 exit; credential test connection authorization (a CONFIG-ONBOARDING ruling) | The user explicitly authorizes the scope |
| Two accounts' plans/IDs + credential refs + initial balance evidence | IN-03 exit→D01/D03; D02 target-host facts→IN-02 | The user provides account facts (secrets go only into approved sources); target host OS/residency/permission verification starts with IN-02 |
| Real load | IN-04 freeze→D04 | Frozen per LOAD-ACCEPTANCE-PROPOSAL after the above IN-01/02/03 are accepted |

G0/G1 have not exited; IN-01–04 remain pending/blocked as they are. This section is a readiness snapshot, not acceptance evidence for any leaf or gate.

## IN-03 Official Terms Freshness Increment (2026-10-04)

After re-reading the current official product pages today, admission was recalibrated: DeepSeek has updated to V4.1-Flash/V4-Pro, with prices differentiated by cache hit/miss/output and peak/off-peak periods, and a maximum output of 384K; the Alibaba Cloud new-user free quota is usually 1M per model, Beijing region only, 90 days, but for verified accounts, if the per-model "stop when exhausted" switch is not enabled and in effect, it still switches to pay-as-you-go after exhaustion; the Volcano Coding Plan personal-edition overview explicitly states it cannot be used for API calls, and the dedicated terms' backend programmatic-call exception applies only where explicitly supported / separately authorized. Details, usage constraints, and official primary sources are in [PROVIDER-EVIDENCE](PROVIDER-EVIDENCE.md) §5. This increment strengthens the judgment that "Token/Coding Plans do not apply to the ANT backend; standard APIs still await user confirmation / account verification", and does not change the state of IN-01 missing private business inputs, IN-03 account facts not obtained, or G0 not exited.
