# ENG-06 — quota credential handling audit (2026-10-08)

Scope: `server/internal/quota` as of 2026-10-08, after the ENG-01/ENG-06 fixes recorded in
[STATUS.md](../../../plans/STATUS.md). Read-only audit of the code plus the regression tests named
below. No real provider, credential or outbound call was used.

## Answers to the four questions

**1. What `~/.config/ant/accounts.json` persists.** Every added (or completed) connection, as
`Connection{id, provider, mode, way, label, origin, source, values, addedAt}`
(`quota/provider.go`). `values` holds the secret fields **in plaintext**: API keys, pasted cookies,
console tokens, AccessKey secrets, Ant's own sign-in session ids, and tokens renewed by
`keepRenewed` (`quota/service.go`). Protection is file mode only: directory 0700, file written via
temp file + chmod 0600 + rename (`quota/store.go`). Found connections are not persisted unless the
user edits them. **Finding:** plaintext at rest. Ant's engine already has encrypted `secret_records`;
moving these values there is design 11 V0.2 §6 (no plaintext fallback). Until then this is the
"stored like the apps Ant reads" posture and needs the user's explicit acceptance (open ruling R5 in
[REVIEW-RESPONSE](../../../REVIEW-RESPONSE-2026-10-08.md)).

**2. Do `/api/*` responses carry secrets.** No. `GET /api/accounts/{id}` returns `accountView`:
non-secret field values plus `secretsSet` (names only) (`quota/service_edit.go`, `view`).
`GET /api/quota` returns `Account` projections (plan, windows, balances, models, status, source
label) — the type has no credential field. The edit view's masking is asserted by
`TestAddEditAndRemoveAccountsOnThePage` (`quota/quota_test.go`).

**3. Logs / error strings.** The quota package does not log. `statusError.Error()` renders only
`"<code> <status text>"`; the provider body is kept for parsing provider error codes
(`aliyunError`, `volcError`), and page messages pass through `short()` (160 chars). Provider error
bodies are not known to echo credentials; no secret value is concatenated into any message.

**4. Credential confinement.** Before this batch: `DefaultLocal` used a plain `http.Client`, which
forwards custom headers on redirect to *any* host. Readers send keys in custom headers
(`xi-api-key`, `x-manus-api-key`), so a provider redirect to another host would have carried them.
**Fixed:** `readerClient()` (`quota/local.go`) refuses cross-host and HTTPS→HTTP redirects and plain
HTTP off loopback. Regression: `TestReaderClientDoesNotCarryKeysAcrossHosts`,
`TestReaderClientSameHostRedirectStillWorks`, `TestReaderClientRefusesPlainHTTPOffThisMachine`
(`quota/local_test.go`). Remaining by design: keys found in another app's config (ZCode, Claude Code
settings) are sent to the base URL that app is configured with; Copilot's model list goes to the API
endpoint GitHub's token response names. Both are the origin the credential's owner already uses.

## Findings beyond the four questions

| # | Finding | Disposition |
|---|---|---|
| A | `LocalOnly` checked only the `Host` header. With a non-loopback `-addr`, another machine could send `Host: localhost` and read `/api/quota`, or (no `Origin` header) add/remove accounts and start sign-ins. | **Fixed**: `LocalOnly` also requires a loopback peer (`quota/service_http.go`). Regression: `TestChangesAreGuarded` "foreign peer". |
| B | The review's "known-good: cookies remain encrypted as Chrome encrypts them" holds only for Ant's own sign-in profile. For the user's **everyday** browsers, Ant reads the `<Browser> Safe Storage` Keychain item and decrypts cookies in-process (`quota/browsercookies.go`), keeps the derived key in memory for the process lifetime, and does so on every refresh (startup + every 10 min) for every web-site provider, without per-site opt-in. | Not changed: behavior the user directed (QUOTA.md). Listed as open ruling R6 (opt-in per site). |
| C | Cross-browser recency was compared on two clocks: Chromium access times were converted to the 1970 epoch, Firefox and Safari to 1601, so Firefox/Safari always won regardless of recency. | **Fixed**: one 1601 clock (`quota/cookiestore`). Regression: `TestBrowserCookieRecencyComparesAcrossBrowsers`, verified to fail on the old arithmetic. |
| D | The Bailian console-login callback listens on a random `127.0.0.1` port for up to 15 min with `Access-Control-Allow-Origin: *`; acceptance requires the 128-bit random `state`. | Acceptable; noted. |
| E | `make run` performs real, credentialed provider reads at startup and every 10 minutes. This contradicts the standing guard in ENG-FINDINGS ("no outbound calls") but matches the user-directed QUOTA.md. | Open ruling R4. No behavior change. |
