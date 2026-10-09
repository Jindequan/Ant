"use strict";

const HUES = {
  chatgpt: 162, claude: 18, copilot: 210, gemini: 225, zai: 232, bigmodel: 222, kimi: 252, minimax: 340, aliyun: 28, volcengine: 200, xiaomi: 22, opencode: 270,
  deepseek: 214, openrouter: 240, siliconflow: 280, stepfun: 190, suno: 8,
  manus: 30, perplexity: 186, poe: 262, kling: 150, leonardo: 290, seaart: 205, sdweb: 312, elevenlabs: 0, figma: 12, stitch: 120,
};
// ICON is a small set of line icons, in the ant's own line style.
const ICON = {
  key: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><circle cx="8" cy="15" r="4"/><path d="M11 12l8-8M16 7l2.5 2.5M14 9l2 2"/></svg>',
  clock: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/></svg>',
  alert: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17v.01"/></svg>',
  open: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/></svg>',
  edit: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 20h4L19 9l-4-4L4 16z"/></svg>',
  pause: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6v12M15 6v12"/></svg>',
  play: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5l11 7-11 7z"/></svg>',
  trash: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 7h14M10 7V4h4v3M7 7l1 13h8l1-13"/></svg>',
};
const SESSION_WARN_DAYS = 7;

// sessionBadge is a sign-in's end: a key, amber when it's near.
function sessionBadge(a, full) {
  const t = time(a.sessionEndsAt);
  if (!t || t < Date.now()) return "";
  const near = t - Date.now() < SESSION_WARN_DAYS * 864e5;
  if (!near && !full) return "";
  const title = `Sign-in ends ${dayFmt.format(t)}`;
  return `<span class="sess ${near ? "near" : ""}" title="${esc(title)}">${ICON.key}${full ? esc(title) : until(t)}</span>`;
}

// LOGOS are the providers whose own icon is bundled with the page
// (webui/static/logos, fetched once from each provider's site); the others
// show their initial.
const LOGOS = new Set(["aliyun", "bigmodel", "chatgpt", "claude", "copilot", "deepseek", "elevenlabs", "figma", "gemini", "kimi", "kling", "manus", "minimax", "opencode", "openrouter", "perplexity", "poe", "sdweb", "seaart", "siliconflow", "stepfun", "stitch", "suno", "volcengine", "xiaomi", "zai"]);

// mark is a provider's logo on a small tile, or its initial in its colour.
function mark(provider, name, cls = "") {
  if (LOGOS.has(provider)) return `<span class="mark logo-mark ${cls}" aria-hidden="true"><img src="/logos/${provider}.png" alt="" decoding="async"></span>`;
  return `<span class="mark ${cls}" aria-hidden="true">${esc(String(name || "").slice(0, 1))}</span>`;
}
const MEDIA_LABEL = { text: "Text", image: "Image", video: "Video", music: "Music", speech: "Speech", audio: "Audio", design: "Design", agent: "Agent", search: "Search" };
const MEDIA_ORDER = Object.keys(MEDIA_LABEL);
const KIND_LABEL = { subscription: "Subscription", api: "API balance", free: "Free" };
const URGENT_HOURS = 24; // a window renewing this soon with this much left is "use it"
const URGENT_LEFT = 30;
const EXPIRING_DAYS = 14; // a grant ending this soon is "use it"

const state = { snap: null, providers: [], insights: {}, view: "cards", sort: "soon", q: "", busy: false, open: new Set() };
const PREFS = ["view"];
try { for (const k of PREFS) state[k] = localStorage.getItem(`ant.${k}`) || state[k]; } catch { /* storage off */ }
function savePref(k) { try { localStorage.setItem(`ant.${k}`, state[k]); } catch { /* storage off */ } }

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const time = (s) => { const t = s ? Date.parse(s) : NaN; return Number.isFinite(t) && t > 946684800000 ? t : null; };
const num = (n) => Number(n).toLocaleString();
const compactFmt = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });
const short = (n) => Math.abs(Number(n)) >= 10000 ? compactFmt.format(Number(n)) : num(n);

function ago(t) {
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}
function until(t) {
  const m = Math.max(0, Math.round((t - Date.now()) / 60000));
  if (m < 1) return "now";
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return h % 24 && d < 7 ? `${d}d ${h % 24}h` : `${d}d`;
}
const dateFmt = new Intl.DateTimeFormat(undefined, { weekday: "short", hour: "numeric", minute: "2-digit" });
const dayFmt = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });

// percentLeft is what's left of a window, 0–100, or null when unknown.
function percentLeft(w) {
  if (w.usedPercent != null) return Math.max(0, Math.min(100, 100 - w.usedPercent));
  if (w.left != null && w.limit) return Math.max(0, Math.min(100, (w.left / w.limit) * 100));
  if (w.used != null && w.limit) return Math.max(0, Math.min(100, 100 - (w.used / w.limit) * 100));
  return null;
}
// urgency says what to use before it is lost. When the recent pace uses an
// allowance up before it resets or expires (a), nothing is lost: no urgency.
function urgency(w, a) {
  if (w.aside) return null;
  if (a && windowInsight(a, w)?.outlook === "runs_out") return null;
  const r = time(w.resetsAt);
  if (r == null) return null;
  if (w.expires) return r - Date.now() < EXPIRING_DAYS * 864e5 ? "Use before it expires" : null;
  const p = percentLeft(w);
  return p != null && p >= URGENT_LEFT && r - Date.now() < URGENT_HOURS * 3600e3 ? "Use before reset" : null;
}
function money(currency, amount) {
  const n = Number(amount);
  if (!Number.isFinite(n)) return esc(amount);
  if (currency === "credits") return `${num(n)} credits`;
  try {
    return new Intl.NumberFormat(undefined, { style: "currency", currency, currencyDisplay: "narrowSymbol" }).format(n);
  } catch {
    return `${esc(currency)} ${n.toFixed(2)}`;
  }
}

// windows and balances of an account, its groups' included
const allWindows = (a) => [...(a.windows || []), ...(a.groups || []).flatMap((g) => g.windows || [])];
const allBalances = (a) => [...(a.balances || []), ...(a.groups || []).flatMap((g) => g.balances || [])];

// order puts soonest-to-be-lost first, then the rest that could be read,
// then problems.
function order(a) {
  if (a.status !== "ok") return 3e15;
  const soon = allWindows(a).filter((w) => urgency(w, a)).map((w) => time(w.resetsAt));
  if (soon.length) return Math.min(...soon);
  const resets = allWindows(a).filter((w) => !w.aside).map((w) => time(w.resetsAt)).filter(Boolean);
  return 1e15 + (resets.length ? Math.min(...resets) : 9e14);
}

// ---------- the glance ----------

// leftText is what's left of a window, in a few words.
function leftText(w) {
  const p = percentLeft(w);
  if (w.left != null) return `<b>${short(w.left)}</b>${w.limit ? ` of ${short(w.limit)}` : ""} ${esc(w.unit || "")} left`;
  if (p === 0) return "<b>Used up</b>";
  if (p != null) return `<b>${Math.round(p)}%</b> left`;
  if (w.limit != null) return `<b>${short(w.limit)}</b> ${esc(w.unit || "")}`;
  return "Unknown";
}

