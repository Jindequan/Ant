# T0 · Scope and Admission Finalization

Goal: make subsequent implementation face a real business, one deployment, and verifiable accounts, rather than picking providers out of thin air. Prerequisite: none. Primary owner: project / business; integration and adapters jointly produce outputs. Related: PRD §1–5/§7–8, DESIGN §1/§3/§5/§9.

## Work Items

### T0.1 Real Scenario and Success Boundary

1. Collect the actual caller, host, and one real request: why two accounts are needed, inputs / outputs, frequency, non-repeatable side effects, and what business success means.
2. Choose the first domain from AI and email, and write out one action plus its failure / audit path. For email, confirm whether success means submission acceptance or final delivery; for AI, confirm streamed content, completion, and usage dimensions.
3. Record controllable test content, account permissions, consumption scope, and observation methods; onboarding examples must not send real customer business or reference unauthorized accounts.
4. Use real usage records or minimal verification to prove the scenario exists; if there are no materials, register pending inputs to obtain — do not fabricate interviews or traffic.

Deliverable: the scenario, closed loop, scope, and applicability sections of `docs/implementation/BASELINE.md`; the scenario portion of D01. Acceptance: able to restate "who submits what, what ANT returns, and how failures are handled"; the first domain and success boundary are unique; confirm that Go server + Web configuration / resource pools / manual ordering belong to the first version; real outbound calls for the second domain may come later, but the heterogeneous configuration model is not deferred.

### T0.2 Deployment and Technology Finalization

1. Confirm whether the execution host is a single-machine long-running process or a cross-host shared service; how caller disconnects, process exits, and sweeps are executed.
2. For a single host, choose one applicable SQLite implementation; for cross-host, choose one shared-transaction Store. Multiple machines each running SQLite must not simulate shared quota; the first version does not implement both Stores at once.
3. Implement the delivery shape already decided by the user: Go server + independent React / TypeScript / Vite Web, HTTP + SSE; verify supported runtimes, database drivers, package managers, and test tooling, and pin versions / lockfiles. Consult official materials; do not set dependencies from memory of old versions.
4. Determine the permission source, time source, persistent directory / connection method, and backup and process-liveness constraints. Serverless cannot assume async tasks continue after the call returns.

Deliverable: BASELINE's deployment topology and technology choices, plus the D02/D07 closure records and rationale in STATUS. Acceptance: exactly one first-version deployment and Store, with clear authorization and transaction boundaries; the resident server supports the chosen business lifecycle, and browser exit does not end the running host. Reversible tool choices are up to the executor; actual host facts must not be guessed.

### T0.3 Two-Provider / Account Admission Verification

1. For the chosen domain, confirm two providers, actual accounts, bindings, plans, credential references, and sharing relationships; distinguish account totals, key-specific rate limits, and binding capacity.
2. Verify per resource: unit / rounding, fixed or rolling window, timezone / reset, concurrency, waterfall, expiry, each billed dimension, and the enforcement source of upper bounds. Record official sources, verification dates, applicable plans, and maintenance responsibility.
3. Verify the initial balance / consumption entry point, billing hard stop, unsent / finished evidence, observability APIs, and time / watermark reliability; where something cannot be confirmed, reject the corresponding strict capability, and never substitute empirical averages for upper bounds.
4. For email, verify sending authorization, host-level unified suppression, and submission / delivery responsibility; for AI, verify inference / tool / cache / error usage and streaming cancellation capability.
5. Produce an admissible capability table; when necessary rules cannot yet be expressed, switch to a verifiable candidate or record the blockage — never omit rules and force enablement.

Deliverable: `docs/implementation/PROVIDER-EVIDENCE.md`, two sets of redacted account / binding / resource materials, and closure evidence for D01/D03 in STATUS. Credential bodies do not go into configuration / documents; they are stored encrypted only via the secret interface or reference an approved external secret source. Acceptance: every required dimension has evidence or an explicit rejection; multiple keys do not multiply account quota; the chosen strict policies leave no uncovered billed items; the spending and side-effect scope of test actions is explicit.

### T0.4 Load and Implementation Baseline

1. Based on the real scenario, confirm accounts / resources / historical volume, concurrent long tasks, submission rates, synchronous / durability requirements, and execution deadlines.
2. For each PRD candidate performance target, confirm item by item or adjust with scenario grounding; freeze before development, and record hardware / deployment assumptions and measurement methods.
3. Record phase owners, applicable A/N/I items, and per-item blockages; estimate actual effort for work items, and do not commit to dates before resources are confirmed.
4. Check that D01–D03/D07 are closed and D04 metrics frozen; link inputs / verification materials into STATUS.

Deliverable: BASELINE's load and scope freeze records, D04 metrics, and T0 exit evidence. Acceptance: targets are measurable with realistic load sources; undecided D05/D06 are left to T1 spec freeze; candidate metrics are never treated as already-passed performance.

## Phase Exit and What Follows

T0.1–T0.4 complete, D01–D03/D07 closed and D04 metrics frozen; both providers are implementable and real materials are accessible. Where required inputs are still missing, write the blockage and its release conditions into STATUS and continue other independent verification; T1 must not close T0 with an arbitrary default.

Next item: [T1 Contracts](01-CONTRACTS.md). This phase may minimally verify scenarios and provider facts; it does not deliver an independent business sender that bypasses the future core, does not start unverified new-domain real outbound calls or production deployment; UI configuration and the pool model belong to the first version.
