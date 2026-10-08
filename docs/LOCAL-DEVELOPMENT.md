# Local development

## Run Ant

```sh
make run    # build, then serve http://127.0.0.1:8080 reading your own accounts
make demo   # the same page with sample data; reads nothing
```

The page is embedded in the server binary (`server/internal/webui`), with no
Node or build step. What each provider reads and where it finds the sign-in is
in [QUOTA.md](QUOTA.md).

The React console that used to live in `web/` was retired on 2026-10-07 and
moved to `.archive/2026-10-07-old-frontend/`. The sections below describe its
configuration workflows. Their server APIs still exist, but `-web-dist` no
longer exists.

## Configure subscriptions and resources first

Build with `make build`, then bootstrap an administrator once:

```sh
./bin/ant-server -db ./server/data/ant.db -bootstrap-admin -bootstrap-namespace local
```

Start the configuration-only workspace:

```sh
./bin/ant-server -addr 127.0.0.1:8080 -db ./server/data/ant.db \
  -enable-resource-config
```

This mode needs SQLite and the administrator token, with no credential master
key, provider API key, paid infrastructure, execution host or provider transport.
Open the app, sign in, choose **Resources → Add a plan or package**.
Choose the provider and name its account, describe the subscription/credit/free
allowance/pay-as-you-go package, then enter its resources and limits. Save once;
the server persists the whole configuration and the UI reads it back. No internal
account/resource IDs, precision fields or evidence references are required.

A package can contain several resources with independent reset windows.
Account-wide constraints apply automatically to every package on that account
and are referenced, not copied. Resource quantities and prices use exact decimal
strings. Package renewal/expiry and optional cost are displayed in the inventory.
Existing packages can be edited, archived and restored. Removing a package-owned
resource retires its identity; it can be restored from the edit form. Archives
remove proposed pool memberships without cancelling the provider subscription.

Choose **Pools** to group compatible packages by intended use and save their
priority order. These are configured supply pools, not executable pools. Saved
inventory persists in SQLite across server restarts. Version conflicts retain
input and require reviewing the latest saved version before retrying.

Configured quantities are declarations, not remaining balances or verified
provider facts. Provider choices identify accounts; they do not claim verified
adapters or current plan terms. Live-provider verification and admission into
executable configuration are the next phase. The inventory does not silently
convert or activate the previous technical drafts; they remain accessible through
the advanced execution workspace. SQLite migration v4 adds the inventory table
without changing existing ledger/config records.

## Local synthetic execution workspace

The localtest provider catalogue and account scopes are fixture data. With
`-enable-localtest-config`, the configuration workspace and operation path are
available end to end through a loopback-only synthetic Chat Completions
endpoint. The same provider directory also registers the DeepSeek descriptor
and adapter; its only allowed destination is the exact DeepSeek Chat
Completions URL. The local catalogue has no verified DeepSeek account scopes,
so it cannot produce an executable DeepSeek candidate. Localtest replies and
usage are synthetic; the fixture records a synthetic metered amount so the
reservation and settlement path can be exercised without contacting a real
provider.

Create a private key file once, then keep it with the local installation and
back it up separately from the database:

```sh
mkdir -p server/data
chmod 700 server/data
cd server
go run ./cmd/ant-server -generate-secret-master-key -secret-master-key-file ./data/secret-master.key
```

Bootstrap a local administrator token once:

```sh
go run ./cmd/ant-server -db ./data/ant.db -bootstrap-admin -bootstrap-namespace local
```

Build the server and web app from the repository root:

```sh
make build
```

Run the app with the local configuration workspace, encrypted credential
storage, and the built web UI served by the same origin:

```sh
./bin/ant-server -addr 127.0.0.1:8080 -db ./server/data/ant.db \
  -enable-localtest-config \
  -secret-master-key-file ./server/data/secret-master.key
```

Open `http://127.0.0.1:8080` and sign in with the one-time token printed by
the bootstrap command. The web directory can also be served separately; in
that setup configure the Vite API proxy with `ANT_API_PROXY_TARGET`.

In **Providers → Advanced execution configuration**, create a localtest account and binding with the endpoint
`http://127.0.0.1:8080/v1/localtest/chat/completions`, store any test-only
credential, create the token, synthetic-cent, and concurrency resource limits,
and activate the configuration. Then create or enable a pool containing the
`ai/text` / `profile.chat.basic` offer. In **Operations**, enter a prompt,
choose the output-token ceiling, and explicitly enable metered resources to
submit it. The displayed reply is always `Local test reply`; the usage values
are fixture data and do not represent provider billing. The loopback endpoint
uses the port from `-addr`, so change the binding endpoint if you use a
different port. Non-loopback callers are rejected by the endpoint.

The master key must be exactly 32 raw bytes in a regular file with mode 0600.
The server refuses to start the local configuration workspace without this
key; it never generates or stores an ephemeral replacement during startup.

Create a verified SQLite snapshot into a new file inside a private directory:

```sh
./bin/ant-server -db ./server/data/ant.db -backup-to ./server/data/ant-$(date +%Y%m%d-%H%M%S).db
```

The backup directory must have no group or world permissions (`chmod 700
server/data`). The command serializes the snapshot with writes, checks SQLite
integrity, and refuses to replace an existing destination. Backups contain
encrypted secret values; retain the matching `secret-master.key` separately
and restrict access to both files. Restoring an older database can omit
operations recorded after that snapshot. Use the guarded procedure in
[RECOVERY.md](operations/RECOVERY.md); the supported restore command closes
dispatch in the restored copy and preserves the replaced database for review.
