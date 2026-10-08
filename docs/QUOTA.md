# Quota: what each service has left

Ant shows every subscription, API balance and free allowance you have, each
read by its provider's own rule. It never spends anything: every request it
makes is a read of the account's own usage or balance.

```sh
make run     # reads your accounts → http://127.0.0.1:8080
make demo    # sample data, reads nothing, additions kept in memory
```

## The model

**Provider → modes → accounts.** A provider is a service (Z.ai, Alibaba
Cloud Bailian…). A **mode** is one way of paying it, and each mode owns its
whole rule:

- **Rule:** how its allowance works (windows, expiring grants, a balance).
- **Setup fields:** what adding it by hand asks for. ChatGPT asks for none,
  because only Codex can sign in.
- **Discovery:** where Ant finds it on this machine.
- **Reader:** how its allowance or balance is read. A mode with no provider
  API is shown from its rule and your setup, and the card says so.

**Ways.** A mode can often be read several ways. Its **ways** are listed
easiest first:
1. what another app on this Mac already holds (an agent's or CLI's sign-in);
2. a key or token you give;
3. a sign-in Ant runs;
4. a pasted cookie, as the last resort.

Every way that finds something is read. When one fails and another works
for the same account, the card shows the working one. **Add account** lists
a mode's ways, and one is enough. A card says which way read it.
Another tool's refresh token is never spent: renewing it could sign that
tool out. Ant reads its current keys, and asks you to renew them in the tool
when they lapse.

An **account** is one sign-in of one mode. It is **found** in another app's
configuration, or **added** on the page with **Add account**. The same key
found and added is one account. Editing a found account (for example, giving
a found Token Plan its tier) keeps it as yours under the same ID. Removing a
found account hides it.

Code: `server/internal/quota` has one file per provider; `provider.go` holds
the model. The page is `server/internal/webui`. Added accounts are kept in
`~/.config/ant/accounts.json` (mode 0600), the same way Codex and ZCode keep
their sign-ins.

## Reading the page

- **Filter** by category: Subscriptions, API balance or Free.
- **Search** by provider, plan or model ID. Press `/` to jump to the search box.
  A model ID narrows a card down to the matching rows, such as one model in
  Bailian's free quota.
- **Cards view:** group by category, provider or none, and sort by what's
  lost soonest, least left, most left, renewal date or provider name.
- **Timeline view:** every reset, expiry, renewal and end date in time order,
  under Today, Tomorrow, This week, This month and Later.

The page remembers these choices.

## Signing in to websites

Websites without API keys (Suno, the Bailian console) are connected with
**Add account → Sign in**. Ant opens the site's own sign-in page in a Chrome
window with a profile of Ant's own (`~/.config/ant/browser`). You sign in
there as usual; Ant never sees the password. Ant watches that window only
through Chrome's DevTools pipe (no port is opened), takes the site's cookies
the moment it is signed in, keeps them in `accounts.json`, and closes the
window. When a session ends, the card offers **Sign in again**; the window's
profile usually remembers you. It needs Google Chrome, Chromium, Edge or
Brave installed.

Sessions in your everyday Chrome, Arc, Brave, Edge, Vivaldi or Firefox are
also found automatically. Safari's aren't, because its store needs Full Disk
Access.

## Providers and modes