// renderSummary is the top of the page: what to use before it's lost, and
// one line of totals.
function renderSummary(accounts) {
  const ok = accounts.filter((a) => a.status === "ok");
  const urgent = ok.flatMap((a) => allWindows(a).filter((w) => urgency(w, a)).map((w) => ({ a, w })));
  urgent.sort((x, y) => time(x.w.resetsAt) - time(y.w.resetsAt));
  const subs = ok.filter((a) => a.kind === "subscription");
  const free = ok.filter((a) => a.kind === "free");
  const next = subs.map((a) => ({ t: time(a.renewsAt) || time(a.endsAt), ends: !a.renewsAt && !!a.endsAt })).filter((x) => x.t).sort((x, y) => x.t - y.t)[0];
  const totals = {};
  for (const a of ok) {
    // money held: an API account's balances and unlabelled group balances;
    // a labelled group amount ("Last month") is what was spent, not held
    const held = (a.groups || []).flatMap((g) => g.balances || []).filter((b) => !b.label);
    const bs = a.kind === "api" ? [...(a.balances || []), ...held] : held;
    for (const b of bs) if (b.currency !== "credits") totals[b.currency] = (totals[b.currency] || 0) + Number(b.amount);
  }
  const balance = Object.entries(totals).map(([c, n]) => money(c, n.toFixed(2)));
  const problems = accounts.filter((a) => a.status === "signed_out" || a.status === "error").length;
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
  const facts = [
    subs.length ? plural(subs.length, "subscription", "subscriptions") + (next ? `, next ${next.ends ? "ends" : "renews"} ${dayFmt.format(next.t)}` : "") : "",
    monthly(subs),
    balance.length ? `${balance.join(" + ")} prepaid` : "",
    free.length ? plural(free.length, "free allowance", "free allowances") : "",
    problems ? `<span class="bad">${plural(problems, "account needs", "accounts need")} you</span>` : "",
  ].filter(Boolean);
  const shown = urgent.slice(0, 4);
  $("summary").innerHTML = `
    <div class="g-head">
      <h2>${urgent.length ? "Use first" : "Nothing is about to be lost"}</h2>
      <p class="g-facts">${facts.join('<span class="sep">·</span>')}</p>
    </div>
    ${shown.length ? `<ol class="g-list">${shown.map(({ a, w }) => `
      <li><button type="button" class="g-item" data-goto="${esc(a.id)}" style="--hue:${HUES[a.provider] ?? 30}">
        ${mark(a.provider, a.name, "sm")}
        <span class="g-what"><b>${esc(a.label || a.name)}</b> ${esc(w.name)}</span>
        <span class="g-left">${leftText(w)}</span>
        <span class="g-when">${w.expires ? "ends" : "resets"} in ${until(time(w.resetsAt))}</span>
      </button></li>`).join("")}</ol>` : ""}
    ${urgent.length > shown.length ? `<p class="g-more">${urgent.length - shown.length} more marked below</p>` : ""}`;
}

// insightsOf is what the history says about an account's rows: its own
// connection's and those merged into its card.
function insightsOf(a) {
  return [...new Set([a.id, ...(a.keys || [])])].flatMap((id) => state.insights[id] || []); // keys include a.id
}

function windowInsight(a, w) {
  return a && insightsOf(a).find((i) => i.window === w.name && (i.model || "") === (w.model || ""));
}

// outlook speaks only when the recent pace uses an allowance up before it
// resets or expires; lasting is what's expected and says nothing.
function outlook(i) {
  if (i?.outlook === "runs_out" && time(i.at)) return `<span class="outlook soon">runs out in ~${until(time(i.at))} at this pace</span>`;
  return "";
}

// spark draws recent values as a small line.
function spark(points) {
  if (!points || points.length < 2) return "";
  const xs = points.map((p) => p[0]), ys = points.map((p) => p[1]);
  const x0 = Math.min(...xs), x1 = Math.max(...xs), y0 = Math.min(...ys), y1 = Math.max(...ys);
  const px = (x) => x1 === x0 ? 0 : ((x - x0) / (x1 - x0)) * 116 + 2;
  const py = (y) => y1 === y0 ? 12 : 22 - ((y - y0) / (y1 - y0)) * 20;
  return `<svg class="spark" viewBox="0 0 120 24" aria-hidden="true"><polyline points="${points.map((p) => `${px(p[0]).toFixed(1)},${py(p[1]).toFixed(1)}`).join(" ")}"/></svg>`;
}

// monthly is what the priced accounts cost a month, per currency.
function monthly(accounts) {
  const per = {};
  for (const a of accounts) {
    const p = state.snap?.prices?.[a.id];
    if (p) per[p.currency] = (per[p.currency] || 0) + Number(p.amount) / (p.period === "year" ? 12 : 1);
  }
  const parts = Object.entries(per).map(([c, n]) => money(c, n.toFixed(2)));
  return parts.length ? `${parts.join(" + ")} a month` : "";
}

function renderWindow(w, compact, a) {
  const p = percentLeft(w);
  const r = time(w.resetsAt);
  const level = p == null ? "" : p < 20 ? "low" : p < 50 ? "warn" : "";
  const unit = esc(w.unit || "");
  let left;
  if (w.left != null) left = `<b>${num(w.left)}</b>${w.limit ? ` of ${num(w.limit)}` : ""} ${unit} left`;
  else if (p === 0) left = `<b>Used up</b>`;
  else if (p != null) left = `<b>${Math.round(p)}%</b> left`;
  else if (w.limit != null) left = `<b>${num(w.limit)}</b> ${unit}`;
  else left = "Unknown";
  const verb = w.expires ? "Expires" : "Resets";
  const when = r ? `${verb} in ${until(r)} · ${w.expires ? dayFmt.format(r) : dateFmt.format(r)}${w.resetEstimated ? " (expected)" : ""}` : "Reset time not reported";
  const tag = compact ? null : urgency(w, a);
  return `
    <div class="win ${w.aside ? "aside" : ""} ${compact ? "compact" : ""}">
      <div class="row">
        <span class="name">${esc(w.name)}${tag ? `<span class="tag">${tag}</span>` : ""}</span>
        <span class="left">${left}</span>
      </div>
      <div class="meter ${p == null ? "unknown" : ""}" role="meter" aria-label="${esc(w.name)} left" aria-valuemin="0" aria-valuemax="100" ${p == null ? "" : `aria-valuenow="${Math.round(p)}"`}>
        ${p == null ? "" : `<i class="${level}" style="width:${p.toFixed(1)}%"></i>`}
      </div>
      ${compact ? "" : `<div class="when">${when}${(() => { const o = outlook(windowInsight(a, w)); return o ? ` · ${o}` : ""; })()}</div>`}
    </div>`;
}

function renderBalance(a) {
  return (a.balances || []).map((b) => {
    const parts = [Number(b.granted) ? `Granted ${money(b.currency, b.granted)}` : "", Number(b.toppedUp) ? `Topped up ${money(b.currency, b.toppedUp)}` : ""].filter(Boolean);
    const label = b.label ? `<div class="parts">${esc(b.label)}</div>` : a.kind === "api" ? "" : `<div class="parts">Extra credits</div>`;
    const i = insightsOf(a).find((x) => x.currency === b.currency && (x.label || "") === (b.label || ""));
    let pace = "";
    if (i?.outlook === "runs_out" && time(i.at)) pace = `≈ ${until(time(i.at))} left at ${money(b.currency, i.perDay.toFixed(2))}/day`;
    else if (i?.outlook === "idle") pace = "Nothing spent this week";
    return `<div class="balance">${label}<div class="amt">${money(b.currency, b.amount)}${spark(i?.points)}</div>${parts.length ? `<div class="parts">${parts.join(" · ")}</div>` : ""}${pace ? `<div class="parts pace">${pace}</div>` : ""}</div>`;
  }).join("");
}

function renderGroup(g, a) {
  const balances = (g.balances || []).map((b) => b.label
    ? `<div class="win compact"><div class="row"><span class="name">${esc(b.label)}</span><span class="left"><b>${money(b.currency, b.amount)}</b></span></div></div>`
    : `<div class="balance"><div class="amt">${money(b.currency, b.amount)}</div></div>`).join("");
  const ws = g.windows || [];
  // windows that all end together say so once, for the group
  const shared = ws.length > 1 && ws.every((w) => w.resetsAt === ws[0].resetsAt && !!w.expires === !!ws[0].expires) && time(ws[0].resetsAt);
  const rows = ws.map((w) => renderWindow(w, !!shared, a));
  const windows = rows.length > 6
    ? rows.slice(0, 5).join("") + `<details class="more"><summary>Show all ${rows.length}</summary>${rows.slice(5).join("")}</details>`
    : rows.join("");
  let together = "";
  if (shared) {
    const tag = ws.some(urgency) ? `<span class="tag">${ws[0].expires ? "Use before they end" : "Use before reset"}</span>` : "";
    together = `<div class="when together">${ws[0].expires ? "All end" : "All reset"} in ${until(shared)} · ${dayFmt.format(shared)}${tag}</div>`;
  }
  const link = g.link ? ` <a href="${esc(g.link)}" target="_blank" rel="noopener noreferrer">Open in console ↗</a>` : "";
  const action = g.action === "edit" ? `<div><button type="button" class="btn" data-edit="${esc(a.id)}">Add API key</button></div>` : "";
  const empty = !ws.length && !balances && g.action === "edit" ? `<div class="empty-list">No models listed yet</div>` : "";
  return `<section class="group"><h4>${esc(g.title)}</h4>${balances}${windows}${empty}${together}${g.note || link ? `<p class="note">${esc(g.note || "")}${link}</p>` : ""}${action}</section>`;
}

