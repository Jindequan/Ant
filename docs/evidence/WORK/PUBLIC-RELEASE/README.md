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

GitHub push protection detected a hardcoded Google installed-application OAuth client pair that the initial pattern list did not cover. The official Gemini CLI source identifies that pair as publicly distributed installed-app credentials, but Ant now resolves it from the installed CLI or an explicit complete environment pair. New tests cover installed-module discovery, missing-client refusal before token transmission and fresh-token behavior. The public branch history is sanitized without bypassing GitHub protection; a private local pre-sanitization Git bundle is retained outside the repository. Release binaries are rebuilt and re-notarized after this change.

Final rebuild after Gemini cleanup: quota package uncached race tests and vet passed; Xcode final upload and notarized export succeeded; final packaged app passed codesign/stapler/Gatekeeper both before and after DMG mounting. Both architectures still target macOS 13. Public history scan covered 1,507 blobs and included Google OAuth client ID/secret patterns: no matches. Public main pushed successfully without a protection bypass. The pre-publication private backup is retained under `.git/publication-backups/`.

Final asset checksums:
```text
90068c2c9e11faf00c071b9cb7e6281fc1348ea4ff4e34c691b8bff68a0b496d  Ant-0.1.0-universal.dmg
0a1a785502838b70c1327dc09ce4ad58aeaefe0957e7d1b93ca99db9202657f7  Ant-0.1.0-universal.zip
```

Publication acceptance: GitHub v0.1.0 is public (prerelease, not draft), with DMG, ZIP and SHA256SUMS assets. Native Sites deployment succeeded at https://ant-subscriptions.johnanderson254.chatgpt.site, public access. Anonymous HTTPS downloads of both packages passed SHA256SUMS. Clicking the production website download action saved the DMG to the browser Downloads folder; its checksum matches the published manifest. Production screenshot: published.png.