| Provider | Mode | Rule | Found in | Read |
|---|---|---|---|---|
| Z.ai / BigModel | GLM Coding Plan | 5-hour and weekly windows; monthly MCP calls | ZCode (`~/.zcode/v2/config.json`), Claude Code settings, `$ZAI_API_KEY` / `$ZHIPU_API_KEY` | Live: `/api/monitor/usage/quota/limit` |
| Claude | Claude plan | 5-hour and weekly windows, and Opus or Sonnet weekly where the plan has them | Ways: **Claude Code** (`claude -p /usage`; it runs no model and no tools), **Claude Code's sign-in** (`~/.claude/.credentials.json`), an **OAuth token** (`claude setup-token`) | Live: Claude Code's own `/usage`, or `api.anthropic.com/api/oauth/usage` |
| GitHub Copilot | Copilot plan | Monthly premium requests; chat and completions on Free | Ways: **Copilot CLI** (`~/.copilot`, the Keychain `copilot-cli`), **editor sign-in** (`~/.config/github-copilot`), **GitHub CLI** (`~/.config/gh/hosts.yml`), a **GitHub token** | Live: `api.github.com/copilot_internal/user` |
| Gemini CLI | Code Assist | Daily allowance per model | Gemini CLI's Google sign-in (`~/.gemini/oauth_creds.json`; the access token is renewed in memory only) | Live: Code Assist `loadCodeAssist` and `retrieveUserQuota` |
| ChatGPT | ChatGPT plan | 5-hour and weekly Codex windows, extra credits | Codex (`~/.codex/auth.json`) | Live: `chatgpt.com/backend-api/wham/usage` |
| DeepSeek | API balance | Prepaid; granted before topped up | ZCode, `$DEEPSEEK_API_KEY` | Live: `/user/balance` |
| Alibaba Cloud Bailian | Token Plan | Credits per window the plan reports (5 hours, week, month), add-on credits, tool benefits, end date and auto-renew | Ways: **Bailian CLI** (`~/.bailian/config.json`, from `bl login`), **Sign in with Alibaba Cloud** (the console-login the official CLI uses), or an everyday browser's aliyun.com session | Live: Bailian's CLI gateway with the console token |
| | New-user free quota | Every model still covered: granted, left, due date | Ways: **Bailian CLI**, **Sign in with Alibaba Cloud** | Live: `listFoundationModels` + `queryFreeTierQuota` on the CLI gateway, as `bl usage freetier` reads it |
| | Pay-as-you-go | Charged this month and last month, usage worth before free quota, and balance | Ways: **aliyun CLI** AccessKey (`~/.aliyun`, env), **Bailian CLI** AccessKey, an **AccessKey** you add, or the sign-in or browser session | Live: official BSS `QueryAccountBalance` and `QueryBillOverview` |
| OpenCode Go | Go plan | 5-hour, weekly and monthly windows | ZCode | Live: `opencode.ai/zen/go/v1/usage` |
| BigModel 智谱 | GLM Coding Plan | 5-hour and weekly windows | ZCode, Claude Code, OpenCode, `$ZHIPU_API_KEY` | Live: `open.bigmodel.cn/api/monitor/usage/quota/limit` |
| Kimi | Kimi Code | Rolling and weekly windows | ZCode, Claude Code, OpenCode (`kimi-for-coding`) | Live: `api.kimi.com/coding/v1/usages` |
| | Moonshot API balance | Prepaid balance, vouchers first | OpenCode (`moonshotai(-cn)`), `$MOONSHOT_API_KEY` | Live: `/v1/users/me/balance` |
| MiniMax | Token Plan | Per model group: a short window and a week | ZCode, Claude Code, OpenCode (`sk-cp-` keys) | Live: `/v1/token_plan/remains` |
| Volcengine Ark | Coding Plan | 5-hour, weekly (Monday) and monthly windows | Ways: **Volcengine CLI** (`~/.volcengine/config.json`), **Ark CLI** (`~/.arkcli/.env`), **Sign in with Volcengine** (device sign-in, as `ve login` does), an **AccessKey** (or `$VOLC_ACCESSKEY/SECRETKEY`) | Live: OpenAPI `GetCodingPlanUsage`; POST at `ark.cn-beijing.volcengineapi.com`, then GET at `open.volcengineapi.com`, 10 s each |
| Xiaomi MiMo | Token Plan | Plan and compensation credits for the period | Ways: an everyday browser, Ant's sign-in window, a pasted Cookie header | Live: the console's `tokenPlan/usage` and `tokenPlan/detail` |
| | API balance | Prepaid balance, gift included | Same sign-in | Live: the console's `/balance` |
| Manus | Credits | Daily (or weekly) refresh credits up to a cap, monthly plan credits, kept add-on credits | Ways: **API key** (official `GET api.manus.ai/v2/usage.availableCredits`, `$MANUS_API_KEY`), your browser (`session_id`), sign-in window, pasted cookie | Live |
| Perplexity | Credits | Recurring (plan), bought, bonus (expiring) | Ways: your browser, sign-in window, pasted cookie | Live: `/rest/billing/credits` |
| Poe | Compute points | Point balance; what each bot spent in 7 days | **API key** (`$POE_API_KEY`) | Live: `api.poe.com/usage/current_balance`, `points_history` |
| Kling AI | Credits | Lots, each with its own end date (daily free credits, membership, packs) | Ways: your browser, sign-in window, pasted cookie | Live: `klingai.com/api/account/pointAndTicket?scope=PERSONAL` (hundredths) |
| Leonardo.Ai | Tokens | Free: 150 a day; paid: monthly, rollover, bought | Ways: your browser, **API key** (official `GET /me`), sign-in window, pasted cookie | Live: `get-session` → GraphQL `user_details` |
| SeaArt | Stamina and credits | Daily stamina to 00:00 UTC, then kept credits | Ways: your browser, sign-in window, pasted cookie | Live: `POST /api/v1/payment/assets/get` |
| Stable Diffusion Online | Credits | Credits per image or video | Ways: your browser, sign-in window, pasted cookie | Live: tRPC `auth.user` |
| ElevenLabs | Credits | Monthly characters; voice slots | **API key** (`$ELEVENLABS_API_KEY`, needs `user_read`) | Live: `/v1/user/subscription` |
| Figma | AI credits | Monthly per seat; Starter at most 150 a day | Plan and billing date you set | **Rule only**: Figma reports use only in its app |
| Google Stitch | Daily credits | Design credits and redesigns per day, to 00:00 UTC | Nothing to set (sizes adjustable) | **Rule only**: Stitch reports use only in its app |
| OpenRouter | Credits | USD credits bought minus used | ZCode, Claude Code, OpenCode, `$OPENROUTER_API_KEY` | Live: `/api/v1/credits` |
| SiliconFlow | API balance | Gift balance, then topped up | OpenCode, `$SILICONFLOW_API_KEY` | Live: `/v1/user/info` |
| StepFun | API balance | Prepaid balance, vouchers included | OpenCode, `$STEPFUN_API_KEY` | Live: `/v1/accounts` |

