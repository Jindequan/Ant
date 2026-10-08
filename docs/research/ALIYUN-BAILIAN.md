# Alibaba Cloud Bailian: how Ant reads it

Observed on 2026-10-07 in the Bailian console (cn-beijing), with the account
owner signed in. These are the console's own internal requests, not a public
API, and they can change without notice. When they do, the Aliyun card says
the reply changed, and this file says where to look again.

## Session

1. Cookies: every cookie the browser holds for `*.aliyun.com`. The login
   ticket is HttpOnly. `login_aliyunid_pk` is a visible marker that a login
   exists.
2. `GET https://bailian.console.aliyun.com/cn-beijing/subscription/token-plan/personal`
   with those cookies. The server-rendered HTML contains
   `ALIYUN_CONSOLE_CONFIG = { … SEC_TOKEN: "…" … }`. If it's missing, or the
   request ended up on `signin.aliyun.com`, the login has expired.
3. Every call is `POST`, form-encoded, carrying `sec_token`, `region=cn-beijing`
   and `params` (a JSON string).

## Calls Ant makes (all read-only)

**Gateway calls:**
`POST https://bailian-cs.console.aliyun.com/data/api.json?action=BroadScopeAspnGateway&product=sfm_bailian&api=<API>&_v=`.
Here `params = {"Api": "<API>", "V": "1.0", "Data": {…, "cornerstoneParam": {}}}`.
The reply is `data.DataV2.data` = `{success, data}`.

| API | Data | Reply `data` |
|---|---|---|
| `zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription` | `{}` | `instanceCode, specCode ("standard"), remainingDays, startTime, endTime (ms), autoRenewFlag, status ("VALID")` |
| `zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage` | `{}` | `per1MonthPercentage` (a fraction: 1.0 = 100% used), `per1MonthResetTime` (ms). Other tiers may add `per<N><Unit>…` windows |
| `zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/addon/summary` | `{}` | `remainingCredits, totalCredits, activeCount` |
| `zeldaEasy.bailian-commerce.tokenPlan.queryTokenPlanEquityInfo` | `{"queryTokenPlanEquityInfoRequest": {"requestId": "<uuid>"}}` | `tokenPlanEquityInfos[]`: `equityType, availableQuota, totalQuota, instanceStartTime, instanceEndTime` |

**Pay-as-you-go spending:** the official BSS `QueryBillOverview` with
`params = {"BillingCycle": "YYYY-MM"}` (Beijing time) goes through the same
proxy as below. Ant reads `Data.Items.Item[]` where `ProductCode` is `sfm`
and `SubscriptionType` is `PayAsYouGo`: `PretaxAmount` is what was charged,
`PretaxGrossAmount` what the usage was worth before free quota and
discounts.

**Console OpenAPI proxy:**
`POST https://bailian.console.aliyun.com/data/api.json?action=GetFundAccountAvailableAmount&product=BssOpenAPI-V3&_tag=`
with `params = {}`. The reply `data` holds `AvailableAmount`, `Currency`, and
so on.

**Harness tool benefits.** The units come from the console's labels:

| equityType | Tool | Unit |
|---|---|---|
| `tokenplan_harnesstool_image_generation` | Image generation MCP | images (张) |
| `tokenplan_mcp_codeinterpreter` | Code interpreter MCP | calls (次) |
| `tokenplan_harnesstool_search_pro` | Web search pro MCP | calls |
| `tokenplan_harnesstool_vertical_search` | Vertical search MCP | calls |
| `tokenplan_harnesstool_webfetch` | Web page parsing MCP | calls |
| `tokenplan_harnesstool_tts` | Speech synthesis MCP | characters (字符) |
| `tokenplan_harnesstool_asr` | Speech recognition MCP | seconds (秒) |

Video generation, RAG, Memory, Managed Agent and Sandbox show "88折" (12% off),
not an allowance.

## What Ant must not call

The console marks 26 APIs for Alibaba's anti-bot layer (Baxia: `uab`, `umid`,
`et`), listed in `bailian-site-fe/…/assets/fetcher.js`. They include every
free-quota API: `bailian-commerce.freeTrial.queryFreeTierQuota`,
`…queryFreeTierQuotaAsyn`, `…queryFreeTierOnlyStatus`, and the
activate/deactivate switches. **Ant doesn't call them**, because that would
mean defeating bot detection. The model marketplace's `listFoundationModels`
(not protected) returns `quota: {}`, so it doesn't help either. **New-user
free quota therefore isn't read.** The card links to the console page.

Before adding a call, check it isn't in that list.

## The CLI's way (bailian-cli 2.1.0, read from its published source on 2026-10-08)

- **Sign-in:** `bl auth login --console`.
  1. Listen on `127.0.0.1:<port>`.
  2. Open `https://bailian.console.aliyun.com/console-login?notice=127.0.0.1:<port>?state=<hex>`.
  3. The page calls `http://127.0.0.1:<port>/?state=<hex>`, carrying
     `access_token` (query, JSON, `data.access_token` or form) and optionally
     `console_site`, `console_region` and `workspace_id`.
  4. Answer CORS preflight with `Access-Control-Allow-Origin: *`.
- **Calls:** `POST https://bailian-cs.console.aliyun.com/cli/api.json?action=BroadScopeAspnGateway&product=sfm_bailian&api=<API>`
  with `Authorization: Bearer <token>` and a form of `params` (the console's
  `{Api, V, Data}`) and `region`. Its `Data.cornerstoneParam` is
  `{protocol: "V2", console: "ONE_CONSOLE", productCode: "p_efm", switchUserType: 3, consoleSite: "BAILIAN_ALIYUN"}`.
  An expired token answers `errorCode` containing `NotLogined`.
- **Free quota (`bl usage freetier`):**
  1. List every model with `listFoundationModels`: `{input: {pageNo, pageSize: 50, name: "", providers: [], inferenceProviders: [], features: [], group: true, capabilities: [], contextWindows: []}}`.
  2. Send all the names to `queryFreeTierQuota`: `{queryFreeTierQuotaRequest: {models}}`.
  3. Read `freeTierQuotas[]`: `model`, `quotaTotal` (left), `quotaInitTotal` (granted), `quotaValidityPeriod` (due, ms), `quotaStatus` (`VALID`).
- **Token Plan (`bl usage token-plan`):** the same `tokenplan/personal/api/v2/usage`. It reports `per5HourPercentage`, `per1WeekPercentage` and `per1MonthPercentage`, with their `…ResetTime`.
- **Token refresh:** the official OpenAPI `GenerateCLIAccessToken` (`/modelstudio/cli/generateAccessToken`, version `2026-02-10`, on `bailian.cn-beijing.aliyuncs.com`), signed with an AccessKey. Ant doesn't use it yet; an expired token asks you to sign in again.
- **Not in the CLI:** balance and bills.
