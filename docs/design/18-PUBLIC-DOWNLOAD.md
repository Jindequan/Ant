# Public download — Ant 0.1.0

## Business contract

Ant is a local macOS app for people with multiple AI accounts. The visitor's job is to understand the current preview and download it. The actor is a public visitor; the object is a versioned, signed universal macOS package. Entry is the public landing page or GitHub repository. Success is a downloaded package with installation instructions; recovery is the alternate ZIP / release page and GitHub issues. Uninstalling means quitting Ant and removing the app; local data remains unless the owner removes it. There is no website account, checkout, email collection, analytics, or cloud credential upload.

Current boundary: configure and observe accounts, provider-specific quota windows and balances, pause/resume, and inspect eligible model routes. Real consumption, automatic switching and usage attribution remain future work. No implication of free provider service or bundled subscription.

## Page contract

1. Route: `/`; one primary download anchor, with installation at `#install`.
2. Entry: repository, shared link, search.
3. Actor: anonymous visitor; no login required.
4. Main object: Ant v0.1.0 universal DMG, macOS 13+.
5. Primary action: download the GitHub release DMG.
6. Supporting actions: ZIP fallback, release notes, source and issue reporting.
7. Content: provider-native semantics, local storage, route preview boundary, installation.
8. States: ready with fixed release links; unavailable download recovers through release page; no JS-dependent loading or empty state; disclosure controls work without JS.
9. Reverse flow: return from releases; quit/remove app without deleting personal data.
10. Trust: early preview, actual signing/notarization status, no invented customer claims or statistics, no real account screenshot.

## Visual decision

Use the user's approved walking-ant mark and the existing Ant panel's teal / graphite palette. System sans-serif typography, strong oversized headline and a compact product-inspired quota illustration explicitly marked illustrative. A wide open hero becomes a single column at 390px. Dividers separate provider semantics and installation; avoid a generic grid of feature cards. Tokens: paper #f7faf9, ink #182c2a, muted #52645f, accent #146957, line #d5dfdb; 8px spacing base, 16px body, 1.6 line-height, bounded 68-character copy.

## Acceptance

- Given an anonymous desktop or 390px mobile visitor, the page shows one main heading, version/system requirements and a keyboard reachable download action without horizontal overflow.
- Given a DMG download, the URL resolves anonymously to the published universal package and its checksum matches the release manifest.
- Given an installation question, the page explains drag to Applications, first launch, local provider reads and where to report an issue.
- Given an unavailable direct asset, the visitor can reach release downloads and ZIP without a script.
- Given a signed release, both Mach-O slices target macOS 13 and nested code verifies; notarization claims require Apple's actual acceptance and a stapled ticket.
- Given source publication, current files and history are scanned for credential patterns before pushing; release bundles contain no account database.

Validation evidence lives in `docs/evidence/WORK/PUBLIC-RELEASE/`. This release does not close the real-consumption milestone.