Each Bailian instance is its own account and card. **Sign in with Alibaba
Cloud** works the way Bailian's official CLI signs in (`bl auth login
--console`, bailian-cli 2.1.0):
1. Ant listens on a one-off `127.0.0.1` port.
2. It opens Bailian's `console-login` page in your default browser.
3. The page hands a console token back, tied to a random state.

The token reads Bailian's gateway for programs (`/cli/api.json`, bearer
token, no browser cookies). It connects the Token Plan and the free quota.
It doesn't open bills, so pay-as-you-go reads the official billing API with a
read-only AccessKey, or with an everyday browser's session.

**Websites.** A site whose allowance only its signed-in pages show is read
with that sign-in, found three ways, easiest first:
1. your everyday browser: Chrome, Edge, Brave, Arc, Firefox, or Safari;
2. Ant's sign-in window;
3. a pasted Cookie header, as the last resort.

Safari's cookies open only to an app with Full Disk Access. Without it,
Ant skips Safari. To allow it, go to System Settings › Privacy & Security
› Full Disk Access and add the app that runs Ant.

Some sites have no single cookie that marks a sign-in. For those, the
sign-in window waits for **I'm signed in**. A browser that only visited
such a site, without signing in, shows no card.

**Models each account can call.** For routing, every account also
records the models it can call, as its own provider lists them with that
account's sign-in or key. The lists are kept for 6 hours.

| Account | Where its models come from |
|---|---|
| ChatGPT | Codex's own list: `chatgpt.com/backend-api/codex/models?client_version=` (the models in Codex's picker) |
| Claude | `api.anthropic.com/v1/models` with Claude Code's OAuth sign-in (from `~/.claude/.credentials.json`, or the macOS Keychain item "Claude Code-credentials"; macOS asks once) |
| Copilot | The GitHub token is exchanged at `copilot_internal/v2/token`, then `{endpoints.api}/models` (picker models the policy allows) |
| Gemini CLI | Its quota buckets are its models |
| Bailian | Free quota: each model with a grant. Token Plan: `{base_url}/models` with the key `bl` holds |
| DeepSeek, Kimi, Moonshot, MiniMax, Z.ai/BigModel, OpenCode Go, OpenRouter, SiliconFlow, StepFun, Poe | Each one's OpenAI-style `/models` with the key |
| ElevenLabs | `/v1/models` |
| Volcengine Coding Plan, MiMo, the image, video and music sites | Not listed yet: they're read with a console sign-in, not a model key |

In the **Models** view, each model lists every account that can serve it,
best first:
1. what would otherwise be lost: expiring grants, windows about to reset
   with room left, free allowances;
2. plans with the most left;
3. pay-as-you-go balances;
4. what's used up.

The same model reached through different providers is one row
(OpenRouter's `anthropic/claude-x` is `claude-x`). This is the order the
router will try.

**Types and models.** Each provider says what it makes: text, image,
video, music, speech, design, agent or search. A mode can narrow that
(Bailian's Token Plan is text). An allowance that belongs to one model
names it (Bailian's free quota, Gemini). The page filters by type and by
model and groups by type. The **Models** view lists every per-model
allowance across accounts: what's left, and when it resets or expires.

## Honest limits

- **ZCode:** Ant reads the provider keys in `v2/config.json`. ZCode's own
  account sign-in (`credentials.json`) is encrypted and isn't read, so its
  OAuth-only "start plan" isn't shown.
- **ChatGPT:** Ant never refreshes Codex's token itself. When it expires,
  opening Codex once renews it.
- **Suno** has no public API, and its reader can break when Suno changes. The
  first read from a Chromium browser asks for Keychain access to that
  browser's cookie key ("Always Allow" makes it one-time). Safari isn't read.
- **Bailian** is read through the console's internal calls with your
  browser's aliyun.com sign-in. When the sign-in ends, log in again in the
  browser. When the console changes, the card says so;
  [research/ALIYUN-BAILIAN.md](research/ALIYUN-BAILIAN.md) records what Ant
  calls and what it must not call.
- Readings are kept in memory. History and the router come next.