// cardBody is an account's detail: every allowance, balance and group, or
// why it couldn't be read and what to do.
function cardBody(a) {
  let body = "";
  if (a.status === "ok") {
    const main = (a.windows || []).filter((w) => !w.aside);
    const aside = (a.windows || []).filter((w) => w.aside);
    const rows = main.map((w) => renderWindow(w, false, a));
    const list = rows.length > 6
      ? rows.slice(0, 5).join("") + `<details class="more"><summary>Show all ${rows.length}</summary>${rows.slice(5).join("")}</details>`
      : rows.join("");
    body = renderBalance(a) + list + aside.map((w) => renderWindow(w, false, a)).join("") + (a.groups || []).map((g) => renderGroup(g, a)).join("");
    if (a.message) body += `<p class="note">${esc(a.message)}</p>`;
    if (!body) body = `<p class="note">Connected. The provider reported no allowance or balance.</p>`;
    return body;
  }
  body = `<p class="note ${a.status === "not_connected" ? "" : "problem"}">${esc(a.message || "Couldn't read this account.")}</p>`;
  const mode = modeOf(a);
  const buttons = [];
  if (a.status === "signed_out" && mode?.signIn) {
    buttons.push(`<button type="button" class="btn primary" data-signin-card="${esc(a.provider)}" data-mode="${esc(a.mode)}" data-way="${esc(a.way || "")}" data-id="${esc(a.id)}">Sign in${a.message?.includes("again") ? " again" : ""}</button>`);
  }
  if (a.status === "not_connected" && mode?.fields?.length) {
    const what = mode.fields.find((f) => !f.optional || f.type === "secret" || f.id.toLowerCase().includes("key"));
    buttons.push(`<button type="button" class="btn primary" data-edit="${esc(a.id)}">Add ${esc(what?.label?.replace(/ ID$/, "") || "details")}</button>`);
  }
  if (buttons.length) body += `<div class="buttons">${buttons.join("")}</div>`;
  return body;
}

// ownWindows are what a row speaks for: the account's own allowances, else
// its groups' (a plan's tool benefits don't stand for the plan).
function ownWindows(a) {
  const own = (a.windows || []).filter((w) => !w.aside);
  return own.length ? own : allWindows(a).filter((w) => !w.aside);
}

// headline is the one value a row shows: a balance, the allowance to use
// first, else the tightest one; or why there is none.
function headline(a) {
  if (a.status !== "ok") {
    return `<span class="a-state ${a.status === "not_connected" ? "" : "bad"}">${{ signed_out: "Signed out", error: "Couldn't read", not_connected: "Not set up" }[a.status] || "—"}</span>`;
  }
  const ws = ownWindows(a);
  const b = (a.balances || [])[0];
  if (b && (a.kind === "api" || !ws.length)) return `<span class="a-amt">${money(b.currency, b.amount)}</span>`;
  if (a.kind === "free" && ws.length > 2) return `<span class="a-count"><b>${ws.length}</b> allowances</span>`;
  const first = ws.filter((w) => urgency(w, a)).sort((x, y) => time(x.resetsAt) - time(y.resetsAt))[0];
  const known = ws.filter((w) => percentLeft(w) != null).sort((x, y) => percentLeft(x) - percentLeft(y));
  const w = first || known[0] || ws[0];
  if (!w) return '<span class="a-state">Connected</span>';
  const p = percentLeft(w);
  const level = p == null ? "" : p < 20 ? "low" : p < 50 ? "warn" : "";
  return `<span class="a-meter"><span class="meter small ${p == null ? "unknown" : ""}">${p == null ? "" : `<i class="${level}" style="width:${p.toFixed(1)}%"></i>`}</span><span class="a-left">${leftText(w)}${ws.length > 1 ? ` <span class="a-of">${esc(w.name)}</span>` : ""}</span></span>`;
}

// nextOf is what happens next to an account, in a few words.
function nextOf(a) {
  if (a.status !== "ok") return "";
  const ws = ownWindows(a);
  const first = ws.filter((w) => urgency(w, a)).sort((x, y) => time(x.resetsAt) - time(y.resetsAt))[0];
  if (first) return `<span class="tag">${first.expires ? "Ends" : "Resets"} in ${until(time(first.resetsAt))}</span>`;
  const soon = ws.map((w) => ({ w, t: time(w.resetsAt) })).filter((x) => x.t).sort((x, y) => x.t - y.t)[0];
  if (soon) return `${soon.w.expires ? "Ends" : "Resets"} in ${until(soon.t)}`;
  const renews = time(a.renewsAt), ends = time(a.endsAt);
  if (renews) return `Renews ${dayFmt.format(renews)}`;
  if (ends) return `Ends ${dayFmt.format(ends)}`;
  return "";
}

function renderAccount(a) {
  const kind = KIND_LABEL[a.kind];
  const sub = [a.plan, a.modeName && a.modeName !== a.plan ? a.modeName : "", !a.modeName && a.plan !== kind ? kind : "", a.user].filter(Boolean).map(esc).join(" · ");
  const read = time(a.readAt);
  const renews = time(a.renewsAt);
  const ends = !renews && time(a.endsAt);
  const life = renews ? `Renews ${dayFmt.format(renews)}` : ends ? `Ends ${dayFmt.format(ends)}${a.autoRenew === false ? " · auto-renew off" : ""}` : "";
  const price = state.snap?.prices?.[a.id];
  const cost = price ? `<button type="button" class="link" data-price="${esc(a.id)}">${money(price.currency, price.amount)}/${price.period === "year" ? "yr" : "mo"}</button>`
    : a.kind === "subscription" && a.status === "ok" ? `<button type="button" class="link quiet" data-price="${esc(a.id)}">Add price</button>` : "";
  const where = a.source?.startsWith("Signed in") ? a.source
    : a.origin === "added" ? (a.source ? `Yours · from ${a.source}` : "Added by you")
    : a.source ? `Found in ${a.source}` : "";
  const via = a.wayName && !where.includes(a.wayName) ? `via ${a.wayName}` : "";
  const how = a.live ? (read ? `read ${ago(read)}` : "") : "shown from its rule, not read";
  const facts = [via || where, how].filter(Boolean).map(esc).join(" · ");
  const paused = isPaused(a);
  const open = state.open.has(a.id) || (state.q && a.narrowed);
  return `
    <details class="acct ${a.status === "ok" ? "" : "attn"}" data-id="${esc(a.id)}" style="--hue:${HUES[a.provider] ?? 30}" ${open ? "open" : ""}>
      <summary class="a-row">
        ${mark(a.provider, a.name)}
        <span class="a-who"><b>${esc(a.label || a.name)}</b>${paused ? ' <span class="tag off">Paused</span>' : ""}<span class="a-sub">${a.label ? `${esc(a.name)} · ` : ""}${sub}</span></span>
        <span class="a-val">${headline(a)}</span>
        <span class="a-next">${sessionBadge(a, false)}${nextOf(a)}</span>
        <svg class="chev" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6l6 6-6 6"/></svg>
      </summary>
      <div class="a-detail">
        <div class="body">${cardBody(a)}</div>
        <div class="a-foot">
          <span class="src"><i class="dot ${esc(a.status)}"></i><span>${facts}</span>${sessionBadge(a, true)}</span>
          ${life || cost ? `<span class="${ends && a.autoRenew === false ? "ending" : ""}">${[life, cost].filter(Boolean).join(" · ")}</span>` : ""}
        </div>
        <div class="a-actions">
          ${a.link ? `<a class="btn sm" href="${esc(a.link)}" target="_blank" rel="noopener noreferrer">${ICON.open}Open</a>` : ""}
          <button type="button" class="btn sm" data-edit="${esc(a.id)}">${ICON.edit}Edit</button>
          ${routable(a) ? `<button type="button" class="btn sm" data-pause="${esc(a.id)}" data-paused="${paused ? "0" : "1"}" title="${paused ? "Use it for routes again" : "Leave it out of routes"}">${paused ? ICON.play + "Resume" : ICON.pause + "Pause"}</button>` : ""}
          <button type="button" class="btn sm quiet" data-remove-card="${esc(a.id)}" title="${a.origin === "found" ? "Hide" : "Remove"}" aria-label="${a.origin === "found" ? "Hide" : "Remove"}">${ICON.trash}</button>
        </div>
      </div>
    </details>`;
}

// ---------- finding, grouping, sorting ----------

const norm = (x) => String(x ?? "").toLowerCase();

