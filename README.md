# Ant

A local macOS menu bar app for AI accounts, subscriptions and quota observations. Each provider keeps its own balance, quota windows, model scope and reset semantics.

[Download Ant for macOS](https://ant-subscriptions.johnanderson254.chatgpt.site/) · [Releases](https://github.com/Jindequan/Ant/releases) · [Report a problem](https://github.com/Jindequan/Ant/issues)

## Current preview

Ant 0.1.0 configures and observes resources and provides read-only model route previews. The supply engine has native modules for Z.ai, DeepSeek, Kimi and Alibaba Bailian, alongside additional observational adapters. Capabilities differ by provider.

- Keep accounts, credential identities, subscriptions, resources and quota readings connected.
- Preserve individual provider windows, balances, reset times and observation freshness.
- Inspect eligible route candidates and exclusion reasons; pause/resume accounts and save per-model preferences.
- Run a Go service bundled with a SwiftUI menu bar app, using local SQLite persistence.

**Real request consumption, automatic failover and usage attribution are still planned.** A route preview does not send a billable inference request. The app is free to download; provider subscriptions and API charges are separate.

## Install

Download the universal DMG from [v0.1.0](https://github.com/Jindequan/Ant/releases/tag/v0.1.0), open it, drag Ant into Applications and launch it. Requires macOS 13 or later; includes Apple Silicon and Intel builds. A ZIP and SHA256SUMS are also available. The application is Developer ID signed, notarized by Apple and includes a stapled ticket. The published app passed macOS Gatekeeper assessment.

The app can discover supported local sign-ins and make read-only usage/balance requests. Use its account management to connect supported credentials. Configuration and database files live in `~/.config/ant`; a cached panel reading lives under `~/Library/Caches/local.ant.app`. No account database is included in the download. The website has no credential form or analytics.

Quit Ant and remove the application to uninstall. Your local data remains until you deliberately remove it. Never attach credentials, cookies, tokens or unredacted account exports to an issue.

## Build from source

Install Go matching [server/go.mod](server/go.mod) and Apple's Xcode command line tools, then run:

```sh
sh scripts/build-app.sh
open bin/Ant.app
```

The local build is ad-hoc signed. For a universal build:

```sh
ANT_UNIVERSAL=1 ANT_VERSION=0.1.0 ANT_BUILD=1 sh scripts/build-app.sh
```

For Developer ID distribution, also set `ANT_SIGN_IDENTITY` to your installed certificate identity. Apple notarization is a separate release step; a signed local build is not automatically notarized.

Run the core checks:

```sh
cd server
go test -race -count=1 -p 1 ./...
go vet ./...
```

For a fixture-only browser preview, use an isolated new database:

```sh
cd server
go run ./cmd/ant-server -demo -db /tmp/ant-demo.db -addr 127.0.0.1:17890
```

`-demo` shows synthetic accounts and does not read this machine's provider sign-ins.

## Architecture and direction

The Go core separates provider-native observations from normalized account/resource records, quota readings, offer eligibility and route ordering. SQLite supplies serialized writes, append-only observations and persistent preferences. The macOS panel and local web UI read the service's projections.

[Design index](docs/design/README.md) · [Current status](docs/plans/STATUS.md) · [Router boundaries](docs/design/16-ROUTER-OWNS-THE-CHOICE.md) · [Route preferences](docs/design/17-SUPPLY-ROUTE-ORDER.md)

The long-term goal is extensible subscription and resource management across AI providers and other service categories. This release exposes the current configuration and observation work so it can be tried and reviewed.
