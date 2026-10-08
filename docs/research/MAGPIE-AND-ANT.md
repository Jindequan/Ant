# Magpie research and Ant direction — 2026-10-07

Research completed by source inspection; architecture below is a proposal for discussion. No Ant application code, database, credentials, running service or browser state changed. This document refines the user direction; PRD remains the requirements authority. The previously delivered universal package wizard is rejected and cannot serve as the next product baseline.

## What the user wants

Ant should understand the services the user owns, including each provider's subscriptions, accounts, quotas, usage, timing and switching behavior; configure them naturally; and eventually route suitable work across them. Start with AI, prove the model with different AI providers, then extend it to other platforms and service categories. Running the core should require no paid infrastructure service. Provider usage charges remain a separate matter.

Recorded direction, verbatim:

> you are on a totally wrong way!
> every provider's subscription, quota usage, time, and switch are individual, don't make them in a **warped style, describe 'What I need, what I want to build'?**
> then discuss with the plan

> in AI, you should research https://github.com/yetone/magpie learn how to design a router and management of ai subscription, and then to other platform and other category of services

## Research basis and limits

Inspected Magpie's README/reference, Go provider/account/quota/routing implementations, gateway fallback/affinity/trace implementations, provider-plugin ownership documentation, and focused regression fixtures. Source snapshot: `cd8d64a9fcb79afe3d9fa9e27857210f719d8eda`, cloned read-only to `/tmp/ant-magpie-research-20261007`. The source manifest in [research evidence](../evidence/WORK/MAGPIE-RESEARCH/source.json) records reviewed file hashes and source links. The clone has no local changes.

This establishes what that implementation does, not whether every current provider plan, endpoint, authentication method or restriction is verified. No Magpie executable, plugin, test suite or installation script was run, and no real account was accessed. Tests below were read as executable examples; their success was not independently measured.

## Findings from Magpie

