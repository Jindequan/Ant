# ANT Documentation Entry Point

Version: V0.9 technical design baseline · Date: 2026-10-04 (Asia/Singapore)

Latest user ruling (2026-10-07): configure resources first; consuming them is the next plan. The user subsequently rejected the delivered configuration editor: “what a shit! do you know what should you build??” The [resource configuration increment](implementation/RESOURCE-CONFIGURATION.md) is **not accepted**. Its storage checks remain historical technical evidence; they do not establish a usable subscription/resource setup flow. An account is not a subscription. The corrective scope is provider accounts, their actual plans/packages, the resources and limits those supply, and pool configuration with manual priority. Execution remains subsequent work. See [local setup](LOCAL-DEVELOPMENT.md) and [historical verification evidence](evidence/WORK/RESOURCE-CONFIGURATION/README.md).

The [supply inventory correction](implementation/SUPPLY-INVENTORY.md) was implemented and verified locally against Go/API/SQLite and the production Web build; see its [evidence](evidence/WORK/SUPPLY-INVENTORY/README.md). It added package/resource business objects and one-save setup, keeping executable configuration separate. **Retired 2026-10-08** (ENG-03, ruling R7): the supply inventory API, code and table were removed; schema v5 drops `supply_inventory` and its rows on the next server start.  The second rejection is recorded verbatim: “I don't know the result you have implement, what logic it is! rediculous form and stupid config progressing”. No user acceptance of the correction is claimed.


2026-10-07 latest product direction: the user rejected the universal package setup: “you are on a totally wrong way! every provider’s subscription, quota usage, time, and switch are individual”. The user directs Magpie research for AI routing/subscription management, then extension to other platforms/categories, with discussion before further implementation. [Research and proposed plan](research/MAGPIE-AND-ANT.md). The generic inventory remains a technically verified artifact but is **rejected as the product direction**. No replacement is implemented or accepted by this research.

## Currently Effective Documents

| File | Question Answered | How to Use |
|---|---|---|
| [PRD](PRD.md) | Who it is built for, what it accomplishes, and what the scope and acceptance criteria are | The sole source of requirements R01–R25, quality attributes N01–N07, and acceptance criteria A01–A30 |
| [DESIGN](DESIGN.md)+[Detailed Technical Design](design/README.md) | System hierarchy, modules/files, concrete interfaces, tables/indexes, transactions, and business algorithms | The core semantics and the technical attachments together constitute the effective design; I01–I10 remain applicable |
| [Implementation Plan](plans/README.md) | Work item order, deliverables, phase gates, and completion evidence | Codex is responsible for implementation, verification, and acceptance; task tree, execution standard, acceptance matrix, and status records |

This entry point governs authority uniformly. Requirements constrain design, and design constrains implementation; when an actual conflict arises, record the specific trigger condition and amend the affected clauses. The plan may refine implementation but must not lower the requirements for correctness, spending, permissions, or recovery. The G1-DEV six-leaf batch once received independent acceptance, but LED-03 was reopened because usage exceeding int64 could not retain its original value as the storage design specified; the six leaves must not currently be called fully accepted. G0/G1, real end-to-end calls, operational stability, onboarding experience, and performance remain unaccepted. The ANT Hooks contract and the basic registration framework are established, but the Router/Ledger/Transport wiring is not yet complete.

## Current Execution Entry

For the start-of-work boundary see [V0.7 Implementation Readiness Assessment](IMPLEMENTATION-READINESS-V0.7.md): T1.1 may begin; before the T1 contracts/T2 outbound calls, the configuration, handle, rejection-convergence, priority, and task seams listed in K1–K6 must be closed. This is an assessment record; the gaps have not yet been remediated and must not be misread as existing operational acceptance or as fixed-state declarations.

Start from the [Plan Entry](plans/README.md), read the execution standard and STATUS, then advance the work items whose prerequisites are met. The accepted T1 and G1-DEV artifacts are retained; the G0/G1 and real outbound call thresholds are unchanged. For specific files and methods see the [Implementation Map](design/06-IMPLEMENTATION-MAP.md). Deliver item by item and preserve evidence; when external facts are missing, complete the work that does not depend on them, and explicitly register the missing input and its impact.