// matches narrows an account to the search: the whole account when its
// provider, plan, user, what it makes or one of its models matches, else
// only the allowances whose name, model or media match. null when nothing
// does. One search box replaces separate type and model pickers.
function matches(a, q) {
  if (!a || !q) return a;
  const words = q.split(/\s+/).filter(Boolean);
  const makes = (a.media || ["text"]).map((m) => MEDIA_LABEL[m] || m);
  const hay = norm([a.name, a.label, a.plan, a.modeName, a.user, a.provider, a.source, KIND_LABEL[a.kind], ...makes,
    ...(a.models || []).map((m) => m.id)].join(" "));
  if (words.every((w) => hay.includes(w))) return a;
  const rowHay = (w) => norm([w.name, w.model, w.media && MEDIA_LABEL[w.media]].join(" "));
  const hit = (w) => words.every((x) => rowHay(w).includes(x) || hay.includes(x)) && words.some((x) => rowHay(w).includes(x));
  const windows = (a.windows || []).filter(hit);
  const groups = (a.groups || []).map((g) => ({ ...g, windows: (g.windows || []).filter(hit), balances: [] })).filter((g) => g.windows.length);
  if (!windows.length && !groups.length) return null;
  return { ...a, windows, groups, balances: [], message: "", narrowed: true };
}

// mediaOf is what an allowance makes: its own media, else its account's.
const mediaOf = (w, a) => w.media || (a.media || ["text"])[0];

// leftOf is how much of an account is left, 0–100, for sorting; balances
// sort after allowances, unknown last.
function leftOf(a) {
  const ps = allWindows(a).filter((w) => !w.aside).map(percentLeft).filter((p) => p != null);
  if (ps.length) return Math.min(...ps);
  return allBalances(a).length ? 150 : 200;
}

const SORTS = {
  soon: (x, y) => order(x) - order(y),
  least: (x, y) => leftOf(x) - leftOf(y) || order(x) - order(y),
  most: (x, y) => (leftOf(x) > 100) - (leftOf(y) > 100) || leftOf(y) - leftOf(x) || order(x) - order(y),
  renewal: (x, y) => (time(x.renewsAt) || time(x.endsAt) || 9e15) - (time(y.renewsAt) || time(y.endsAt) || 9e15) || order(x) - order(y),
  name: (x, y) => norm(x.name + x.modeName + x.label).localeCompare(norm(y.name + y.modeName + y.label)),
};

const KIND_ORDER = ["subscription", "free", "api"];
const KIND_TITLE = { subscription: "Subscriptions", free: "Free allowances", api: "API balances" };

// sections groups cards by category, the same categories as the tabs.
function sections(accounts) {
  const by = new Map();
  for (const a of accounts) {
    if (!by.has(a.kind)) by.set(a.kind, []);
    by.get(a.kind).push(a);
  }
  return [...by.keys()].sort((x, y) => KIND_ORDER.indexOf(x) - KIND_ORDER.indexOf(y))
    .map((k) => ({ title: KIND_TITLE[k] || k, items: by.get(k) }));
}

// ---------- the models view: who can serve each model ----------

// modelKey groups the same model across providers: OpenRouter's
// "anthropic/claude-x" is claude-x.
const modelKey = (id) => norm(id).split("/").pop();

// route is one account's way to a model: the allowance that applies (the
// model's own, else the account's tightest) and its cost class.
function route(a, id, media) {
  const own = allWindows(a).filter((w) => w.model && modelKey(w.model) === modelKey(id));
  const shared = (a.windows || []).filter((w) => !w.aside && !w.model);
  const ws = own.length ? own : shared;
  const ps = ws.map(percentLeft).filter((p) => p != null);
  const p = ps.length ? Math.min(...ps) : null;
  const w = ws.find((x) => percentLeft(x) === p) || ws[0] || null;
  const expiring = ws.some((x) => x.expires || urgency(x, a));
  const pays = a.kind === "api" || (!ws.length && allBalances(a).length);
  const cls = p === 0 ? 9 : expiring || a.kind === "free" ? 0 : pays ? 2 : 1;
  return { a, id, media, w, p, cls, t: w ? time(w.resetsAt) : null, pays };
}

// routes is every model any shown account can call, each with its
// accounts best first: what would be lost first, then plans with the most
// left, then pay-as-you-go, then what is used up.
function routes(accounts) {
  const by = new Map();
  for (const a of accounts) {
    if (a.status !== "ok") continue;
    const seen = new Set();
    const add = (id, media) => {
      const k = modelKey(id);
      if (!id || seen.has(k)) return;
      seen.add(k);
      if (!by.has(k)) by.set(k, { id, media: media || "text", via: [] });
      by.get(k).via.push(route(a, id, media));
    };
    for (const m of a.models || []) add(m.id, m.media);
    for (const w of allWindows(a)) if (w.model) add(w.model, mediaOf(w, a));
  }
  const rank = (x, y) => x.cls - y.cls || (x.cls === 0 ? (x.t || 9e15) - (y.t || 9e15) : 0) || (y.p ?? 50) - (x.p ?? 50);
  const out = [...by.values()];
  for (const r of out) { r.via.sort(rank); r.best = r.via[0]; }
  const sorts = {
    soon: (x, y) => x.best.cls - y.best.cls || (x.best.t || 9e15) - (y.best.t || 9e15),
    least: (x, y) => (x.best.p ?? 200) - (y.best.p ?? 200),
    most: (x, y) => (y.best.p ?? -1) - (x.best.p ?? -1),
    renewal: (x, y) => (x.best.t || 9e15) - (y.best.t || 9e15),
    name: () => 0,
  };
  return out.sort((x, y) => (sorts[state.sort] || sorts.soon)(x, y) || x.id.localeCompare(y.id));
}

function routeLeft(r) {
  const w = r.w;
  if (w && w.left != null) return `${short(w.left)}${w.limit ? ` / ${short(w.limit)}` : ""} ${esc(w.unit || "")}`;
  if (r.p != null) return `${Math.round(r.p)}%`;
  const b = allBalances(r.a)[0];
  return b ? money(b.currency, b.amount) : "—";
}

function routeRow(r) {
  const level = r.p == null ? "" : r.p < 20 ? "low" : r.p < 50 ? "warn" : "";
  const why = r.cls === 9 ? "Used up" : r.pays ? "Pay as you go" : r.w && urgency(r.w, r.a) ? urgency(r.w, r.a) : "";
  return `<span class="r-acct" style="--hue:${HUES[r.a.provider] ?? 30}">${mark(r.a.provider, r.a.name)}${esc(r.a.label || r.a.name)}${r.a.modeName ? ` · ${esc(r.a.modeName)}` : ""}</span>
    <span class="m-left"><span class="meter small ${r.p == null ? "unknown" : ""}">${r.p == null ? "" : `<i class="${level}" style="width:${r.p.toFixed(1)}%"></i>`}</span>${routeLeft(r)}</span>
    <span class="m-when">${why ? `<span class="tag ${r.cls === 9 ? "off" : ""}">${esc(why)}</span>` : r.t ? `${r.w?.expires ? "Expires" : "Resets"} ${until(r.t)}` : ""}</span>`;
}

function renderModels(accounts) {
  const rs = routes(accounts);
  if (!rs.length) return '<p class="empty">No account here lists its models yet. Accounts read with an API key or an agent\'s sign-in list theirs.</p>';
  return `<p class="sheet-lead models-lead">${rs.length} models. Open one to see which account Ant would use for it, and why.</p>
  <div class="models" role="list">
    <div class="m-row m-head"><span>Model</span><span>Type</span><span>Best account</span><span>Left</span><span></span></div>
    ${rs.map((r) => `<details class="m-item" role="listitem" data-model="${esc(r.id)}" data-providers="${esc(JSON.stringify(r.via.map((v) => ({ provider: v.a.provider, name: v.a.label || v.a.name }))))}">
      <summary class="m-row">
        <span class="m-model"><code>${esc(r.id)}</code>${r.via.length > 1 ? `<span class="m-n">${r.via.length} accounts</span>` : ""}</span>
        <span class="m-type">${esc(MEDIA_LABEL[r.media] || r.media)}</span>
        ${routeRow(r.best)}
      </summary>
      <div class="m-route"></div>
      <div class="m-via">${r.via.map((v, i) => `<div class="m-row m-sub"><span class="m-rank">${i + 1}</span><span></span>${routeRow(v)}</div>`).join("")}</div>
    </details>`).join("")}
  </div>`;
}

// ---------- the timeline ----------

