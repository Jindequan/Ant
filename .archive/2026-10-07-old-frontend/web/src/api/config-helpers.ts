/**
 * Shared primitives for the ConfigDocument validators — the Web mirror of
 * server/internal/config/document_helpers.go plus the R04 quantity codec
 * (domain.ParseUnits). The rule-by-rule reference-graph mirror lives in
 * config-semantics.ts; the boundary/catalog entry and wire guards in
 * validate.ts. Issue messageKeys equal the Go keys ("ant.config.*").
 */
import type { FieldIssue } from './types';

export type ConfigIssue = FieldIssue;

// ---------------------------------------------------------------------------
// Lenient accessors — the semantic pass must tolerate any JSON because it is
// also the standalone entry (mirrors Go struct zero-values after lenient decode).
// ---------------------------------------------------------------------------

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export type Entity = Record<string, unknown>;

export function obj(v: unknown): Entity {
  return isRecord(v) ? v : {};
}
export function arr(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}
export function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}
export function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

// ---------------------------------------------------------------------------
// Quantity codec (R04). Canonical decimal over int64, checked with BigInt —
// mirror of domain.ParseUnits: no floats, exponents, leading zeros, "-0",
// bare JSON numbers, null or overflow. Never routed through Number.
// ---------------------------------------------------------------------------

export const MAX_INT64 = (1n << 63n) - 1n;
export const MIN_INT64 = -(1n << 63n);

export function parseQuantity(value: unknown): bigint | null {
  if (typeof value !== 'string') return null; // bare numbers / null / bool
  let body = value;
  let neg = false;
  if (body.startsWith('-')) {
    neg = true;
    body = body.slice(1);
    if (body === '') return null;
  }
  if (body === '0') return neg ? null : 0n; // canonical zero is "0"; "-0" rejected
  if (body.startsWith('0')) return null; // leading zeros ("010", "00")
  if (!/^[0-9]+$/.test(body)) return null; // '.', 'e', '+', spaces, unicode digits
  const parsed = BigInt(value);
  if (parsed < MIN_INT64 || parsed > MAX_INT64) return null;
  return parsed;
}

// ---------------------------------------------------------------------------
// Primitive validity mirrors (document_helpers.go)
// ---------------------------------------------------------------------------

const ID_PATTERN = /^[A-Za-z0-9._:-]{1,64}$/;
const UNIT_PATTERN = /^[a-z0-9_]{1,32}$/;
const RFC3339_PATTERN = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;

export const validID = (id: string): boolean => ID_PATTERN.test(id);
export const validUnit = (u: string): boolean => UNIT_PATTERN.test(u);

// validSourceRef accepts provenance URIs (fixture://…, https://…), not ant
// IDs; control characters and emptiness are the only hard failures.
export function validSourceRef(s: string): boolean {
  if (s.length === 0 || s.length > 256) return false;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return false;
  }
  return true;
}

// Go uses time.LoadLocation (accepts "" as UTC plus the Go-only "Local");
// Intl accepts IANA names/aliases. Known divergence: Go's "Local" is
// rejected here; shared fixtures only use real IANA zones.
export function timeZoneValid(tz: string): boolean {
  if (tz === '') return true;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

export function rfc3339Valid(s: string): boolean {
  return RFC3339_PATTERN.test(s) && !Number.isNaN(Date.parse(s));
}

// S4 P2-4: anchors support millisecond precision only — anything finer is
// rejected at admission (mirror of Go checkWindow / WindowAt).
export function rfc3339SubMillisecond(s: string): boolean {
  const match = /\.(\d+)/.exec(s);
  const digits = match?.[1];
  if (digits === undefined) return false;
  return /[1-9]/.test(digits.slice(3));
}

// document_helpers.go secretKeyNames: recursive, case-insensitive matching so
// a provider extension can never smuggle credential material into a config
// revision. Evidence payloads are checked at their (string-map) top level by
// checkEvidence, like Go.
export const SECRET_KEY_NAMES = new Set([
  'value',
  'secret',
  'token',
  'password',
  'apikey',
  'api_key',
  'authorization',
  'credential',
]);

export function walkSecretKeys(path: string, node: unknown, issues: ConfigIssue[]): void {
  if (isRecord(node)) {
    for (const key of Object.keys(node)) {
      if (SECRET_KEY_NAMES.has(key.toLowerCase())) {
        issues.push({ path: `${path}/${key}`, messageKey: 'ant.config.secretMaterialForbidden' });
      }
      walkSecretKeys(`${path}/${key}`, node[key], issues);
    }
  } else if (Array.isArray(node)) {
    node.forEach((item, j) => walkSecretKeys(`${path}/${j}`, item, issues));
  }
}

// ---------------------------------------------------------------------------
// Cross-check utilities
// ---------------------------------------------------------------------------

export function canonicalWindowIdentity(w: Entity): string {
  const kind = str(w.kind);
  if (kind === 'fixed') {
    const phase = str(w.anchorISO) === '' ? 'natural' : canonicalAnchorPhase(w);
    return `fixed|${str(w.period)}|${str(w.timeZone)}|${phase}`;
  }
  if (kind === 'rolling') return `rolling|${num(w.durationMillis)}`;
  return kind;
}

// document_helpers.go canonicalAnchorPhase: renders the anchor's local
// wall-clock phase for the window period as HH:MM:SS.mmm (Go "15:04:05.000").
// Unparseable anchors fall back to the raw spelling — the window validator
// separately rejects unparseable anchors, so identity is only ever computed
// for documents that already carry that issue (same as Go). An unresolvable
// timezone falls back to UTC, also like Go.
const WEEKDAY_CODES: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };

