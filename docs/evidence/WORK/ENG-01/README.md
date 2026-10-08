# ENG-01 — dependency gate restored (2026-10-08)

Ruling recorded in [STATUS.md](../../../plans/STATUS.md): Option B, narrowed and time-boxed;
verdict detail in [REVIEW-RESPONSE](../../../REVIEW-RESPONSE-2026-10-08.md) §1.

Changes:
- `internal/webui` holds only the embedded files (`Files()`); `api.PageHandler` serves them
  (`internal/api/page.go`, test `TestPageHandlerServesThePageAndNothingElse`). No exemption.
- `internal/quota/cookiestore` is the only SQL on the quota side: read-only, immutable opens of
  browser cookie databases. `internal/quota` no longer imports `database/sql` or `modernc.org/sqlite`.
- `scripts/check-deps.py`: `QUOTA_COOKIE_STORE` exempt for `database/sql` + `modernc.org`;
  `QUOTA_OBSERVATION` exempt for `net/http` **temporarily** (provider reads with the origin-pinned
  client, the 127.0.0.1 console-login callback, the loopback-only `/api/*`), ending when design 11
  phase 4 removes `internal/quota`.

Verification on the final tree: `make check` → `check-deps: dependency boundaries OK (31 packages)`;
`make test-contract`, `make test-integration`, `make test-fault` pass.

`make verify-manifest` reports drift against the T1-CODEX baseline (291 entries, pre-existing;
this batch adds `scripts/check-deps.py`). Not re-signed (R14/B6 review pending).