function events(accounts) {
  const out = [];
  for (const a of accounts) {
    if (a.status !== "ok") continue;
    const seen = new Set();
    for (const w of allWindows(a)) {
      const t = time(w.resetsAt);
      if (!t || w.aside) continue;
      out.push({ t, a, w, verb: w.expires ? "expires" : "resets" });
      seen.add(t);
    }
    // forecasts: when the recent pace runs something out
    for (const i of insightsOf(a)) {
      const t = time(i.at);
      if (i.outlook !== "runs_out" || !t) continue;
      const w = i.window ? allWindows(a).find((x) => x.name === i.window && (x.model || "") === (i.model || "")) : null;
      if (i.window && !w) continue;
      out.push({ t, a, w, balance: i.currency ? i : null, verb: "runs out", forecast: true });
    }
    const r = time(a.renewsAt), e = time(a.endsAt);
    if (r && !seen.has(r)) out.push({ t: r, a, verb: "renews" });
    else if (e && !r && !seen.has(e)) out.push({ t: e, a, verb: "ends" });
  }
  return out.sort((x, y) => x.t - y.t);
}

function bucketOf(t) {
  const now = new Date();
  const day = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diff = Math.round((day(new Date(t)) - day(now)) / 864e5);
  if (diff <= 0) return "Today";
  if (diff === 1) return "Tomorrow";
  if (diff < 7) return "This week";
  if (diff < 31) return "This month";
  return "Later";
}

function renderTimeline(accounts) {
  const evs = events(accounts);
  if (!evs.length) return '<p class="empty">Nothing resets, renews, expires or runs out in what is shown.</p>';
  const buckets = new Map();
  for (const ev of evs) {
    const b = bucketOf(ev.t);
    if (!buckets.has(b)) buckets.set(b, []);
    buckets.get(b).push(ev);
  }
  const timeFmt = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
  return `<div class="timeline">${[...buckets].map(([b, list]) => `
    <section class="tl-bucket"><h2>${b}</h2><div class="tl-list">${list.map((ev) => {
      const { a, w } = ev;
      const when = b === "Today" || b === "Tomorrow" ? timeFmt.format(ev.t) : dayFmt.format(ev.t);
      const p = w ? percentLeft(w) : null;
      let val = "";
      if (w && w.left != null) val = `<b>${num(w.left)}</b>${w.limit ? ` / ${num(w.limit)}` : ""} ${esc(w.unit || "")} left`;
      else if (p != null) val = p === 0 ? "<b>Used up</b>" : `<b>${Math.round(p)}%</b> left`;
      else if (ev.verb === "ends" && a.autoRenew === false) val = "auto-renew off";
      const name = esc(a.label || a.name);
      const subject = ev.balance ? `${esc(ev.balance.label || "Balance")}` : w ? esc(w.name) : esc(a.plan || a.modeName || "Plan");
      const what = `${subject} <span${ev.forecast ? ' class="soon"' : ""}>${ev.verb}${ev.forecast ? " at this pace" : ""}</span>`;
      if (ev.balance) val = `<b>${money(ev.balance.currency, ev.balance.perDay.toFixed(2))}</b>/day`;
      const tag = w && !ev.forecast && urgency(w, a) ? ` <span class="tag">${urgency(w, a)}</span>` : "";
      return `<div class="tl-row ${ev.forecast ? "forecast" : ""}" style="--hue:${HUES[a.provider] ?? 30}">
        <span class="t">${when}</span>
        ${mark(a.provider, a.name)}
        <span class="what">${what}${tag}<span class="sub">${name}${a.modeName ? " · " + esc(a.modeName) : ""}</span></span>
        <span class="val">${val}</span></div>`;
    }).join("")}</div></section>`).join("")}</div>`;
}

// ---------- the page ----------

// render draws the page; open rows stay open across the page's own refreshes.
function render() {
  const snap = state.snap;
  document.querySelectorAll(".views button").forEach((b) => b.setAttribute("aria-selected", String(b.dataset.view === state.view)));
  const refreshed = snap && time(snap.refreshedAt);
  $("updated").textContent = state.busy || snap?.refreshing ? "Reading…" : refreshed ? `Updated ${ago(refreshed)}` : "";
  $("refresh").classList.toggle("busy", state.busy || !!snap?.refreshing);
  const bell = $("notify");
  bell.hidden = !snap || snap.demo;
  bell.setAttribute("aria-pressed", String(!!snap?.notify));
  bell.setAttribute("aria-label", snap?.notify ? "Notifications on — turn off" : "Notifications off — turn on");
  bell.title = snap?.notify ? "Notifications on: Ant tells you before anything runs out or is lost" : "Notifications off";
  if (!snap || !refreshed) {
    $("summary").innerHTML = "";
    $("cards").innerHTML = '<div class="list"><div class="skeleton"></div><div class="skeleton"></div><div class="skeleton"></div></div>';
    return;
  }
  $("demo").hidden = !snap.demo;
  const all = [...(snap.accounts || [])];
  renderSummary(all);
  const q = norm(state.q).trim();
  const shown = all.map((a) => matches(a, q)).filter(Boolean).sort(SORTS[state.sort] || SORTS.soon);
  if (!shown.length) {
    $("cards").innerHTML = `<p class="empty">${!all.length ? "No accounts found on this Mac yet. Add one to start." : `Nothing matches “${esc(state.q)}”.`}</p>`;
    return;
  }
  if (state.view === "models") {
    $("cards").innerHTML = renderModels(shown);
    return;
  }
  if (state.view === "timeline") {
    $("cards").innerHTML = renderTimeline(shown);
    return;
  }
  $("cards").innerHTML = sections(shown).map((sec) => `
    <section class="section"><h2>${esc(sec.title)} <span class="n">${sec.items.length}</span></h2>
      <div class="list">${sec.items.map(renderAccount).join("")}</div>
    </section>`).join("");
}

// ---------- server ----------

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    cache: "no-store",
    headers: { "X-Ant": "1", ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `Ant answered ${res.status}`);
  return data;
}

// loadInsights fetches the history-based outlooks when the accounts were
// read again since the last time.
let insightsAt = "";
async function loadInsights() {
  const at = state.snap?.refreshedAt || "";
  if (!at || at === insightsAt) return;
  insightsAt = at;
  try { state.insights = await api("GET", "/api/insights"); render(); } catch { /* the cards stand without it */ }
}

async function load() {
  try { state.snap = await api("GET", "/api/quota"); } catch { /* keep the last snapshot */ }
  render();
  loadInsights();
  if (state.snap?.refreshing || !time(state.snap?.refreshedAt)) setTimeout(load, 1500);
}

async function refresh() {
  if (state.busy) return;
  state.busy = true;
  render();
  try { state.snap = await api("POST", "/api/quota/refresh"); } catch { /* keep the last snapshot */ }
  state.busy = false;
  render();
  loadInsights();
}

// ---------- the add / edit sheet ----------

const sheet = $("sheet");
let sheetStack = []; // steps, for Back

function provider(id) { return state.providers.find((p) => p.id === id); }

// asWay is mode m as read by one of its ways: that way's fields, sign-in
// and hint. A mode without ways is its own one way.
function asWay(m, wayID) {
  if (!m) return m;
  const w = m.ways.find((x) => x.id === wayID) || m.ways[0];
  return { ...m, way: w.id, wayName: m.ways.length > 1 ? w.name : "", fields: w.fields || [], signIn: w.signIn, browserDomain: w.browserDomain, connect: w.hint || m.connect };
}
function modeOf(a) { return asWay(provider(a.provider)?.modes.find((m) => m.id === a.mode), a.way); }

function showStep(title, html, back) {
  $("sheet-title").textContent = title;
  const body = $("sheet-body");
  for (const k of Object.keys(body.dataset)) delete body.dataset[k]; // each step sets its own
  body.innerHTML = html;
  $("sheet-back").hidden = !back;
  if (!sheet.open) sheet.showModal();
  const first = $("sheet-body").querySelector("input, select, button");
  if (first) first.focus();
}

function stepProviders() {
  sheetStack = [];
  const have = {};
  for (const a of state.snap?.accounts || []) if (a.status === "ok") have[a.provider] = (have[a.provider] || 0) + 1;
  showStep("Add account", `
    <p class="sheet-lead">Ant already finds the sign-ins of apps on this Mac. Add anything it didn't find — as many accounts per provider as you have.</p>
    <label class="search sheet-search">
      <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.2-4.2"/></svg>
      <input id="provider-search" type="search" placeholder="Provider, or image, video…" aria-label="Search providers" autocomplete="off" spellcheck="false">
    </label>
    <div class="pick">${state.providers.map((p) => `
      <button type="button" data-provider="${esc(p.id)}" data-hay="${esc(norm([p.name, p.id, ...(p.media || ["text"]).map((m) => MEDIA_LABEL[m] || m)].join(" ")))}" style="--hue:${HUES[p.id] ?? 30}">
        ${mark(p.id, p.name)}<span class="n">${esc(p.name)}</span>
        ${have[p.id] ? `<span class="kind">${have[p.id]} added</span>` : ""}
      </button>`).join("")}</div>
    <p class="empty" id="provider-none" hidden>No provider matches. Ant reads only the providers listed here.</p>`, false);
  const input = $("provider-search");
  input.addEventListener("input", () => {
    const words = norm(input.value).split(/\s+/).filter(Boolean);
    let shown = 0;
    for (const b of $("sheet-body").querySelectorAll("[data-provider]")) {
      b.hidden = !words.every((w) => b.dataset.hay.includes(w));
      if (!b.hidden) shown++;
    }
    $("provider-none").hidden = shown > 0;
  });
  input.focus();
}

