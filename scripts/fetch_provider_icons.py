"""Fetch each provider's own icon once: read the homepage's declared icons,
take the best (SVG, else the largest PNG / apple-touch-icon), else
/favicon.ico. Each download goes into its own new directory."""
import html.parser, json, os, sys, urllib.parse, urllib.request

OUT = sys.argv[1]
SITES = {
    "chatgpt": "https://chatgpt.com", "claude": "https://claude.ai", "copilot": "https://github.com",
    "gemini": "https://gemini.google.com", "zai": "https://z.ai", "bigmodel": "https://bigmodel.cn",
    "kimi": "https://www.kimi.com", "minimax": "https://www.minimaxi.com", "aliyun": "https://www.aliyun.com",
    "volcengine": "https://www.volcengine.com", "xiaomi": "https://platform.xiaomimimo.com", "opencode": "https://opencode.ai",
    "deepseek": "https://www.deepseek.com", "openrouter": "https://openrouter.ai", "siliconflow": "https://www.siliconflow.cn",
    "stepfun": "https://www.stepfun.com", "manus": "https://manus.im", "perplexity": "https://www.perplexity.ai",
    "poe": "https://poe.com", "kling": "https://klingai.com", "leonardo": "https://leonardo.ai",
    "seaart": "https://www.seaart.ai", "sdweb": "https://stablediffusionweb.com", "suno": "https://suno.com",
    "elevenlabs": "https://elevenlabs.io", "figma": "https://www.figma.com", "stitch": "https://stitch.withgoogle.com",
}
UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36"
LIMIT = 512 * 1024


def get(url):
    req = urllib.request.Request(url, headers={"User-Agent": UA, "Accept": "*/*"})
    with urllib.request.urlopen(req, timeout=15) as r:
        return r.geturl(), r.headers.get("Content-Type", ""), r.read(LIMIT + 1)


class Icons(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.found = []

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        rel = (a.get("rel") or "").lower()
        if tag == "link" and "icon" in rel and a.get("href"):
            self.found.append(a)


def score(a):
    href, typ, sizes = a["href"].lower(), (a.get("type") or "").lower(), (a.get("sizes") or "").lower()
    if "mask-icon" in (a.get("rel") or "").lower():
        return -1  # monochrome Safari pinned-tab mask
    if href.endswith(".svg") or "svg" in typ:
        return 10000
    best = 0
    for s in sizes.split():
        try:
            best = max(best, int(s.split("x")[0]))
        except ValueError:
            pass
    if "apple-touch-icon" in (a.get("rel") or "").lower():
        best = max(best, 180)
    return best or 32


report = {}
for pid, site in SITES.items():
    d = os.path.join(OUT, pid)
    os.makedirs(d, exist_ok=True)
    tried = []
    try:
        base, _, page = get(site)
        p = Icons()
        p.feed(page.decode("utf-8", "replace"))
        cands = sorted(p.found, key=score, reverse=True)
        urls = [urllib.parse.urljoin(base, c["href"]) for c in cands if score(c) >= 0]
    except Exception as e:  # noqa: BLE001
        base, urls = site, []
        tried.append(f"homepage: {e}")
    urls.append(urllib.parse.urljoin(base, "/favicon.ico"))
    for u in urls:
        if u.startswith("data:"):
            continue
        try:
            final, ctype, body = get(u)
            if len(body) > LIMIT or len(body) < 32 or b"<html" in body[:400].lower():
                tried.append(f"{u}: unusable ({len(body)} bytes)")
                continue
            ext = ".svg" if (u.lower().split("?")[0].endswith(".svg") or "svg" in ctype) else ".ico" if (u.lower().split("?")[0].endswith(".ico") or "icon" in ctype) else ".png"
            path = os.path.join(d, "icon" + ext)
            with open(path, "wb") as f:
                f.write(body)
            report[pid] = {"from": final, "type": ctype, "bytes": len(body), "file": path}
            break
        except Exception as e:  # noqa: BLE001
            tried.append(f"{u}: {e}")
    if pid not in report:
        report[pid] = {"failed": tried[-3:]}
print(json.dumps(report, indent=1))
