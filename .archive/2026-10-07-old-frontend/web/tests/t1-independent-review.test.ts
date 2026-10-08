import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { createDescriptorCatalog, validateConfigDocument } from '../src/api/validate';
import descriptor from '../../contracts/providers/provider-alpha.descriptor.json';
import scopes from '../../contracts/providers/provider-alpha.verified-scopes.json';

const corpus = JSON.parse(readFileSync('../docs/evidence/CODEX-T1-REWORK2-AUDIT/config-cases.json', 'utf8'));
const control = () => structuredClone(corpus.find((c: {name: string}) => c.name === 'control-completed-verified-fact').document);
describe('independent T1 orthogonal profile role review', () => {
  for (const role of ['limit', 'entitlement', 'unknown']) it(role, () => {
    const d = structuredClone(descriptor);
    const p = d.profiles[0];
    if (!p) throw new Error("missing registered profile");
    p.requiredDimensions.push({metricId: 'metric.concurrency.request', role});
    const result = validateConfigDocument(control(), {catalog: createDescriptorCatalog([d], scopes)});
    if (role === 'limit') {
      expect(result.ok).toBe(true);
      if (!result.ok) throw new Error(JSON.stringify(result.issues));
      const r = result.compiled?.resources.find(r => r.resourceId === 'res-alpha-concurrency');
      expect(r).toMatchObject({kind:'occupancy', commercialRole:'limit', scopeId:'acct-demo-1', physicalKey:'phys-alpha:acct-demo-1:concurrency', capacity:'1', initialRemaining:'1'});
      console.log(JSON.stringify({case:role, compiled:r}));
    } else {
      expect(result.ok).toBe(false);
      if (result.ok) throw new Error('unsupported profile compiled');
      expect(result.issues).toContainEqual({path:'/offers/0/resourceRefs', messageKey:'ant.config.incompleteRequiredBundle'});
      console.log(JSON.stringify({case:role, issues:result.issues}));
    }
  });
});

describe('independent T1 evidence canonical strings', () => {
  for (const [id, field, path] of [
    ['ev-alpha-usd-bound','bound','/resources/1/capacityEvidenceRef'],
    ['ev-alpha-usd-initial','remaining','/resources/1/initialStateEvidenceRef'],
  ] as const) for (const value of [' 500', '500 ', '+500', '5e2', '500.0', 500, null]) it(`${field} ${JSON.stringify(value)}`, () => {
    const doc = control();
    doc.evidence.find((e: {evidenceId:string}) => e.evidenceId === id).payload[field] = value;
    const out = validateConfigDocument(doc, {catalog:createDescriptorCatalog([descriptor], scopes)});
    expect(out.ok).toBe(false);
    if (out.ok) throw new Error('malformed claim compiled');
    expect(out.issues.length).toBeGreaterThan(0);
    if (typeof value === 'string') expect(out.issues).toContainEqual({path, messageKey:'ant.config.evidenceNotApplicable'});
  });
});

describe('independent T1 calendar identities through complete entry', () => {
  for (const [name, period, zone, trusted, configured, accepted] of [
    ['offset instant conversion','day','Asia/Singapore','2026-01-01T00:00:00+08:00','2025-12-31T16:00:00Z',true],
    ['millisecond trailing zeros','hour','UTC','2026-01-01T11:23:45.120Z','2027-02-01T09:23:45.120000Z',true],
    ['different weekday','week','UTC','2026-01-05T00:00:00Z','2026-01-06T00:00:00Z',false],
    ['month clamp does not merge','month','UTC','2026-01-30T00:00:00Z','2026-01-31T00:00:00Z',false],
    ['sub millisecond','hour','UTC','2026-01-01T11:23:45.120Z','2027-02-01T09:23:45.120001Z',false],
  ] as const) it(name, () => {
    const snapshot = structuredClone(scopes);
    const f = snapshot.scopes.flatMap(s => s.facts).find(f => f.factId === 'alpha-daily-token-limit')!;
    Object.assign(f,{windowKind:'fixed', windowPeriod:period, windowTimeZone:zone, windowAnchorISO:trusted});
    const doc = control();
    const resource = doc.resources.find((r: {resourceId:string}) => r.resourceId === 'res-alpha-daily-token');
    resource.providerFactId = 'alpha-daily-token-limit';
    resource.window = {kind:'fixed',period,timeZone:zone,anchorISO:configured};
    const out = validateConfigDocument(doc, {catalog:createDescriptorCatalog([descriptor], snapshot)});
    expect(out.ok).toBe(accepted);
    if (accepted) {
      if (!out.ok) throw new Error(JSON.stringify(out.issues));
      const r = out.compiled?.resources.find(r => r.resourceId === 'res-alpha-daily-token');
      expect(r).toMatchObject({window:{kind:'fixed',period,timeZone:zone,anchorISO:trusted},scopeId:'acct-demo-1',capacity:'1000000',initialRemaining:'1000000'});
      console.log(JSON.stringify({case:name, compiled:r}));
    } else {
      if (out.ok) throw new Error('invalid window compiled');
      expect(out.issues.some(i => i.path === '/resources/0/window')).toBe(true);
    }
  });
});

it('positive int64 rolling duration exactly representable above safe integer', () => {
  const snapshot=structuredClone(scopes);
  const fact=snapshot.scopes.flatMap(s=>s.facts).find(f=>f.factId==='alpha-daily-token-limit')!;
  Object.assign(fact,{windowKind:'rolling',windowDurationMillis:9007199254740992});
  const doc=control(), resource=doc.resources.find((r: {resourceId:string})=>r.resourceId==='res-alpha-daily-token');
  resource.providerFactId='alpha-daily-token-limit';resource.window={kind:'rolling',durationMillis:9007199254740992};
  const out=validateConfigDocument(doc,{catalog:createDescriptorCatalog([descriptor],snapshot)});
  expect(out.ok).toBe(true);
  if (!out.ok) throw new Error(JSON.stringify(out.issues));
  expect(out.compiled?.resources.find(r=>r.resourceId==='res-alpha-daily-token')?.window).toMatchObject({kind:'rolling',durationMillis:9007199254740992});
});