export function canonicalAnchorPhase(w: Entity): string {
  const anchor = str(w.anchorISO);
  const ms = Date.parse(anchor);
  if (Number.isNaN(ms)) return `raw:${anchor}`;
  let timeZone = str(w.timeZone);
  if (timeZone === '' || !timeZoneValid(timeZone)) timeZone = 'UTC';
  const fmt = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 3,
    weekday: 'short',
  });
  const parts: Record<string, string> = {};
  for (const part of fmt.formatToParts(new Date(ms))) parts[part.type] = part.value;
  // Go truncates to milliseconds in the phase string; Date.parse already
  // carries whole milliseconds, so the three-digit group is exact.
  let phase = `${parts['hour']}:${parts['minute']}:${parts['second']}.${parts['fractionalSecond']}`;
  if (str(w.period) === 'week') phase += `|dow=${WEEKDAY_CODES[parts['weekday'] ?? ''] ?? 0}`;
  else if (str(w.period) === 'month') phase += `|dom=${Number(parts['day'])}`;
  return phase;
}

// ---------------------------------------------------------------------------
// Window identity mirrors resources.NormalizeWindowRuleIdentity: kind-specific
// structure and explicit UTC aliases, independent of date sampling.
// ---------------------------------------------------------------------------

/** The rule shape both config WindowDef and verified fact windows map onto (Go domain.WindowRule). */
export type WindowRuleLike = {
  kind: string;
  period: string;
  timeZone: string;
  anchorISO: string;
  durationMillis: number;
};

/** Minimal window fields of a verified fact (Go VerifiedResourceFact.Window*). */
export type WindowedFact = {
  windowKind: string;
  windowPeriod?: string;
  windowTimeZone?: string;
  windowAnchorISO?: string;
  windowDurationMillis?: number;
};

/** compile.go windowRuleOf: config WindowDef → domain.WindowRule. */
export function windowRuleOf(w: Entity): WindowRuleLike {
  return {
    kind: str(w.kind),
    period: str(w.period),
    timeZone: str(w.timeZone),
    anchorISO: str(w.anchorISO),
    durationMillis: num(w.durationMillis),
  };
}

/** VerifiedResourceFact.WindowRule: the fact's canonical window as a rule. */
export function factWindowRule(f: WindowedFact): WindowRuleLike {
  return {
    kind: f.windowKind,
    period: f.windowPeriod ?? '',
    timeZone: f.windowTimeZone ?? '',
    anchorISO: f.windowAnchorISO ?? '',
    durationMillis: f.windowDurationMillis ?? 0,
  };
}

interface WallParts {
  y: number;
  mo: number; // 1-12
  d: number;
  h: number;
  mi: number;
  s: number;
  ms: number;
  dow: number; // Go time.Weekday numbering: 0=Sunday … 6=Saturday
}

function wallParts(instantMs: number, timeZone: string): WallParts {
  const fmt = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 3,
    weekday: 'short',
  });
  const parts: Record<string, string> = {};
  for (const part of fmt.formatToParts(new Date(instantMs))) parts[part.type] = part.value;
  return {
    y: Number(parts['year']),
    mo: Number(parts['month']),
    d: Number(parts['day']),
    h: Number(parts['hour']),
    mi: Number(parts['minute']),
    s: Number(parts['second']),
    ms: Number(parts['fractionalSecond']),
    dow: WEEKDAY_CODES[parts['weekday'] ?? ''] ?? 0,
  };
}

