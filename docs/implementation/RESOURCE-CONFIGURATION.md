# Configure resources before consumption

Latest user ruling, 2026-10-07: “we can configure resources first. then use them is the next plan.” Subsequent rejection, verbatim: “what a shit! do you know what should you build??” **The delivered editor is rejected; configuration-first is not complete.** The previous implementation mistakes a provider account for a subscription and makes users construct accounting definitions through internal fields. Passing persistence tests does not satisfy the product requirement.

## Corrective business boundary — not yet implemented

Ant manages resource supply across providers and makes that supply usable through pools and server-side allocation. A subscription is one supply source, alongside credit packages, free allowances and pay-as-you-go arrangements. Provider accounts own the real shared billing/quota boundaries; credentials are access paths, not independent copies of those resources.

The target administrator should be able to choose a provider, name an account, attach a plan/package, configure its human-readable allowances and commercial rules, and save an understandable inventory. The same account may own several packages. Each package may supply several resources. Account-wide limits can constrain all of them. Resources can then be assigned to suitable pools with a manual order of use. Consumption follows in the next phase.

The core needs distinct provider/account, plan/package, resource/limit, metering-rule and pool concepts. Provider presets translate business inputs into normalized definitions and record the rule version and source. Calculators must support different units and windows without provider-specific branches scattered through the ledger. No subscription count, local declaration or credential count may manufacture a provider-verified balance or duplicate a physical quota bucket.

Configuration acceptance requires an empty-installation walkthrough that saves an account with a recurring allowance and a separate expiring credit package, reopens them from SQLite, and shows their scopes, renewal/expiry, cost and pool priority in ordinary terms. Internal IDs are generated. Exact quantities survive readback. Missing credentials and verification are separate next-step states, rather than errors that make an otherwise valid inventory look unsaved. Invalid inputs and concurrent saves preserve the user's work. Real provider plan values are supplied by the user or verified from official sources; examples must be labeled as examples.

At the time of this rejection, the correction had not changed the schema or implemented a package aggregate. The subsequent [supply inventory correction](SUPPLY-INVENTORY.md) now implements that aggregate with additive SQLite v4 persistence and has [local verification evidence](../evidence/WORK/SUPPLY-INVENTORY/README.md). Existing drafts must be preserved. Go/SQLite and the four established routes remain the baseline. Execution admission, exact accounting, namespace isolation, permissions and the distinction between declared and verified quantities remain mandatory.

## Historical rejected increment

The previous increment implemented durable account and declared-resource drafts without credentials, network observations, or execution. It extended the existing Providers → Resources draft flow and preserved the four primary routes and existing visual language.

The existing ConfigDocument is the storage contract. Accounts can name future providers; resource definitions retain exact integer capacities, precision, commercial role, window, metering and expiry. Unsupported provider/semantics and missing evidence remain server validation issues. A declared quantity is never available balance or verified evidence. No schema migration or execution-path relaxation is needed.

Historical technical checks (not product acceptance):

- Given a new installation, when an administrator creates an account and resource draft, then SQLite stores it without a credential or provider request.
- Given a saved draft URL, when it is reopened, then the same account, resource identities, capacities and window rules are editable.
- Given unsupported provider rules or missing evidence, when a draft is saved, then it remains saved and pending verification; activation is rejected.
- Given invalid quantities, windows or expiry, when the form is submitted, then a recoverable error keeps its fields and the document unchanged.
- Given a resource referenced by a binding, offer or entitlement group, when removal is attempted, then the form explains the references and preserves them.
- Given a conflicting save, then local edits survive and the user can explicitly reload the stored version; read-only sessions have no write controls.

Historical resource page behavior: show declared resource rules and draft version; add/edit/remove unreferenced resources and save; accounts are prerequisites but may be created in the same draft; loading, empty, error, success and read-only states; return to Providers for advanced validation/activation. This is not an accepted business loop. Consumption remains a subsequent increment.
