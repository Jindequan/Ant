import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import descriptor from '../../contracts/providers/provider-alpha.descriptor.json';
import snapshot from '../../contracts/providers/provider-alpha.verified-scopes.json';
import { createDescriptorCatalog, validateConfigDocument, windowsExecutableEqual, type ProviderDescriptorInput } from '../src/api/validate';
import type { Entity, WindowRuleLike } from '../src/api/config-helpers';
import type { VerifiedScopeInput } from '../src/api/config-catalog';

const corpus = JSON.parse(readFileSync('../docs/evidence/CODEX-T1-REWORK2-AUDIT/config-cases.json', 'utf8')) as { name: string; document: Entity }[];
const clone = (): Entity => structuredClone(corpus.find((c) => c.name === 'control-completed-verified-fact')!.document);
const entities = (d: Entity, field: string): Entity[] => d[field] as Entity[];
const find = (d: Entity, field: string, key: string, value: string): Entity => entities(d, field).find((e) => e[key] === value)!;
const resource = (d: Entity, id = 'res-alpha-usd-entitlement'): Entity => find(d, 'resources', 'resourceId', id);
const evidence = (d: Entity, id: string): Entity => find(d, 'evidence', 'evidenceId', id);
const cat = createDescriptorCatalog([descriptor]);
function rejected(d: Entity, path: string, key: string, catalog = cat): void {
 const result = validateConfigDocument(d, { catalog });
 expect(result.ok).toBe(false);
 if (result.ok) throw new Error('expected rejection');
 expect(result.issues).toContainEqual({ path, messageKey: `ant.config.${key}` });
 expect('compiled' in result).toBe(false);
}