function clockMillisOfDay(p: WallParts): number {
  return (p.h * 3600 + p.mi * 60 + p.s) * 1000 + p.ms;
}

interface AnchorSpec {
  clockMs: number; // ms of day of the anchor phase in the rule zone
  dow: number; // anchor weekday (week periods)
  dom: number; // anchor day of month (month periods)
}

/** resources/window.go parseAnchor: natural rules anchor Monday 00:00:00.000 local, day 1. */
function parseAnchorSpec(anchorISO: string, timeZone: string): AnchorSpec | null {
  if (anchorISO === '') return { clockMs: 0, dow: 1, dom: 1 };
  if (!rfc3339Valid(anchorISO) || rfc3339SubMillisecond(anchorISO)) return null;
  const ms = Date.parse(anchorISO);
  if (Number.isNaN(ms)) return null;
  const p = wallParts(ms, timeZone);
  return { clockMs: clockMillisOfDay(p), dow: p.dow, dom: p.d };
}

/** Comparable kind-specific identity; invalid rules never compare equal. */
export function normalizeWindowRuleIdentity(rule: WindowRuleLike): string | null {
  if (rule.kind === 'lifetime') return JSON.stringify(['lifetime']);
  if (rule.kind === 'rolling') {
    return Number.isInteger(rule.durationMillis) && rule.durationMillis > 0 && BigInt(rule.durationMillis) <= MAX_INT64
      ? JSON.stringify(['rolling', rule.durationMillis]) : null;
  }
  if (rule.kind !== 'fixed' || !['minute', 'hour', 'day', 'week', 'month'].includes(rule.period)) return null;
  const zone = ['', 'UTC', 'Etc/UTC', 'Etc/GMT', 'GMT'].includes(rule.timeZone) ? 'UTC' : rule.timeZone;
  if (zone === 'Local') return null;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone }).format(0);
    const anchor = parseAnchorSpec(rule.anchorISO, zone);
    if (anchor === null) return null;
    const phase = rule.period === 'minute' ? anchor.clockMs % 60000
      : rule.period === 'hour' ? anchor.clockMs % 3600000 : anchor.clockMs;
    const calendarDay = rule.period === 'week' ? anchor.dow : rule.period === 'month' ? anchor.dom : 0;
    return JSON.stringify(['fixed', rule.period, zone, phase, calendarDay]);
  } catch { return null; }
}

export function windowsExecutableEqual(a: WindowRuleLike, b: WindowRuleLike): boolean {
  const ia = normalizeWindowRuleIdentity(a), ib = normalizeWindowRuleIdentity(b);
  return ia !== null && ib !== null && ia === ib;
}

export function checkIDList(p: string, ids: unknown[], allow: Set<string> | null, issues: ConfigIssue[]): void {
  const seen = new Set<string>();
  ids.forEach((rawId, j) => {
    const id = str(rawId);
    if (!validID(id) || seen.has(id)) {
      issues.push({ path: `${p}/${j}`, messageKey: 'ant.config.invalidOrDuplicateRef' });
    }
    if (allow && !allow.has(id)) {
      issues.push({ path: `${p}/${j}`, messageKey: 'ant.config.unknownRef' });
    }
    seen.add(id);
  });
}

/** evidenceId → kind, first occurrence wins (mirrors Go evidenceKindByID linear scan). */
export function evidenceKindsByID(doc: Entity): Map<string, string> {
  const ids = new Map<string, string>();
  for (const rawEvidence of arr(doc.evidence)) {
    const e = obj(rawEvidence);
    if (!ids.has(str(e.evidenceId))) ids.set(str(e.evidenceId), str(e.kind));
  }
  return ids;
}

/** Set of an id field over an entity array (mirrors the Go "all entries, valid or not" maps). */
export function idSet(v: unknown, field: string): Set<string> {
  return new Set(arr(v).map((item) => str(obj(item)[field])));
}

export function resourceIndex(doc: Entity, id: string): number {
  return arr(doc.resources).findIndex((r) => str(obj(r).resourceId) === id);
}

export function accountTypeOf(doc: Entity, accountId: string): string {
  for (const raw of arr(doc.accounts)) {
    if (str(obj(raw).accountId) === accountId) return str(obj(raw).providerTypeId);
  }
  return '';
}

export const GRANT_PERMISSIONS = ['execute', 'read', 'cancel', 'manage', 'configure'];
export const GRANT_SCOPE_KINDS = ['namespace', 'binding', 'account', 'pool', 'offer'];