function stepModes(p) {
  if (p.modes.length === 1) return stepWays(p, p.modes[0], false);
  sheetStack = [stepProviders];
  showStep(p.name, `
    <p class="sheet-lead">Each way of paying ${esc(p.name)} follows its own rule. Add the ones you have.</p>
    <div class="modes">${p.modes.map((m) => `
      <button type="button" data-mode="${esc(m.id)}">
        <span class="t"><span>${esc(m.name)}</span><span class="kind">${esc(KIND_LABEL[m.kind])}</span></span>
        <span class="r">${esc(m.rule)}</span>
      </button>`).join("")}</div>`, true);
  $("sheet-body").dataset.provider = p.id;
}

// how a way connects, in a word
function wayTag(w) {
  if (w.browserDomain) return (state.snap?.browserSites || []).includes(w.browserDomain) ? "Allowed" : "Asks first";
  if (w.signIn) return "Sign in";
  if (!(w.fields || []).length) return "Uses another app";
  if (w.fields.some((f) => f.id === "folder")) return "Folder";
  if (w.fields.some((f) => f.id.toLowerCase().includes("cookie"))) return "Paste";
  return "Key";
}

// stepWays lists the ways of reading a mode, easiest first; one is enough.
function stepWays(p, m, back) {
  if (m.ways.length === 1) return stepForm(p, asWay(m, m.ways[0].id), null, back);
  sheetStack = back ? [() => stepModes(p)] : [];
  const have = new Set((state.snap?.accounts || [])
    .filter((a) => a.provider === p.id && a.mode === m.id && a.status === "ok").map((a) => a.way || ""));
  showStep(`${p.name} · ${m.name}`, `
    ${back ? "" : `<div class="rule"><b>${esc(m.name)}.</b> ${esc(m.rule)}</div>`}
    <p class="sheet-lead">Ant can read this several ways, easiest first.</p>
    <div class="modes ways">${m.ways.map((w) => `
      <button type="button" data-way="${esc(w.id)}">
        <span class="t"><span>${esc(w.name)}</span><span class="kind">${have.has(w.id) ? ((w.fields || []).length || w.signIn ? "In use · add another" : "In use") : esc(wayTag(w))}</span></span>
        ${w.hint ? `<span class="r">${esc(w.hint)}</span>` : ""}
      </button>`).join("")}</div>`, back);
  const body = $("sheet-body");
  body.dataset.provider = p.id;
  body.dataset.mode = m.id;
}

function fieldHTML(f, view) {
  const id = `f-${f.id}`;
  const value = view?.values?.[f.id] ?? "";
  const isSet = view?.secretsSet?.includes(f.id);
  const label = `<label for="${id}">${esc(f.label)}${f.optional ? ' <span class="opt">optional</span>' : ""}</label>`;
  let input;
  if (f.type === "select") {
    input = `<select id="${id}" name="${esc(f.id)}"><option value="">Choose…</option>${(f.options || []).map((o) =>
      `<option value="${esc(o.value)}" ${o.value === value ? "selected" : ""}>${esc(o.label)}</option>`).join("")}</select>`;
  } else {
    const type = { secret: "password", date: "date", number: "text" }[f.type] || "text";
    const ph = isSet ? "Saved. Leave empty to keep it." : f.placeholder || "";
    input = `<input id="${id}" name="${esc(f.id)}" type="${type}" value="${f.type === "secret" ? "" : esc(value)}" placeholder="${esc(ph)}"
      autocomplete="off" spellcheck="false" ${f.type === "number" ? 'inputmode="decimal"' : ""}>`;
  }
  return `<div class="field">${label}${input}${f.help ? `<div class="help">${esc(f.help)}</div>` : ""}</div>`;
}

function stepForm(p, m, view, back) {
  const full = p.modes.find((x) => x.id === m.id);
  sheetStack = back ? [full.ways.length > 1 ? () => stepWays(p, full, p.modes.length > 1) : () => stepModes(p)] : [];
  const editing = !!view;
  const found = view?.origin === "found";
  // the rule was on the step before; say it here only when this step is the first
  const rule = back && !view ? (m.live ? "" : `<div class="rule">Ant can't read this from ${esc(p.name)}, so it shows what follows from the rule and your setup.</div>`)
    : `<div class="rule"><b>${esc(m.name)}${m.wayName ? ` · ${esc(m.wayName)}` : ""}.</b> ${esc(m.rule)}${m.live ? "" : " Ant can't read this from " + esc(p.name) + ", so it shows what follows from the rule and your setup."}</div>`;
  if (m.signIn) {
    const fields = editing && m.fields.length ? m.fields.map((f) => fieldHTML(f, view)).join("") : "";
    showStep(editing ? (view.label || p.name) : p.name, `${rule}<p class="sheet-lead">${esc(m.connect || "")}</p>${fields}
      <p class="form-error" id="form-error" role="alert" hidden></p>
      <div class="sheet-actions">${editing ? `<button type="button" class="btn danger" data-remove>Remove</button>` : ""}
      <div class="right">
        <button type="button" class="btn" data-signin>${editing ? "Sign in again" : `Sign in to ${esc(p.name)}`}</button>
        ${fields ? `<button type="submit" class="btn primary" data-save>Save</button>` : ""}
      </div></div>`, back);
    if (!fields) $("sheet-body").querySelector("[data-signin]").classList.add("primary");
    const body = $("sheet-body");
    body.dataset.provider = p.id;
    body.dataset.mode = m.id;
    body.dataset.way = m.way || "";
    body.dataset.id = view?.id || "";
    return;
  }
  if (m.browserDomain) return stepBrowser(p, m, rule, back);
  if (!m.fields.length) {
    showStep(p.name, `${rule}<div class="connect">${esc(m.connect || "")
      .replace(/codex login/g, "<code>codex login</code>")}</div>
      <div class="sheet-actions">${editing ? `<button type="button" class="btn danger" data-remove>${found ? "Hide" : "Remove"}</button>` : ""}
      <div class="right"><button type="button" class="btn primary" data-refresh-close>I've done this — refresh</button></div></div>`, back);
    return;
  }
  const labelField = `<div class="field"><label for="f-label">Name <span class="opt">optional</span></label>
    <input id="f-label" name="__label" type="text" maxlength="60" value="${esc(view?.label || "")}" placeholder="${esc(p.name)}" autocomplete="off"></div>`;
  const where = found ? `<p class="sheet-lead">Found in ${esc(view.source)}. Saving keeps it as yours, with what you change here.</p>`
    : m.wayName && m.connect ? `<p class="sheet-lead">${esc(m.connect)}</p>` : "";
  showStep(editing ? (view.label || p.name) : `${p.name} · ${m.name}`, `${where}${rule}
    ${m.fields.map((f) => fieldHTML(f, view)).join("")}${labelField}
    <p class="form-error" id="form-error" role="alert" hidden></p>
    <div class="sheet-actions">
      ${editing ? `<button type="button" class="btn danger" data-remove>${found ? "Hide" : "Remove"}</button>` : ""}
      <div class="right"><button type="button" class="btn" data-cancel>Cancel</button>
      <button type="submit" class="btn primary" data-save>${editing ? "Save" : "Add and read"}</button></div>
    </div>`, back);
  const body = $("sheet-body");
  body.dataset.provider = p.id;
  body.dataset.mode = m.id;
  body.dataset.way = m.way || "";
  body.dataset.id = view?.id || "";
}

async function openEdit(id) {
  try {
    const view = await api("GET", `/api/accounts/${encodeURIComponent(id)}`);
    const p = provider(view.provider);
    const m = asWay(p?.modes.find((x) => x.id === view.mode), view.way);
    if (!p || !m) return;
    stepForm(p, m, view, false);
  } catch (e) {
    alert(e.message);
  }
}

