# Provider logos

Each provider's own icon, fetched once on 2026-10-09 from its site (the homepage's declared
icon, preferring SVG or the largest PNG, else `/favicon.ico`) and converted to a 64 px PNG.
They only label each provider's own accounts on Ant's local page; the page never contacts the
providers (its CSP allows nothing but Ant itself). The page serves `/logos/<id>.png` only.

Leonardo.Ai has none: its site answered the fetch with bot protection (403, then 429), so its
accounts show their initial.

Refresh (downloads one icon per provider; review the result before committing):

    python3 -I scripts/fetch_provider_icons.py <empty download dir>
    sh scripts/convert_provider_icons.sh <download dir> <work dir> server/internal/webui/static/logos

When a provider is added, add its icon here and its id to `LOGOS` in `app.js`.

| Provider | Source |
|---|---|
| `aliyun` | https://img.alicdn.com/tfs/TB1_ZXuNcfpK1RjSZFOXXa6nFXa-32-32.ico |
| `bigmodel` | https://bigmodel.cn/static/images/favicon.png |
| `chatgpt` | https://chatgpt.com/cdn/assets/favicon-l4nq08hd.svg |
| `claude` | https://claude.ai/favicon.ico |
| `copilot` | https://github.githubassets.com/favicons/favicon.svg |
| `deepseek` | https://www.deepseek.com/favicon.ico |
| `elevenlabs` | https://elevenlabs.io/icon.svg?c23f5371fdd26632 |
| `figma` | https://static.figma.com/app/icon/2/favicon.svg |
| `gemini` | https://www.gstatic.com/lamda/images/gemini_sparkle_4g_512_lt_f94943af3be039176192d.png |
| `kimi` | https://www.kimi.com/pwa-192.png |
| `kling` | https://p4-kling.klingai.com/kcdn/cdn-kcdn112452/kling-homepage-aio/logo-180x180.png |
| `manus` | https://manus.im/icon.svg?icon.2kbcs13ndm9it.svg |
| `minimax` | https://www.minimax.cn/favicon.ico |
| `opencode` | https://opencode.ai/apple-touch-icon-v3.png |
| `openrouter` | https://openrouter.ai/apple-touch-icon.png |
| `perplexity` | https://www.perplexity.ai/favicon.ico |
| `poe` | https://poe.com/favicon.ico |
| `sdweb` | https://stablediffusionweb.com/icon.png?566a66bcbc780bd8 |
| `seaart` | https://www.seaart.ai/apple-touch-icon.png |
| `siliconflow` | https://www.siliconflow.cn/favicon.ico |
| `stepfun` | https://www.stepfun.com/step_favicon.svg |
| `stitch` | https://www.gstatic.com/labs-code/stitch/favicon-512x512.png |
| `suno` | https://cdn-o.suno.com/favicon-512x512.png |
| `volcengine` | https://portal.volccdn.com/obj/volcfe/misc/favicon.png |
| `xiaomi` | https://platform.xiaomimimo.com/favicon.png |
| `zai` | https://z-cdn.chatglm.cn/z-ai/static/logo.svg |
