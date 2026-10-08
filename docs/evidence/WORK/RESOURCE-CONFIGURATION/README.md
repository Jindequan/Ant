# Resource configuration verification

Date: 2026-10-07 (Asia/Singapore). Implementer/reviewer: Codex.
Scope: the user's configure-resources-first ruling and
[RESOURCE-CONFIGURATION](../../../implementation/RESOURCE-CONFIGURATION.md).
**Product acceptance withdrawn on 2026-10-07.** User rejection, verbatim:
“what a shit! do you know what should you build??” This evidence proves the
recorded implementation's technical behavior, not the suitability of its
account-as-subscription model or raw accounting-field setup. The corrected
business loop has not been implemented or verified. Real consumption, release,
provider admission and the existing T2/T5 phase gates remain unaccepted.
No Git repository is present in this workspace.

## Changes and purpose

The existing Resources route now creates and edits persisted ConfigDocument
drafts. Users can record subscription accounts for registered or future provider
IDs before adding credentials. Resource forms record stable identity, owner,
metric, unit/precision, limit/entitlement/concurrency role, exact integer capacity,
fixed/rolling/lifetime windows, renewal anchor, metering, currency and expiry.
The form never creates verified evidence, a provider fact or an available balance.
Unsupported and incomplete rules stay saved with server field issues.

`-enable-resource-config` composes schema registration, configuration commands and
read projections without a secret cipher, master key, execution adapter/policy,
transport, execution host or supervisor. The separate localtest execution mode
keeps its existing opt-in and key requirements. No dependencies or storage
migrations were added.

Draft saves retain their original version for CAS. Conflicts preserve local edits;
reload and navigation require a deliberate keep/save/discard choice. Read-only
sessions have no write forms. Draft-page data and the local last-draft pointer are
scoped to the signed-in identity and namespace; the pointer contains only an ID.
The saved URL also supports resumption independently of local storage. Linked
resource edits/removals are blocked until references are repaired. Invalid stored
shapes lead to document repair rather than crashing the resource form.

## Verification

- [Check](check.log): Go formatting/vet/build, dependency boundaries, TypeScript,
  generated contract guards and current stage-runbook generation passed.
- [Go targeted race tests](go-targeted.log): composition, configuration service and
  API packages passed. The schema-only registry test proves no adapter/policy or
  invented account facts are registered.
- [Contract race tests](contract.log), [SQLite integration race tests](integration.log)
  and [fault regression](fault.log) passed. The new SQLite configuration case
  persists an unsupported subscription resource with capacity `9007199254740993`,
  checks exact independent SQL readback, rejects validation/activation, and confirms
  no live resource projection is created.
- [Web](web.log): 260 tests passed, including six new configure-first cases:
  exact save/reload/edit/remove, invalid quantities/windows/date recovery,
  conflict preservation, deliberate save before navigation, linked-resource and
  malformed-draft guards, empty-installation entry and read-only controls.
- [Production build](build.log) passed. Vite retains its existing large-chunk warning.

The manual real-stack walkthrough used an isolated temporary SQLite database and
an agent-created disposable test identity. The server started without a master
key in configuration-only mode. Through the actual browser UI, a future-provider
account and monthly token quota were saved and reopened; the large exact integer
survived readback. Invalid input remained editable. The quota was then edited to
3,000,000 tokens and a separate 2,500 USD-cent entitlement declaration was added.
After a full server restart, both definitions and draft version 4 remained readable.
Independent [SQLite readback](sqlite-readback.json) confirms zero secret records,
live resource projections and operations. No provider requests were made.

[Desktop](desktop.jpg), [390px saved rules](mobile-390.jpg), and
[390px invalid-input form](mobile-form-390.jpg) were captured from the final build.
At 390px, both saved and expanded/error form views had document width 390px;
all fields/actions remain accessible and the table uses labelled vertical records.
The temporary viewport was reset after verification.

## Evidence binding

[source-manifest.json](source-manifest.json) records the final implementation
source snapshot (345 files). This is an increment snapshot; the original signed
T1 baseline was not replaced. Logs and screenshots show local synthetic inputs,
not provider-verified quotas or production acceptance.

## Limits and next stage

Consumption, live provider/account verification, adapter/model expansion and
usage settlement remain subsequent work. Declared resources use pending semantics
and no bound/initial-state evidence, so they cannot be promoted into executable
paths. Credential encryption remains necessary when credentials are added later.
The local resume control remembers one latest draft per identity/namespace;
additional saved drafts remain accessible by their URLs. Linked resource repairs
use the existing provider document workspace.

The optional [task-tree formatting check](task-tree-format.log) reported existing
card-renderer drift: workstream cards are English while `check_tree.py` renders
Chinese labels. This increment changed neither that renderer nor those cards or
task metadata, and did not overwrite the curated plan files. The application
checks above passed; no task-tree/phase acceptance is claimed.
