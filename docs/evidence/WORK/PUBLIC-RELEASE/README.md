# Ant 0.1.0 public release evidence

Release scope: configuration, provider-native observations and read-only route previews. No real-consumption or automatic-failover completion claim.

- Current tracked source (1,053 files before publication additions) and all existing history blobs (1,538) were scanned for GitHub/Sites/API credential patterns, AWS access IDs and private-key markers: no matches. Additional JWT/Bearer/long API-key scan of documentation/archive material: no matches. These are pattern checks, not a claim that arbitrary secrets can never exist.
- `go test -race -count=1 -p 1 ./...`: all packages passed, uncached. Initial unconstrained parallel run failed one supply seed command's 50ms budget under race instrumentation; the fresh serial package run passed. Production command budgets were not changed to hide that result.
- `go vet ./...`: passed.
- `git diff --check`: passed.
- App and bundled server include arm64 and x86_64 slices. `vtool -show-build` reports macOS 13.0 minimum on all four slices; Info.plist also declares 13.0.
- Developer ID Application signing uses hardened runtime and timestamp; nested server and bundle pass `codesign --verify --deep --strict`.
- Xcode's logged-in Developer account submitted the xcarchive with `developer-id` / `destination=upload`: EXPORT SUCCEEDED. `xcodebuild -exportNotarizedApp`: EXPORT SUCCEEDED.
- Exported app: `stapler validate` passed; `spctl --assess --type execute --verbose=2` returned `accepted`, `source=Notarized Developer ID`.
- Signed bundled server booted against a new isolated demo SQLite file; `/healthz` returned `ok`, `/readyz` returned `ready`.
- Signed native app launched with `ANT_DEMO=1` and loaded the synthetic projection; no real account payload used for website imagery. Intel and actual macOS 13 runtime were not available on this Apple Silicon host; cross-compilation and load-command validation cover those targets.
- Landing page at 1440×900 and 390×844: one H1; no horizontal overflow; main download action fits mobile viewport. Screenshots accompany this record. Installation anchor and download-recovery disclosure were exercised.
- Website illustration is explicitly labeled illustrative. Its social image is a screenshot of the same public page, not a claimed live account screenshot.

Published application is the notarized Xcode export, not the original pre-export build. SHA256SUMS is generated after packaging. The DMG container is Developer ID signed; the application inside carries the Apple notarization ticket.