| Observed design | Implication for Ant |
|---|---|
| Accounts carry provider-specific signing, model discovery, request shaping and refusal handling. Models have protocol availability. [account.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/provider/account.go) | An account is an integration with behavior, not merely a name plus manually declared quantity. |
| `SubscriptionQuota` separates upstream quota reports from local token logs; windows preserve scope, reset, amount/unit where available, reading time, partial coverage and errors. [subscription_usage.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/provider/subscription_usage.go) | A percentage of a provider window is not a token balance. Store observations and their provenance separately from configurations and local consumption. |
| API-key coding plans have distinct quota readers for different endpoints and response shapes; some limits concern auxiliary tools rather than model calls. [planquota.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/provider/planquota.go) | Credential type alone does not define entitlement. Provider modules identify which constraints apply to which action. |
| Candidate ranking includes smart reset-aware use, explicit order, rotation, least-used and pacing. Failure state has an account/model scope and a reason for its return time. [routing.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/gateway/routing.go) | Eligibility and ranking need different contracts. “Prefer first” cannot make an ineligible account usable. |
| Fallback expands a provider into its accounts/keys and considers protocol/model restrictions; reply delivery constrains switching. [fallback.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/gateway/fallback.go) | Route candidates should represent concrete account + model + access path, not generic resource packages. |
| Conversation affinity preserves a previous account during a turn/session or while cache reuse is valuable, and survives restart. [affinity.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/gateway/affinity.go) | Model choice and account switching affect continuity and cost. Affinity is conditional on continuing eligibility. |
| Gateway account order and keeping an agent signed in to a particular account are separate controls. [keep_login_as.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/provider/keep_login_as.go) | Ant must distinguish request dispatch, provider account selection, and client configuration/sign-in changes. |
| Built-in or plugin ownership determines authentication, model discovery, requests, usage and upstream errors; the host retains orchestration. [provider-plugin ownership](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/docs/subsystems/provider-plugins.md) | A provider extension should own its complete semantics and lifecycle, not just encode an HTTP request. |
| Route traces retain values used in selection, candidate order, exclusions, affinity and attempts. [trace.go](https://github.com/yetone/magpie/blob/cd8d64a9fcb79afe3d9fa9e27857210f719d8eda/internal/gateway/trace.go) | Explain the actual decision from its recorded inputs, not from today's quota or settings. |

Important source examples: `TestAllowancePooled` ensures a model-specific exhausted allowance does not exhaust unrelated models; `TestRoutingKeepsModelAllowancesSeparate` checks scoped ranking; `TestAllowancesBeforeFirstReading` preserves last observations through restart; `TestRateLimitBacksOffWhenItComesBack` distinguishes remaining credit from temporary throttling; fallback/account tests cover account/key takeover; stream-cut tests distinguish partial delivery from failure before content. These are useful Ant acceptance-case inputs, not proof of live provider compatibility.

## What to adopt, and what requires Ant-specific design

Adopt provider-owned semantics, multiple simultaneous scoped windows, observed usage freshness, concrete account/model candidates, explicit routing policies, conditional affinity, classified cooldown and decision explanations. Start with explicit order plus eligibility; add reset/expiry-aware ranking only after its relevant windows and spend rules are trustworthy. Intent routing, agent configuration rewriting, nested groups and dynamic community plugin execution are separate optional capabilities, not prerequisites for resource configuration.

Ant should retain its controlled Transport, explicit extension registration, exact accounting, durable command identity and unknown-outcome handling. Magpie's float percentage rankings are not an exact money ledger. Its latest-reading heuristics do not establish a strict no-charge guarantee. Its before-output retry boundary cannot by itself prove that the upstream performed no billable work. For each candidate, Ant needs a separate answer for client output delivery, upstream execution outcome, charge exposure and retry safety.

The proposal must not infer full capacity from missing usage, reset a window merely because its timestamp elapsed unless the provider semantics warrant that inference, or infer shared billing identity from coincident reset times. Retain explicit unknown/stale/partial states and provider-backed sharing identity. Estimated list-price cost, actual balance, subscription usage and confirmed billing are separate quantities.

## Proposed architecture

| Layer | Owns | Does not delegate |
|---|---|---|
| Shared Go/SQLite core | Stable identity, namespace/permissions, durable config and observations, bounded scheduling, atomic reservations where meaningful, command/attempt history, versioned policies and decision records | Core validation, exact arithmetic, spending enforcement and unknown outcome guards |
| Service category module, initially AI | Request/result semantics, capability compatibility, metering vocabulary, client protocols, continuity and retry/idempotency classification | Whether unrelated actions are substitutable |
| Provider module within a category | Its configuration fields, supported authentication/access paths, plan interpretation, model/service discovery, quota scopes/time rules, observation collection, response/error interpretation, provider metering mappings | Upstream facts and provider-specific behavior |
| Routing policies | Preference among eligible candidates: manual order first, then reset/expiry-aware use, pacing, cost or latency as justified | Permission, capability, spend or safety gates |
| User management surface | Provider-native onboarding, subscriptions/accounts and observed state, route choices and reasons, reversible pause/reorder | Internal graph construction or arbitrary accounting fields |

A provider module may compose smaller capabilities; an API-key provider need not implement OAuth and a service without a quota API must report that limitation. Module descriptors and opaque provider settings need versioning and migration. The core accepts typed, validated projections for routing; provider-native facts remain available for explanation. Exact exported interfaces and a replacement persistence schema are not frozen by this research.

Proposed routing sequence: identify category/action → enumerate concrete candidates → evaluate access, capability, quota, time, spend and health → apply affinity/preferences to eligible candidates → record decision → reserve/dispatch under core controls → record outcome and usage → provider-specific refresh/cooldown → reuse those facts for the next decision. In the configure-first milestone, end at a read-only route preview: no dispatch or provider usage.

## First business slice and stage plan

1. **AI cases and provider contracts (next, before code).** Contrast a windowed/model-scoped subscription, an API-key coding plan, and a balance/pay-as-you-go account. Specify provider-native setup, observable quantities, time semantics, spend controls, unavailable states and permitted access paths. Magpie is a design reference; official provider evidence supplies actual rules. Select two implementation targets after comparing support and the user's intended accounts.
2. **Configure and inspect.** Deliver one provider-aware vertical slice with the existing real Go/API/SQLite stack: provider selection → appropriate account setup → plan/models/usage and source freshness → reversible pause/resume. Manual-only information is labelled as such; neither a static Example inventory nor a saved declaration proves discovery/usage. Preserve prior saved data while defining migration, and retire the rejected primary form only through the discussed replacement plan.
3. **Route preview.** Combine the two integrations. Given a requested AI capability and policy, show eligible candidates, selected account/model and reasons for exclusions. Verify different scoped windows, unknown data, reset/cooldown boundaries, no-incremental-spend policy and version conflicts. No execution yet.
4. **Consume and switch (subsequent user plan).** Add the AI gateway/protocol adapters, conditional conversation affinity, classified failure handling, exact usage attribution and operation/attempt outcome handling. Requests may fail over only under explicit outcome/retry rules; traces identify which account actually served each attempt.
5. **Extend to another AI platform, then another category.** A third provider tests that provider code/config can be added without modifying core routing/storage algorithms. Then a proposed email or storage slice tests category boundaries. No “all categories supported” claim before that evidence.

Email routing must consider sender/domain eligibility, recipient/action identity and whether a message may already have been accepted. Storage routing must consider object location, durability and data movement costs. These are Ant proposal examples, not researched provider guarantees. Shared scheduling, observations, spending policy and decision history can transfer; AI token semantics, prompt-cache affinity and AI stream conversion remain in the AI category.

## Business definition and acceptance for discussion

Target user: a developer/operator managing multiple owned AI service accounts, later analogous service accounts. Value: configure each naturally, understand its usable state, and let Ant choose suitable authorized supplies according to policy. Objects: category/action; provider integration; account/access path; plan/entitlement; scoped limit and observed usage; model/service capability; route/policy; logical operation/attempt; decision and metering record.

Loop status: existing Go/SQLite/auth/exact ledger extension seams implemented; generic inventory technically implemented, rejected as a business baseline, and retired 2026-10-08 (schema v5); provider-aware setup and observation lifecycle not established by that inventory; cross-provider route preview, unified AI gateway and provider-native switching not implemented as this corrected slice; real consumption remains subsequent work. No additional implementation was performed in this research.

Non-goals now: a universal manual quota wizard; frontend polish; automatically copying existing client credentials; arbitrary runtime plugins; automatic paid fallback; agent sign-in rewriting; live calls; a catch-all category schema. Billing: preserve each plan's own inclusion, overage, deduction and reset rules. User pause/resume reverses Ant's participation; it does not purchase, cancel or refund a subscription. Configuration and observed balance are separate.

Proposed Given/When/Then examples:

- Given two providers with different setup requirements, when each is configured, then each asks only its relevant account/plan/access fields and readback retains its native semantics.
- Given a model-scoped window is exhausted while an unrelated model has allowance, when evaluating that unrelated model, then that scoped window alone does not block the whole account.
- Given a valid credential but unreadable usage, when inspecting or previewing routing, then the state is unknown/stale with its source/time and policy treatment; it is never shown as zero usage.
- Given provider usage and local token records disagree in unit or coverage, when inspecting, then both remain separately labelled; neither fabricates the other.
- Given all eligible supplies would incur forbidden incremental spend, when previewing a route, then it is blocked with a reason and next action, not silently sent to pay-as-you-go.
- Given a paused account, when resuming, then its identity/history survive and current eligibility is re-evaluated.
- Given partial output or an uncertain upstream outcome, when considering a later consumption fallback, then category/provider retry rules and core outcome guards control the decision.
- Given a restart or stale config update, when reading history or writing configuration, then persisted observations/decision input versions survive and conflicts retain user input.

Value risk: current user rejection plus Magpie supplies concrete comparative evidence, but no independent Ant user acceptance. Usability risk: validate two provider-native setup examples before selecting interfaces/forms. Feasibility risk: inspect existing Adapter/CapacityReader/ProviderPolicy/Hook seams and extend only verified gaps. Commercial risk: separate subscription observations from exact billing and enforce explicit spend policy. Provider-specific public rules and authorized account testing remain to be verified when choosing actual integrations.