Three rounds of static review have converged; V0.4 found no P0/P1, and the four P2 items were closed out in V0.4.1. This consolidation retains those valid contents and adds implementable modules, contracts, schemas, transaction algorithms, and execution tasks, without relabeling static passes as measured passes. Subsequent revisions target only what is actually found, amending only the affected content; a new round of comprehensive document review is not started by default.

## Current Implementation Prerequisites

The first business use case, the actual caller, provider accounts, actual deployment, and admission evidence are not yet settled; the core technology choices are the Go server/React+TypeScript+Vite Web/SQLite reference Store, with D07 pending verification of the host environment. The contents, owners, freeze/close conditions, and current status of D01–D07 are consolidated in [STATUS](plans/STATUS.md); this entry no longer maintains a separate decision table.

## User Rulings and Sources

- 2026-10-07 product rejection, verbatim: “what a shit! do you know what should you build??” The account-as-subscription model and raw accounting-field setup cannot be treated as delivered business functionality or revived as the corrected main flow. Preserve existing drafts and execution invariants while correcting the business model; no new schema or corrective implementation has been delivered by this rejection record.

- 2026-10-07: The user prioritizes the core over frontend work and requests free infrastructure, strong capabilities and extensible providers/resources/usage calculations. The bounded implementation and verification scope is [CORE-EXTENSIONS](implementation/CORE-EXTENSIONS.md); developer integration guidance is [Extending Ant's core](CORE.md). Go + local SQLite remain the runtime, with no new infrastructure service or paid dependency; real-provider and release gates remain unchanged.

- 2026-10-01: Things have an order of precedence; division of labor and incremental completion matter; oppose all-encompassing documents that offer no place to start.
- 2026-10-02: Requirements and design must both land in files; judge review items carefully, improve robustness through complete responsibilities and extension boundaries, and avoid item-by-item patching.
- 2026-10-02: Stop repeatedly polishing static documents; consolidate the latest complete version, and the execution plan must have structure and standards, landing in `docs/plans`. Subsequent execution is fully Codex's responsibility (2026-10-04).
- 2026-10-04: The user required ANT's internal Hooks to support flexible lifecycle extension. PRD V0.9 added startup-phase named checkpoint definitions and Guard/CandidateOrderer/Observer type constraints; the core must invoke them at real boundaries and must not dynamically load code.
- 2026-10-04 Hooks capability calibration: On top of the above, explicitly enabled Transformers are included; the core validates the initial value and every step's output, failures do not return partial results, and both events and transformed data are bounded. Requirement acceptance escalates to R25/A30 of PRD V0.9; real Router/ledger wiring still awaits the task gates.
- 2026-10-04 Hooks traceability calibration: PRD V0.10 requires every Hook to register a named version; Registry Seal generates a deterministic callback-free manifest and SHA-256 revision and injects them automatically into execution events. PRD V0.11 brings the effective RegistryOptions budget affecting callback execution into the manifest/revision; PRD V0.12 clarifies that the Hook API version is injected with the event and participates in the manifest/revision. This revision has not yet been written into the business DecisionRecord; that persistence belongs to RUN-04/A30 and is currently unaccepted.
- Design inputs: cite session `6abe4aa0-4fc4-83ee-b63e-ceda235ad041` and this project's discussions; sessions and review comments are not provider or performance measured evidence.

- 2026-10-02 technical completion: The user pointed out that V0.5 lacked technical depth; V0.6 filled in the then-core technology, subsequently replaced by this round's V0.7/P1.2, so the executor no longer has to infer the architecture from goals.

- 2026-10-02 delivery ruling (translated from the original Chinese ruling): **"Of course it is needed. The backend uses Go, making good use of its features to improve performance. For the frontend, the choice is yours; it can be crude at first, but it must support my configuring various types of resources of various types of providers, with resources of different types formed into pools, resources within a pool consumed by priority, priorities adjustable manually, and resource configuration in the user interface. Switching dispatch resides in the server."** V0.7 therefore froze the Go backend, an independent Web, UI configuration, logical resource pools, manual priorities, and server-side dispatch; the old Node backend and the "no graphical console" stance are no longer valid.