describe('final compiled contract D1-D4', () => {
 for (const kind of ['rolling', 'fixed']) {
  it(`full compile resolves ${kind} through structural window identity`, () => {
   const d = clone(), r = resource(d, 'res-alpha-daily-token');
   const scopes = structuredClone(snapshot) as { scopes: VerifiedScopeInput[] };
   const fact = scopes.scopes[0]!.facts[0]!;
   if (kind === 'rolling') {
    fact.windowKind = 'rolling'; fact.windowDurationMillis = 60000;
    r.window = { kind: 'rolling', durationMillis: 60000 };
   } else {
    fact.windowTimeZone = 'Africa/Casablanca';
    r.window = { kind: 'fixed', period: 'day', timeZone: 'Africa/Casablanca' };
   }
   const catalog = createDescriptorCatalog([descriptor], scopes);
   const result = validateConfigDocument(d, { catalog });
   expect(result.ok).toBe(true);
   if (!result.ok) throw new Error(JSON.stringify(result.issues));
   expect(result.compiled?.resources[0]).toMatchObject({ scopeId: 'acct-demo-1', window: { kind } });
   r.window = kind === 'rolling' ? { kind, durationMillis: 60001 } : { kind, period: 'day', timeZone: 'Africa/Lagos' };
   rejected(d, '/resources/0/providerFactId', 'factUnresolved', catalog);
  });
 }
 it('outputs exact verified account scope and trusted resource types', () => {
  const result = validateConfigDocument(clone(), { catalog: cat });
  expect(result.ok).toBe(true);
  if (!result.ok) throw new Error(JSON.stringify(result.issues));
  expect(result.compiled?.resources).toHaveLength(3);
  expect(result.compiled?.resources.map((r) => [r.scopeId, r.kind, r.commercialRole])).toEqual([
   ['acct-demo-1', 'counter', 'limit'], ['acct-demo-1', 'counter', 'entitlement'], ['acct-demo-1', 'occupancy', 'limit'],
  ]);
 });
 for (const [id, field, value] of [['res-alpha-concurrency', 'kind', 'counter'], ['res-alpha-usd-entitlement', 'commercialRole', 'limit']]) {
  it(`rejects unoffered ${field} contradiction`, () => {
   const d = clone(); d.offers = []; d.pools = [];
   const r = resource(d, id); expect(r).toBeDefined(); r[field!] = value;
   const index = entities(d, 'resources').indexOf(r);
   rejected(d, `/resources/${index}/${field}`, 'providerFactMetadataMismatch');
  });
 }
 for (const field of ['bound', 'remaining']) {
  for (const value of [undefined, '-1', '00', '9223372036854775808', '0', '499']) {
   it(`${field} rejects ${String(value)}`, () => {
    const d = clone(), r = resource(d), ref = field === 'bound' ? 'capacityEvidenceRef' : 'initialStateEvidenceRef';
    const payload = evidence(d, String(r[ref])).payload as Entity;
    if (value === undefined) delete payload[field]; else payload[field] = value;
    rejected(d, `/resources/1/${ref}`, value === '0' || value === '499' ? 'evidenceConflictsVerifiedFact' : 'evidenceNotApplicable');
   });
  }
 }
 it('hard_stop bound requires an exact snapshot quantity too', () => {
  const d = clone(), r = resource(d, 'res-alpha-concurrency'), ev = evidence(d, String(r.capacityEvidenceRef));
  ev.kind = 'hard_stop';
  const result = validateConfigDocument(d, { catalog: cat });
  expect(result.ok).toBe(true);
  delete (ev.payload as Entity).bound;
  rejected(d, '/resources/2/capacityEvidenceRef', 'evidenceNotApplicable');
 });
 it('keeps conservative capacity separate from snapshot claims', () => {
  const d = clone(); resource(d).configuredCapacity = '250';
  const result = validateConfigDocument(d, { catalog: cat });
  expect(result.ok).toBe(true);
  if (!result.ok) throw new Error(JSON.stringify(result.issues));
  expect(result.compiled?.resources[1]).toMatchObject({ capacity: '250', initialRemaining: '500', scopeId: 'acct-demo-1' });
 });
 it('accepts actual zero snapshot and zero claims', () => {
  const scopes = structuredClone(snapshot) as { scopes: VerifiedScopeInput[] };
  const fact = scopes.scopes[0]!.facts.find((f) => f.factId === 'alpha-usd-grant-promo')!;
  fact.capacityBound = '0'; fact.initialRemaining = '0';
  const d = clone(), r = resource(d); r.configuredCapacity = '0';
  (evidence(d, String(r.capacityEvidenceRef)).payload as Entity).bound = '0';
  (evidence(d, String(r.initialStateEvidenceRef)).payload as Entity).remaining = '0';
  const result = validateConfigDocument(d, { catalog: createDescriptorCatalog([descriptor], scopes) });
  expect(result.ok).toBe(true);
  if (!result.ok) throw new Error(JSON.stringify(result.issues));
  expect(result.compiled?.resources[1]).toMatchObject({ capacity: '0', initialRemaining: '0' });
 });
 it('shared alias compiles shared billing scope rather than owner ID', () => {
  const d = clone(), r = resource(d, 'res-alpha-daily-token');
  r.providerFactId = 'alpha-shared-tokens'; r.window = { kind: 'fixed', period: 'month', timeZone: 'Asia/Singapore' }; r.configuredCapacity = '500000';
  (evidence(d, String(r.capacityEvidenceRef)).payload as Entity).bound = '500000';
  (evidence(d, String(r.initialStateEvidenceRef)).payload as Entity).remaining = '500000';
  const result = validateConfigDocument(d, { catalog: cat });
  expect(result.ok).toBe(true);
  if (!result.ok) throw new Error(JSON.stringify(result.issues));
  expect(result.compiled?.resources[0]).toMatchObject({ scopeId: 'billing-alpha-shared', physicalKey: 'phys-alpha:shared:tokens', ownerId: r.ownerId });
 });
 for (const [field, value] of [['resourceKind', undefined], ['commercialRole', 'occupancy']]) {
  it(`fails closed on invalid registration ${field}`, () => {
   const desc = structuredClone(descriptor) as ProviderDescriptorInput;
   Object.assign(desc.semantics![0]!, { [field!]: value });
   rejected(clone(), '/resources/0/semanticsRef', 'unregisteredResourceSemantics', createDescriptorCatalog([desc]));
  });
 }
 it('fails closed on duplicate matching registrations', () => {
  const desc = structuredClone(descriptor) as ProviderDescriptorInput;
  desc.semantics!.push(structuredClone(desc.semantics![0]!));
  rejected(clone(), '/resources/0/semanticsRef', 'unregisteredResourceSemantics', createDescriptorCatalog([desc]));
 });
 for (const role of ['limit', 'entitlement', 'unknown']) {
  it(`profile occupancy commercial role ${role}`, () => {
   const desc = structuredClone(descriptor) as ProviderDescriptorInput;
   desc.profiles![0]!.requiredDimensions.push({ metricId: 'metric.concurrency.request', role });
   const result = validateConfigDocument(clone(), { catalog: createDescriptorCatalog([desc]) });
   expect(result.ok).toBe(role === 'limit');
   if (role !== 'limit') { expect(result.ok ? [] : result.issues).toContainEqual({ path: '/offers/0/resourceRefs', messageKey: 'ant.config.incompleteRequiredBundle' }); }
  });
 }
});

