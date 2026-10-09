# The supply foundation the router stands on

Version: V1.0, 2026-10-09. The router will choose, for each request, an account and endpoint
from what Ant manages. It reads the **supply store** (credentials, accounts, packages, meters,
readings, offers) through the engine — never the page. These are the store's promises; each is
held by a test, and a change that breaks one fails the gates.

## Promises

| | Promise | Held by |
|---|---|---|
| ✅ | **Only what exists.** A credential no longer found (removed, hidden, gone from this Mac) is `hidden` with no offers; found again, it's back | `TestGoneCredentialsLeaveTheStore` |
| ✅ | **Removed stays removed.** What the user removes never comes back from an old record | `TestMigratedAccountsCanBeRemoved`, `TestRemovedAccountLeavesThePageAtOnce` |
| ✅ | **Many accounts per provider.** Keys, sign-ins (own browser profile each) and app sign-ins add accounts; signing in again renews one | `TestOneAlibabaSignInConnectsEachInstance`, `TestEachSignInHasItsOwnProfile`, `TestChatGPTAnotherAccountInItsOwnCodexFolder` |
| ✅ | **One account, one identity.** Accounts merge only on the identity a provider reports; inferred groupings are display-only | design 11 §2.6; `TestNativeModulesReadZaiAndDeepSeek`, `TestGrantsGroupRecognisesAnAccount` |
| ✅ | **Exact numbers, kept over time.** Amounts parsed exactly; readings append-only, on change | `TestParseDecimalIsExact`, `TestBalanceByRegion`, `TestPaygInventory` |
| ✅ | **Routable means usable.** Paused, signed out, couldn't read, used up, too low to call: never a route; each says why | `TestRoutePreview*` |
| ✅ | **A model's own meters.** A call draws on its model's grant (`scope:model`) and never another model's | `TestRoutePreviewModelScopedMeters`, `TestRoutePreviewFreeGrants` |
| ✅ | **Keys stay sealed, failures stay small.** One key that won't open marks its account only | `TestOneUnreadableKeyMarksOnlyItsAccount`, `TestMigratingSealedAccountsKeepsEveryKeyOpen` |
| ✅ | **Sign-ins last.** Session ends are known and warned; Ant's own sign-ins are kept alive | `TestSessionEndsWhenItsCookieExpires`, `TestKeepAliveVisitsEachSignInOnceADay` |

## Before the router

| | Gap | Why it matters | Plan |
|---|---|---|---|
| 🔴 | **Offers for 4 of 27 providers** (Z.ai, DeepSeek, Kimi, Bailian). The other 23 are read through the quota adapter: cards, timeline and history, but no endpoint and no offers | The router can only route what has offers | Move providers to modules (design 12's contract), the ones with an API endpoint first: OpenRouter, SiliconFlow, StepFun, MiniMax, Volcengine, BigModel, Xiaomi; then app sign-ins (ChatGPT, Claude, Gemini, Copilot), whose calls go through their own tools |
| 🟠 | **Prices per token** are known for DeepSeek only | The router compares pay-as-you-go costs | A price table per module, from each provider's published prices |
| 🟡 | Safari session ends aren't read | Warnings for Safari sign-ins | Read `Cookies.binarycookies` expiry |
| 🟡 | Execution (calls, failover) | The router itself | Its own design, on these promises |