async function save() {
  const body = $("sheet-body");
  const p = provider(body.dataset.provider);
  const m = asWay(p?.modes.find((x) => x.id === body.dataset.mode), body.dataset.way);
  if (!m) return;
  const error = $("form-error");
  const values = {};
  for (const f of m.fields) values[f.id] = (body.querySelector(`[name="${f.id}"]`)?.value || "").trim();
  const editing = !!body.dataset.id;
  // check what the browser can before asking the server
  const missing = m.fields.find((f) => !f.optional && !values[f.id] && !(editing && f.type === "secret"));
  if (missing && !editing) {
    error.textContent = `${missing.label} is needed.`;
    error.hidden = false;
    body.querySelector(`[name="${missing.id}"]`)?.focus();
    return;
  }
  const button = body.querySelector("[data-save]");
  button.disabled = true;
  button.textContent = "Reading…";
  try {
    const label = body.querySelector('[name="__label"]')?.value || "";
    state.snap = editing
      ? await api("PUT", `/api/accounts/${encodeURIComponent(body.dataset.id)}`, { label, values })
      : await api("POST", "/api/accounts", { provider: p.id, mode: m.id, way: m.way, label, values });
    sheet.close();
    render();
  } catch (e) {
    error.textContent = e.message;
    error.hidden = false;
    button.disabled = false;
    button.textContent = editing ? "Save" : "Add and read";
  }
}

// stepBrowser asks before Ant reads a site's sign-in from the everyday
// browsers (ruling R6): nothing is read for a site the user didn't allow.
function stepBrowser(p, m, rule, back) {
  const d = m.browserDomain;
  const allowed = (state.snap?.browserSites || []).includes(d);
  const text = allowed
    ? `Ant reads your <b>${esc(d)}</b> sign-in from your browsers. If nothing shows up, sign in to ${esc(d)} in Chrome, Edge, Brave, Arc, Firefox or Safari, then refresh.`
    : `Ant will look for your <b>${esc(d)}</b> sign-in in Chrome, Edge, Brave, Arc, Firefox and Safari on this Mac, for ${esc(d)} only. Your Mac may ask once to let Ant read the browser's saved sign-ins.`;
  showStep(`${p.name} · ${m.wayName || "Your browser"}`, `${rule}<p class="sheet-lead">${text}</p>
    <p class="form-error" id="form-error" role="alert" hidden></p>
    <div class="sheet-actions">${allowed ? `<button type="button" class="btn danger" data-browser="off">Stop reading</button>` : ""}
    <div class="right">${allowed ? `<button type="button" class="btn primary" data-refresh-close>Refresh</button>`
      : `<button type="button" class="btn" data-cancel>Cancel</button><button type="button" class="btn primary" data-browser="on">Allow and read</button>`}</div></div>`, back);
  const body = $("sheet-body");
  body.dataset.provider = p.id;
  body.dataset.mode = m.id;
  body.dataset.way = m.way || "";
}

async function setBrowser(allow) {
  const body = $("sheet-body");
  const button = body.querySelector(`[data-browser="${allow ? "on" : "off"}"]`);
  button.disabled = true;
  button.textContent = allow ? "Reading…" : "Stopping…";
  try {
    state.snap = await api("POST", "/api/browser", { provider: body.dataset.provider, mode: body.dataset.mode, way: body.dataset.way, allow });
    sheet.close();
    render();
  } catch (e) {
    const error = $("form-error");
    error.textContent = e.message;
    error.hidden = false;
    button.disabled = false;
    button.textContent = allow ? "Allow and read" : "Stop reading";
  }
}

const CURRENCIES = ["USD", "CNY", "EUR", "GBP", "JPY", "HKD", "SGD", "KRW", "INR", "CAD", "AUD"];

// stepPrice asks what the user pays for an account.
function stepPrice(id) {
  const a = (state.snap?.accounts || []).find((x) => x.id === id);
  if (!a) return;
  const p = state.snap?.prices?.[id] || { amount: "", currency: (allBalances(a)[0]?.currency || "USD"), period: "month" };
  const currencies = CURRENCIES.includes(p.currency) ? CURRENCIES : [p.currency, ...CURRENCIES];
  sheetStack = [];
  showStep(`What do you pay for ${a.label || a.name}?`, `
    <p class="sheet-lead">Providers rarely say. Ant adds it up for you and mentions it before the plan renews.</p>
    <div class="price-row">
      <div class="field"><label for="f-amount">Price</label>
        <input id="f-amount" name="amount" type="text" inputmode="decimal" value="${esc(p.amount)}" placeholder="20" autocomplete="off"></div>
      <div class="field"><label for="f-currency">Currency</label>
        <select id="f-currency" name="currency">${currencies.map((c) => `<option ${c === p.currency ? "selected" : ""}>${esc(c)}</option>`).join("")}</select></div>
      <div class="field"><label for="f-period">Every</label>
        <select id="f-period" name="period"><option value="month" ${p.period === "month" ? "selected" : ""}>month</option><option value="year" ${p.period === "year" ? "selected" : ""}>year</option></select></div>
    </div>
    <p class="form-error" id="form-error" role="alert" hidden></p>
    <div class="sheet-actions">${state.snap?.prices?.[id] ? `<button type="button" class="btn danger" data-price-remove>Remove price</button>` : ""}
      <div class="right"><button type="button" class="btn" data-cancel>Cancel</button>
      <button type="button" class="btn primary" data-price-save>Save</button></div></div>`, false);
  $("sheet-body").dataset.priceId = id;
  $("f-amount").focus();
}

async function savePrice(remove) {
  const body = $("sheet-body");
  const id = body.dataset.priceId;
  try {
    state.snap = remove ? await api("DELETE", `/api/prices/${encodeURIComponent(id)}`)
      : await api("PUT", `/api/prices/${encodeURIComponent(id)}`, { amount: $("f-amount").value, currency: $("f-currency").value, period: $("f-period").value });
    sheet.close();
    render();
  } catch (e) {
    const error = $("form-error");
    error.textContent = e.message;
    error.hidden = false;
  }
}

// signIn signs in to a provider: a new account, or again (againID) the
// account that has ended.
async function signIn(providerID, modeID, wayID, againID) {
  const p = provider(providerID);
  const m = asWay(p.modes.find((x) => x.id === modeID), wayID);
  if (!sheet.open) {
    // started from a card: show the waiting state in the sheet
    stepForm(p, m, null, false);
  }
  const body = $("sheet-body");
  const error = $("form-error");
  if (error) error.hidden = true;
  const actions = body.querySelector(".sheet-actions");
  const before = actions.innerHTML;
  const viaBrowser = m.signIn?.browser;
  actions.innerHTML = `<span class="muted waiting">Waiting for you to sign in…</span>
    <div class="right"><button type="button" class="btn" data-signin-cancel>Cancel</button>
    ${viaBrowser ? "" : `<button type="button" class="btn primary" data-signin-finish>I'm signed in</button>`}</div>`;
  const lead = body.querySelector(".sheet-lead");
  if (lead) lead.textContent = viaBrowser
    ? `Your browser opened ${p.name}'s sign-in page. Sign in there and approve; Ant connects as soon as it does.`
    : `A Chrome window is open on ${p.name}. Sign in there as usual — Google sign-in works too. Ant notices within half a minute, or press I'm signed in.`;
  // a device sign-in has a code to confirm in the browser: show it here
  let watching = true;
  (async () => {
    while (watching) {
      await new Promise((r) => setTimeout(r, 800));
      try {
        const st = await api("GET", "/api/signin/status");
        if (!watching || !st.code || body.querySelector(".device-code")) continue;
        const box = document.createElement("div");
        box.className = "device-code";
        box.innerHTML = `<div class="dc-label">Confirm this code on ${esc(p.name)}'s page</div>
          <div class="dc-code">${esc(st.code)}</div>
          <div class="dc-help">Volcengine asks you to confirm the device asking for access. It's Ant on this computer.
          ${st.page ? `<a href="${esc(st.page)}" target="_blank" rel="noopener noreferrer">Open the page again ↗</a>` : ""}</div>`;
        actions.before(box);
      } catch { /* keep waiting */ }
    }
  })();
  try {
    state.snap = await api("POST", "/api/signin", { provider: providerID, mode: modeID, way: m.way, id: againID || undefined });
    watching = false;
    if (sheet.open) sheet.close();
    render();
  } catch (e) {
    watching = false;
    if (error) { error.textContent = e.message; error.hidden = false; }
    actions.innerHTML = before;
    body.querySelector(".device-code")?.remove();
  }
}

async function remove() {
  const id = $("sheet-body").dataset.id;
  if (!id) return;
  try {
    state.snap = await api("DELETE", `/api/accounts/${encodeURIComponent(id)}`);
    sheet.close();
    render();
  } catch (e) {
    alert(e.message);
  }
}