describe('scope metadata snapshot guards', () => {
 it('inherits scope/provider without mutating caller snapshot, including binding alias', () => {
  const scopes = structuredClone(snapshot) as { scopes: VerifiedScopeInput[] };
  scopes.scopes[2]!.aliases!.push('binding-demo-1');
  const before = structuredClone(scopes), c = createDescriptorCatalog([descriptor], scopes);
  for (const alias of ['acct-demo-1', 'acct-demo-2', 'binding-demo-1']) {
   expect(c.resolveScopeFact(alias, 'alpha-shared-tokens')).toMatchObject({ scopeId: 'billing-alpha-shared', providerTypeId: 'provider-alpha' });
  }
  expect(c.resolveScopeFact('acct-demo-2', 'alpha-usd-grant-promo')).toMatchObject({ scopeId: 'acct-demo-2', providerTypeId: 'provider-alpha' });
  expect(scopes).toEqual(before);
 });
 it('accepts matching explicit child identities', () => {
  const scopes = structuredClone(snapshot) as { scopes: VerifiedScopeInput[] };
  for (const scope of scopes.scopes) for (const fact of scope.facts) {
   fact.scopeId = scope.scopeId; fact.providerTypeId = scope.providerTypeId;
  }
  expect(() => createDescriptorCatalog([descriptor], scopes)).not.toThrow();
 });
 for (const change of [
  (s: VerifiedScopeInput[]) => { s[0]!.scopeId = ''; },
  (s: VerifiedScopeInput[]) => { s[0]!.providerTypeId = ''; },
  (s: VerifiedScopeInput[]) => { s[0]!.providerTypeId = 'foreign'; },
  (s: VerifiedScopeInput[]) => { s[1]!.scopeId = s[0]!.scopeId; },
  (s: VerifiedScopeInput[]) => { s[0]!.kind = 'binding'; },
  (s: VerifiedScopeInput[]) => { s[2]!.facts[0]!.scopeId = 'acct-demo-1'; },
  (s: VerifiedScopeInput[]) => { s[0]!.facts[0]!.providerTypeId = 'foreign'; },
 ]) {
  it('rejects malformed parent/child identity', () => {
   const scopes = structuredClone(snapshot) as { scopes: VerifiedScopeInput[] }; change(scopes.scopes);
   expect(() => createDescriptorCatalog([descriptor], scopes)).toThrow();
  });
 }
});

describe('structural window identity', () => {
 const day: WindowRuleLike = { kind: 'fixed', period: 'day', timeZone: 'UTC', anchorISO: '', durationMillis: 0 };
 it('rolling is reflexive and distinguishes durations', () => {
  const rolling = { ...day, kind: 'rolling', durationMillis: 60000 };
  expect(windowsExecutableEqual(rolling, rolling)).toBe(true);
  expect(windowsExecutableEqual(rolling, { ...rolling, durationMillis: 60001 })).toBe(false);
 });
 it('preserves exact IANA names rather than sample offsets', () => {
  expect(windowsExecutableEqual({ ...day, timeZone: 'Africa/Casablanca' }, { ...day, timeZone: 'Africa/Lagos' })).toBe(false);
 });
 for (const zone of ['', 'UTC', 'Etc/UTC', 'Etc/GMT', 'GMT']) {
  it(`explicit UTC alias ${zone}`, () => expect(windowsExecutableEqual(day, { ...day, timeZone: zone })).toBe(true));
 }
 it('keeps phase/week/month and millisecond identities', () => {
  expect(windowsExecutableEqual(day, { ...day, anchorISO: '2026-01-02T00:00:00.0000Z' })).toBe(true);
  expect(windowsExecutableEqual(day, { ...day, anchorISO: '2026-01-02T00:00:00.001Z' })).toBe(false);
  const week = { ...day, period: 'week' }, month = { ...day, period: 'month' };
  expect(windowsExecutableEqual(week, { ...week, anchorISO: '2026-01-01T00:00:00Z' })).toBe(false);
  expect(windowsExecutableEqual(month, { ...month, anchorISO: '2026-01-15T00:00:00Z' })).toBe(false);
 });
 for (const rule of [{ ...day, timeZone: 'Local' }, { ...day, timeZone: 'bad/zone' }, { ...day, period: 'year' }, { ...day, anchorISO: '2026-01-01T00:00:00.0001Z' }, { ...day, kind: 'rolling', durationMillis: 0 }]) {
  it('invalid rules never compare equal to themselves', () => expect(windowsExecutableEqual(rule, rule)).toBe(false));
 }
});
