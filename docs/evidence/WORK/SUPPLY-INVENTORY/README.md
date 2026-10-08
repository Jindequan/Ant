# Supply inventory correction — local verification, 2026-10-07

**Later user ruling: the product direction is rejected.** The local checks below remain technical evidence for their snapshot, not business acceptance. Current work is [Magpie research and discussion](../../../research/MAGPIE-AND-ANT.md); provider-native behavior must replace the generic wizard baseline.

## 1. Change and acceptance boundary

User ruling: configure resources first; consuming them is next. The previous raw
account/resource draft editor was rejected. Latest rejection, verbatim:
“I don't know the result you have implement, what logic it is! rediculous form
and stupid config progressing”. [Scope, seven business outputs and page contracts](../../../implementation/SUPPLY-INVENTORY.md)
were written before implementation. This correction is **implemented and locally
verified; user acceptance pending**. Historical rejection and signed T1 evidence
are not overwritten or re-signed.

The primary loop is account → package → resources → one save/readback → pool
membership and priority. Packages distinguish subscription, credit, free and
pay-as-you-go. Accounts own mandatory shared limits, referenced once by every
package. Resources retain exact decimals, independent calendar/rolling/lifetime
windows and immutable identities; removed package resources are retired and
restorable. Packages support archive/restore. Pool compatibility is declared
intended use, not proof of executable provider compatibility. Go/SQLite migration
v4 is additive; ConfigDocument v1, old drafts and the execution ledger remain
separate. No dependencies or paid infrastructure were added.

## 2. Verification and real-stack walkthrough

All final commands returned exit 0:

| Command | Evidence |
|---|---|
| `make check` (format/vet/build/dependency/type/generated guard + runbooks) | [check.log](check.log) |
| `make test-contract` (all Go internal packages, race, fresh run) | [contract.log](contract.log) |
| `make test-integration` (real SQLite, race, fresh run) | [integration.log](integration.log) |
| `make test-fault` | [fault.log](fault.log) |
| `go test -race -count=1 ./cmd/ant-server` | [composition.log](composition.log) |
| `make test-web` — 265 tests / 9 files | [web.log](web.log) |
| `make build` — production server and Web | [build.log](build.log) |

Meaningful new tests cover exact decimal preservation, invalid graphs/windows,
mandatory shared references, identity guards, retirement/restoration, CAS races,
namespace/principal-scoped idempotency, restart, audit/receipt atomic rollback,
HTTP strict JSON/permissions/preconditions, retained conflicts, native date input,
main package/pool loop and read-only UI. Migration tests upgrade v3 to v4 while
preserving old config, check byte-identical embedded migration, and still reject
future schema v5. The initial workflow accessibility regression log is retained
as [historical failure](initial-workflow-failure.log); final Web evidence above
passes after quantity inputs received explicit accessible labels and described
inline guidance. The real WebKit walkthrough found date input appearing entered
without persisting; onInput/onChange handling now saves and reads back the date.

Constitution §3.8: each successful save is followed by GET. Stale CAS preserves
local input and requires deliberate latest-version review; immutable identities
cannot be repurposed. API permission checks remain server-side. §4.4: the actual
production Go binary and Web dist served the existing disposable SQLite database
on 127.0.0.1:18087. The browser created one Example account, a subscription and a
separately expiring credit package, added the shared 120 requests/minute limit,
and saved/reopened a pool in credit-first order. Archive removed membership;
restore kept package/resource identity, then explicit membership/order save
rejoined it. Server restart preserved final inventory version 8 and date rules.
Read-only identity saw no mutation controls; actual HTTP PUT returned 403.
Another namespace read an empty version 0. See [HTTP roles](http-roles.json) and
[final SQLite readback](sqlite-readback.json).

Exact quantity 9007199254740993 survived actual UI → API → SQLite → readback
before the demo allowance was changed to 3000000; [early exact readback](exact-readback.json)
records it. Final data has token allowance 3000000/month, price 12 USD/month,
renewal 2026-11-07, credit 25.01 USD with expiry 2027-01-01, and one shared rate
resource. These are **fictional Example values**, not verified provider terms.
Live resource, executable pool, operation and secret tables each remain empty.
The original draft definition SHA/version matches the pre-migration backup.
The original browser tab still retains its unsaved deepseek-standard input and
leave blocker; it was not reloaded or discarded. The corrected preview is a
separate tab. Read-only test browser was logged out and closed.

Constitution §4.3: established tokens and zh/en remain synchronized. Real
screenshots were inspected; final desktop widths are 1280 and mobile widths are
390, with no horizontal page overflow. Temporary viewport overrides were reset.

| Page | Desktop | 390px |
|---|---|---|
| Resources | [1280×1030](resources-desktop.jpg) | [390×1472](resources-390.jpg) |
| Providers/accounts | [1280×900](providers-desktop.jpg) | [390×844](providers-390.jpg) |
| Pools | [1280×900](pools-desktop.jpg) | [390×930](pools-390.jpg) |
| Resource editor | — | [390×1807](mobile-form-390.jpg) |
| Read-only resources | [1280×1066](readonly-desktop.jpg) | Covered by UI tests |

## 3. Source binding

[source-manifest.json](source-manifest.json) binds 361 implementation/build/test
inputs using the existing snapshot coverage (server, Web, contracts, scripts,
root build files and consumed regression inputs; caches/build outputs excluded).
Manifest SHA-256: `b54e7db6bbd620b583f8d554794f810917cf0e8399f726b9ccb450f16fdaa608`.
This is a new local verification snapshot, not independent acceptance or a
replacement for the old signed manifest. The workspace has no Git repository,
so no commit or PR is claimed. The production server and build correspond to
this implementation snapshot; subsequent edits were documentation only.

## 4. Remaining work and limits

Consumption is subsequent work. Inventory currently collects user declarations;
a trusted admission compiler, verified provider presets/terms, credentials,
usage mappings and live balances must connect those declarations to executable
configuration. Saving a proposed pool does not authorize or perform a call.
No provider request, real secret, charge, cancellation or refund was made.
The production build retains its existing large-chunk warning. Performance,
independent user onboarding timing and user acceptance were not established.
Old task-tree format validation is not claimed. Broader gate status remains
as recorded in STATUS; this scoped correction does not exit G1 or accept T5.