$("sheet-body").addEventListener("click", (e) => {
  const t = e.target.closest("button");
  if (!t) return;
  const body = $("sheet-body");
  if (t.dataset.provider) stepModes(provider(t.dataset.provider));
  else if (t.dataset.way !== undefined && !t.dataset.signinCard) {
    const p = provider(body.dataset.provider);
    stepForm(p, asWay(p.modes.find((m) => m.id === body.dataset.mode), t.dataset.way), null, true);
  } else if (t.dataset.mode) {
    const p = provider(body.dataset.provider);
    stepWays(p, p.modes.find((m) => m.id === t.dataset.mode), true);
  } else if (t.hasAttribute("data-cancel")) sheet.close();
  else if (t.hasAttribute("data-remove")) remove();
  else if (t.hasAttribute("data-refresh-close")) { sheet.close(); refresh(); }
  else if (t.dataset.browser) setBrowser(t.dataset.browser === "on");
  else if (t.hasAttribute("data-price-save")) savePrice(false);
  else if (t.hasAttribute("data-price-remove")) savePrice(true);
  else if (t.hasAttribute("data-signin")) signIn(body.dataset.provider, body.dataset.mode, body.dataset.way, body.dataset.id);
  else if (t.hasAttribute("data-signin-finish")) { t.disabled = true; t.textContent = "Reading your session…"; api("POST", "/api/signin/finish").catch(() => {}); }
  else if (t.hasAttribute("data-signin-cancel")) api("POST", "/api/signin/cancel").catch(() => {});
});
$("sheet-body").addEventListener("input", () => { const e = $("form-error"); if (e) e.hidden = true; });
$("sheet-form").addEventListener("submit", (e) => { e.preventDefault(); if ($("sheet-body").dataset.priceId) savePrice(false); else save(); });
$("sheet-close").addEventListener("click", () => sheet.close());
sheet.addEventListener("close", () => { if ($("sheet-body").querySelector("[data-signin-cancel]")) api("POST", "/api/signin/cancel").catch(() => {}); });
$("sheet-back").addEventListener("click", () => { const back = sheetStack.pop(); if (back) back(); });
sheet.addEventListener("click", (e) => { if (e.target === sheet) sheet.close(); });

// ---------- card menu, pause, route preview ----------

const isPaused = (a) => (state.snap?.paused || []).includes(a.id);
// a card can be paused when it serves models: pausing only changes Ant's choice of it
const routable = (a) => (a.models || []).length > 0;

// accountAction runs a row's action: pause or resume routing, remove.
async function accountAction(t) {
  try {
    if (t.dataset.pause) state.snap = await api("POST", "/api/pause", { id: t.dataset.pause, paused: t.dataset.paused === "1" });
    if (t.dataset.removeCard) {
      const a = (state.snap?.accounts || []).find((x) => x.id === t.dataset.removeCard);
      if (!confirm(`${a?.origin === "found" ? "Hide" : "Remove"} ${a?.label || a?.name || "this account"}?`)) return;
      state.snap = await api("DELETE", `/api/accounts/${encodeURIComponent(t.dataset.removeCard)}`);
      state.open.delete(t.dataset.removeCard);
    }
    render();
  } catch (e) {
    alert(e.message);
  }
}

const ROUTE_CLASS = { use_before_lost: "Use first", free: "Free", plan: "Plan", prepaid: "Prepaid", payg: "Pay as you go" };

// routePreview fills a model's row with what Ant would do, from the server.
async function routePreview(details) {
  const box = details.querySelector(".m-route");
  if (!box || box.dataset.loaded) return;
  box.dataset.loaded = "1";
  box.innerHTML = '<p class="muted">Working out the route…</p>';
  let p;
  try { p = await api("GET", `/api/route?model=${encodeURIComponent(details.dataset.model)}`); } catch (e) {
    box.innerHTML = `<p class="note problem">${esc(e.message)}</p>`;
    return;
  }
  const covered = new Set([...p.use, ...p.notUsed].map((c) => c.provider));
  const others = JSON.parse(details.dataset.providers || "[]").filter((x) => !covered.has(x.provider));
  if (!p.use.length && !p.notUsed.length) {
    box.innerHTML = '<p class="muted route-note">Ant\'s route preview doesn\'t cover these accounts yet. Below: what the accounts themselves show.</p>';
    return;
  }
  details.classList.add("previewed");
  // the row's "best account" is now the preview's first choice, not the page's guess
  const best = details.querySelector("summary .r-acct");
  if (best && p.use[0]) {
    const a = (state.snap?.accounts || []).find((x) => x.id === p.use[0].id || (x.keys || []).includes(p.use[0].id));
    best.style.setProperty("--hue", HUES[p.use[0].provider] ?? 30);
    best.innerHTML = `${mark(p.use[0].provider, p.use[0].name)}${esc(p.use[0].name)}${a?.modeName ? ` · ${esc(a.modeName)}` : ""}`;
  }
  const row = (c, i) => `<li class="r-choice">${i != null ? `<span class="r-rank">${i + 1}</span>` : ""}
    <span class="r-who"><b>${esc(c.name)}</b>${c.class ? ` <span class="tag ${c.class === "use_before_lost" || c.class === "free" ? "" : "off"}">${esc(ROUTE_CLASS[c.class] || c.class)}</span>` : ""}</span>
    <span class="r-why">${esc(c.reason)}${c.stale ? ` <span class="muted">· ${esc(c.stale)}</span>` : ""}</span></li>`;
  box.innerHTML = `
    <div class="r-title">What Ant would do</div>
    ${p.use.length ? `<ol class="r-list">${p.use.map((c, i) => row(c, i)).join("")}</ol>` : '<p class="note problem">No account can serve it right now.</p>'}
    ${p.notUsed.length ? `<div class="r-title">Not used</div><ul class="r-list">${p.notUsed.map((c) => row(c, null)).join("")}</ul>` : ""}
    ${others.length ? `<p class="muted route-note">Also listed by ${others.map((x) => esc(x.name)).join(", ")}, not in the preview yet.</p>` : ""}
    <p class="muted route-note">A preview: nothing is sent to any model.</p>`;
}

// ---------- wiring ----------

$("cards").addEventListener("click", (e) => {
  const act = e.target.closest("[data-pause], [data-remove-card]");
  if (act) return accountAction(act);
  const b = e.target.closest("[data-edit]");
  if (b) openEdit(b.dataset.edit);
  const price = e.target.closest("[data-price]");
  if (price) stepPrice(price.dataset.price);
  const again = e.target.closest("[data-signin-card]");
  if (again) signIn(again.dataset.signinCard, again.dataset.mode, again.dataset.way, again.dataset.id);
});
document.querySelector(".views").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-view]");
  if (!b) return;
  state.view = b.dataset.view;
  savePref("view");
  render();
});
$("search").addEventListener("input", () => { state.q = $("search").value; render(); });
document.addEventListener("keydown", (e) => {
  if (e.key === "/" && document.activeElement?.tagName !== "INPUT" && !sheet.open) { e.preventDefault(); $("search").focus(); }
});
$("refresh").addEventListener("click", refresh);
$("notify").addEventListener("click", async () => {
  try { state.snap = await api("POST", "/api/notify", { on: !state.snap?.notify }); } catch { /* unchanged */ }
  render();
});
$("add").addEventListener("click", () => { if (state.providers.length) stepProviders(); });
api("GET", "/api/providers").then((ps) => {
  for (const p of ps) for (const m of p.modes) {
    m.fields = m.fields || [];
    m.ways = (m.ways || []).length ? m.ways : [{ id: "", name: m.name, fields: m.fields, signIn: m.signIn, hint: m.connect }];
  }
  state.providers = ps;
  render();
}).catch(() => {});
setInterval(load, 60_000);
setInterval(render, 30_000);
load();

$("cards").addEventListener("toggle", (e) => {
  const d = e.target;
  if (d.matches?.("details.m-item") && d.open) routePreview(d);
  if (d.matches?.("details.acct")) d.open ? state.open.add(d.dataset.id) : state.open.delete(d.dataset.id);
}, true);
// a "use first" item opens its account
$("summary").addEventListener("click", (e) => {
  const b = e.target.closest("[data-goto]");
  if (!b) return;
  state.view = "cards";
  state.q = $("search").value = "";
  state.open.add(b.dataset.goto);
  render();
  const row = [...document.querySelectorAll("details.acct")].find((d) => d.dataset.id === b.dataset.goto);
  row?.scrollIntoView({ behavior: "smooth", block: "center" });
  row?.querySelector("summary")?.focus({ preventScroll: true });
});
