/**
 * Manifest-driven contract tests (K1/K6, R05): every verdict comes from
 * contracts/fixtures/api/manifest.json — the same files the Go codec/OpenAPI
 * tests consume — plus the shared audit probe cases (docs/evidence/
 * CODEX-T1-AUDIT/config-cases.json) for Go/Web parity. Layers per manifest:
 * schema (ajv over inlined openapi components + canonical quantity spelling),
 * codec (raw-text strict decode + BigInt quantity), guard (wire mirrors of
 * internal/api validate.go), semantic (reference-graph mirror of
 * internal/config validate_graph.go), web (validateConfigDocument overall).
 */
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import Ajv2020 from 'ajv/dist/2020';
import addFormats from 'ajv-formats';
import { describe, expect, it } from 'vitest';
import manifestJson from '../../contracts/fixtures/api/manifest.json';
import providerDescriptorJson from '../../contracts/providers/provider-alpha.descriptor.json';
import validConfigFixture from '../../contracts/fixtures/config-document-valid.json';
import configSchemaJson from '../../contracts/schema/config-document.schema.json';
import {
  canonicalJson,
  canonicalQuantitySchema,
  loadOpenApiSchemas,
  parseJsonStrict,
  strictDecodeConfigDocument,
} from '../scripts/contract-tools.mjs';
import { validateConfigDocumentSemantics } from '../src/api/config-semantics';
import {
  createDescriptorCatalog,
  factWindowRule,
  parseQuantity,
  validateConfigDocument,
  validateIfMatch,
  validateOperationResult,
  validateSubmitReply,
  windowsExecutableEqual,
  windowRuleOf,
  type CompiledConfigSummary,
  type ProviderDescriptorInput,
  type ValidationResult,
} from '../src/api/validate';
import type { ConfigDocument, ReorderPoolInput, SubmitReply } from '../src/api/types';

/** messageKeys of a validation result (empty when ok) — avoids union narrowing noise. */
function issueKeys(result: ValidationResult): string[] {
  return result.ok ? [] : result.issues.map((issue) => issue.messageKey);
}
/** Mutable clone of the valid fixture with loosely-typed entities for mutations. */
function cloneValidFixture(): { doc: ConfigDocument; entities: (field: string) => Array<Record<string, unknown>> } {
  const doc = JSON.parse(JSON.stringify(validConfigFixture)) as ConfigDocument;
  return {
    doc,
    entities: (field) => (doc as unknown as Record<string, unknown>)[field] as Array<Record<string, unknown>>,
  };
}

  const completePrepaid = (doc: Record<string, unknown>): Record<string, unknown> => {
    const resources = doc['resources'] as Array<Record<string, unknown>>;
    const prepaid = resources.find((r) => r['resourceId'] === 'res-alpha-usd-second');
    if (!prepaid) return doc;
    const evidence = doc['evidence'] as Array<Record<string, unknown>>;
    const bound = structuredClone(evidence.find((e) => e['evidenceId'] === 'ev-alpha-usd-bound')!);
    bound['evidenceId'] = 'ev-second-bound';
    (bound['payload'] as Record<string, unknown>)['bound'] = '1000';
    evidence.push(bound);
    prepaid['capacityEvidenceRef'] = 'ev-second-bound';
    (evidence.find((e) => e['evidenceId'] === 'ev-second-initial')!['payload'] as Record<string, unknown>)['remaining'] = '1000';
    return doc;
  };
interface ManifestEntry {
  file: string;
  expect?: 'accept' | 'reject';
  schema?: 'accept' | 'reject';
  semantic?: 'accept' | 'reject' | 'accept-after-lenient-decode';
  strictCodec?: 'accept' | 'reject';
  issueKey?: string;
  role?: string;
  schemaRef?: string;
  rule?: string;
  codec?: 'accept' | 'reject';
  guard?: 'accept' | 'reject';
  note?: string;
}
interface ManifestFile {
  version: string;
  configFixtures: ManifestEntry[];
  apiFixtures: ManifestEntry[];
}
const manifest = manifestJson as unknown as ManifestFile;

const here = path.dirname(fileURLToPath(import.meta.url));
const fixturesRoot = path.resolve(here, '../../contracts/fixtures');
const probeCasesPath = path.resolve(here, '../../docs/evidence/CODEX-T1-AUDIT/config-cases.json');
const reworkCasesPath = path.resolve(here, '../../docs/evidence/CODEX-T1-REWORK-AUDIT/config-cases.json');
const rework3CasesPath = path.resolve(here, '../../docs/evidence/CODEX-T1-REWORK2-AUDIT/config-cases.json');

function readFixtureText(rel: string): string {
  return readFileSync(path.join(fixturesRoot, rel), 'utf8');
}
function readFixtureJson(rel: string): unknown {
  return JSON.parse(readFixtureText(rel));
}

// --- schema layer (ajv over inlined openapi components + canonical quantity) ---
const ajv = new Ajv2020({ allErrors: true });
addFormats(ajv);
const openApiSchemas = loadOpenApiSchemas();
const compiledSchemas = new Map<string, ReturnType<typeof ajv.compile>>();
function schemaAccepts(schemaRef: string, value: unknown): boolean {
  let validate = compiledSchemas.get(schemaRef);
  if (!validate) {
    const schema = openApiSchemas[schemaRef];
    if (!schema) throw new Error(`openapi component schema missing: ${schemaRef}`);
    validate = ajv.compile(schema);
    compiledSchemas.set(schemaRef, validate);
  }
  return validate(value) === true;
}
const quantitySchemaAccepts = (() => {
  const validate = ajv.compile(canonicalQuantitySchema());
  return (value: unknown): boolean => validate(value) === true;
})();
const configSchemaAccepts = (() => {
  const validate = ajv.compile(configSchemaJson as unknown as object);
  return (value: unknown): boolean => validate(value) === true;
})();

/** Layer assertion counters — pinned in the totals test below (numbers feed the rework report). */
const layers = {
  schema: 0,
  codec: 0,
  guard: 0,
  semantic: 0,
  web: 0,
  probe: 0,
  descriptor: 0,
  rework2: 0,
  rework3: 0,
  catalogIdentity: 0,
};

// Shared catalog instance for the {catalog} boundary entry (descriptor +
// embedded verified-scope snapshot, mirror of localtest.New).
const descriptorCatalog = createDescriptorCatalog([providerDescriptorJson as unknown as ProviderDescriptorInput]);

// ---------------------------------------------------------------------------
// Config fixtures: manifest.configFixtures (schema / semantic / issueKey / web)
// ---------------------------------------------------------------------------

// Q1 D-C1-v3 migration (rework 3): commercial identity is RESOLUTION-based
// (config-graph checkCommercialIdentity over the verified-scope directory) and
// runs only in the catalog phase — the catalog-free structural pass mints no
// identity verdicts any more. Both legacy duplicate fixtures still fail the
// structural pass, but for ADJACENT evidence-integrity defects (a shared
// initial-state ref / a dangling selector subject), not identity; the IDENTITY
// keys are therefore pinned at the {catalog} entry:
//  - duplicate-commercial-fact: two identical daily limits resolve to the SAME
//    physical key → duplicateCommercialFact (key unchanged);
//  - two-lone-buckets: the two fact-less lifetime entitlements match TWO
//    verified buckets → ambiguousFact. The round-2 "default slot duplicate"
//    verdict is obsolete (Q1 默认槽撤销): declare providerFactId explicitly.
// Go keeps the legacy classification inside ValidateStructural, so Go's
// structural pass still pins duplicateCommercialFact for both fixtures; the
// web deliberately drops that implementation (rework-3 instruction) and the
// catalog entry — the single authority both sides feed — rejects both.
const IDENTITY_MIGRATION = new Map<string, string>([
  ['config-document-invalid-duplicate-commercial-fact.json', 'ant.config.duplicateCommercialFact'],
  ['config-document-invalid-two-lone-buckets.json', 'ant.config.ambiguousFact'],
]);

describe('config fixtures (manifest.configFixtures, per-layer verdicts)', () => {
  const configFixtures = manifest.configFixtures;
  it('manifest declares the frozen 13-fixture config matrix', () => {
    expect(configFixtures).toHaveLength(13);
  });

  for (const entry of configFixtures) {
    it(`${entry.file} [schema=${entry.schema} semantic=${entry.semantic ?? '-'} expect=${entry.expect}]`, () => {
      const parsed = readFixtureJson(entry.file);
      // schema layer (executable constraints only)
      expect(configSchemaAccepts(parsed)).toBe(entry.schema === 'accept');
      layers.schema += 1;
      // semantic layer (reference graph; runs without the schema gate)
      const migratedIssueKey = IDENTITY_MIGRATION.get(entry.file);
      const semanticOk = entry.semantic === undefined || entry.semantic.startsWith('accept');
      const issues = validateConfigDocumentSemantics(parsed);
      expect(issues.length === 0).toBe(semanticOk);
      if (!semanticOk && migratedIssueKey === undefined) {
        // The migrated identity fixtures still reject structurally, but via
        // adjacent defects; their identity keys are pinned at the catalog
        // entry below (see IDENTITY_MIGRATION).
        expect(entry.issueKey).toBeTruthy();
        expect(issues.some((issue) => issue.messageKey === entry.issueKey)).toBe(true);
      }
      layers.semantic += 1;
      // strict codec layer where the manifest declares it (unknown fields)
      if (entry.strictCodec !== undefined) {
        expect(strictDecodeConfigDocument(readFixtureText(entry.file))).toBe(entry.strictCodec);
        layers.codec += 1;
      }
      // web overall (schema + semantic; identity fixtures via the {catalog}
      // entry, which is where resolution identity lives after Q1)
      if (migratedIssueKey !== undefined) {
        const migrated = validateConfigDocument(parsed, { catalog: descriptorCatalog });
        expect(migrated.ok).toBe(false);
        expect(issueKeys(migrated)).toContain(migratedIssueKey);
        layers.catalogIdentity += 1;
      } else {
        expect(validateConfigDocument(parsed).ok).toBe(entry.expect === 'accept');
      }
      layers.web += 1;
    });
  }

  // The unknown-field fixture also documents the lenient-decode asymmetry the
  // Go test pins: shape layers reject it, the reference graph accepts it.
  it('unknown-field fixture: schema and strict codec reject, semantics accept the lenient object', () => {
    const parsed = readFixtureJson('config-document-invalid-unknown-field.json');
    expect(configSchemaAccepts(parsed)).toBe(false);
    expect(validateConfigDocumentSemantics(parsed)).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// Shared audit probe parity (15 cases, same entry the Go probe runs)
// ---------------------------------------------------------------------------
describe('codex audit config parity (15 shared cases)', () => {
  const cases = JSON.parse(readFileSync(probeCasesPath, 'utf8')) as {
    name: string;
    document: unknown;
    expected: boolean;
  }[];
  it('probe file still carries the frozen 15-case matrix', () => {
    expect(cases.map((c) => c.name)).toEqual([
      'valid-baseline',
      'dangling-offer-binding',
      'dangling-resource-owner',
      'dangling-group-scope',
      'group-unit-mismatch',
      'dangling-selector-evidence',
      'dangling-evidence-subject',
      'quantity-leading-zero',
      'quantity-negative',
      'quantity-negative-zero',
      'quantity-overflow',
      'nested-secret',
      'case-secret',
      'missing-required-arrays',
      'valid-gift-and-prepaid-buckets',
    ]);
    expect(cases.map((c) => c.expected)).toEqual([
      true, false, false, false, false, false, false, false, false, false, false, false, false, false, true,
    ]);
  });

  // S1 adaptation (input parity with the Go probe, server/tests/integration/
  // codex_regression_test.go): the Go probe feeds DecodeStrict +
  // ValidateStructural the factId-adapted document, because Go's structural
  // pass still carries the round-2 classification identity. The web single-arg
  // entry keeps the SAME adapted input (行为保持) — on the web the identity
  // layer moved to the catalog phase, so the adaptation is verdict-neutral
  // here; the gift+prepaid coexistence is re-proven under resolution identity
  // by the rework-2/3 blocks below.
  function adaptLegacyDocument(name: string, document: unknown): unknown {
    if (name !== 'valid-gift-and-prepaid-buckets') return document;
    const doc = JSON.parse(JSON.stringify(document)) as { resources: Array<Record<string, unknown>> };
    for (const resource of doc.resources) {
      if (resource['resourceId'] === 'res-alpha-usd-entitlement') {
        resource['providerFactId'] = 'alpha-usd-grant-promo';
      }
      if (resource['resourceId'] === 'res-alpha-usd-prepaid') {
        resource['providerFactId'] = 'alpha-usd-prepaid';
      }
    }
    return doc;
  }

  for (const c of cases) {
    it(c.name, () => {
      // One-arg entry = schema + structural semantics, no catalog: the exact
      // mirror of the Go probe's DecodeStrict + ValidateStructural.
      expect(validateConfigDocument(adaptLegacyDocument(c.name, c.document)).ok).toBe(c.expected);
      layers.probe += 1;
    });
  }
});

// ---------------------------------------------------------------------------
// Rework-2 full boundary parity: 16 shared cases through the {catalog} entry
// (mirror of Go TestRework2FullBoundaryConfigCases: root schema → structural
// semantics → registered catalog)
// ---------------------------------------------------------------------------
describe('codex rework-2 full boundary parity (16 shared cases, {catalog} entry)', () => {
  const cases = JSON.parse(readFileSync(reworkCasesPath, 'utf8')) as {
    name: string;
    document: unknown;
    expected: boolean;
  }[];
  const catalog = createDescriptorCatalog([providerDescriptorJson as unknown as ProviderDescriptorInput]);

  it('rework corpus still carries the frozen 16-case matrix', () => {
    expect(cases.map((c) => c.name)).toEqual([
      'full-boundary-valid',
      'required-account-enabled-missing',
      'required-account-enabled-null',
      'required-resource-semantics-missing',
      'required-resource-quantum-missing',
      'required-resource-metered-missing',
      'dangling-grant-scope',
      'initial-state-wrong-resource',
      'capacity-wrong-owner-and-metric',
      'binding-owned-extension-unknown',
      'catalog-unsupported-domain',
      'catalog-fact-metric-mismatch',
      'same-bucket-cloned-with-different-order',
      'valid-catalog-two-facts',
      'same-group-incompatible-quantum',
      'same-limit-equivalent-anchor-spelling',
    ]);
    expect(cases.map((c) => c.expected)).toEqual([
      true, false, false, false, false, false, false, false, false, false, false, false, false, true, false, false,
    ]);
  });

  // S4 P2-3 adaptation (sanctioned input completion, business assertion
  // unchanged): full-boundary-valid's single entitlement resource must
  // declare its providerFactId explicitly. Under Q1 the fact-less resource
  // would resolve dimensionally against TWO verified lifetime buckets and be
  // pinned as ambiguousFact (see the un-adapted check below) — declaring the
  // fact keeps the legal case legal.
  const caseByName = new Map(cases.map((c) => [c.name, c.document] as const));
  const withFacts = (name: string, facts: string[]): Record<string, unknown> => {
    const doc = JSON.parse(JSON.stringify(caseByName.get(name))) as Record<string, unknown>;
    let idx = 0;
    for (const r of doc['resources'] as Array<Record<string, unknown>>) {
      if (r['commercialRole'] === 'entitlement' && idx < facts.length) {
        r['providerFactId'] = facts[idx]!;
        idx += 1;
      }
    }
    return name === 'valid-catalog-two-facts' ? completePrepaid(doc) : doc;
  };

  for (const c of cases) {
    it(c.name, () => {
      const doc = c.name === 'full-boundary-valid'
        ? withFacts('full-boundary-valid', ['alpha-usd-grant-promo'])
        : c.name === 'valid-catalog-two-facts' ? withFacts(c.name, ['alpha-usd-grant-promo', 'alpha-usd-prepaid']) : c.document;
      expect(validateConfigDocument(doc, { catalog }).ok).toBe(c.expected);
      layers.rework2 += 1;
    });
  }

  it('Q1: un-adapted full-boundary-valid is a draft with ambiguousFact', () => {
    // Q1 D-C1-v3 supersedes the round-2 providerFactRequired pin: a fact-less
    // resource resolves DIMENSIONALLY, and the fixture's lifetime usd class
    // matches TWO verified buckets (grant-promo + prepaid) in scope
    // acct-demo-1 → ambiguousFact; the document must declare providerFactId
    // explicitly instead of leaning on the deleted default slot.
    const original = caseByName.get('full-boundary-valid');
    const result = validateConfigDocument(original, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.ambiguousFact');
    layers.rework2 += 1;
  });

  it('S4 P1-1: selector evidence certifying another group is inapplicable', () => {
    const doc = withFacts('valid-catalog-two-facts', ['alpha-usd-grant-promo', 'alpha-usd-prepaid']);
    const selector = JSON.parse(JSON.stringify(
      (doc['evidence'] as Array<Record<string, unknown>>).find(
        (e) => e['evidenceId'] === 'ev-alpha-usd-selector',
      ),
    )) as Record<string, unknown>;
    selector['evidenceId'] = 'ev-alpha-usd-selector-2';
    selector['subjectRef'] = 'eg-alpha-usd'; // certifies the ORIGINAL group only
    (doc['evidence'] as Array<Record<string, unknown>>).push(selector);
    const groups = doc['entitlementGroups'] as Array<Record<string, unknown>>;
    groups[0]!['selectorEvidenceId'] = 'ev-alpha-usd-selector-2';
    groups[0]!['groupId'] = 'eg-alpha-usd-renamed';
    for (const r of doc['resources'] as Array<Record<string, unknown>>) {
      if (r['commercialRole'] === 'entitlement') r['entitlementGroupId'] = 'eg-alpha-usd-renamed';
    }
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.evidenceNotApplicable');
    layers.rework2 += 1;
  });

  // Q1 note: the round-2 "S4 P2-1 cross-account same factId is a clone" test
  // is OBSOLETE — identity is now the RESOLVED physical key, and the same
  // factId on two independent scopes resolves to two distinct physical keys
  // (legal coexistence). The clone guarantee is re-pinned scope-based in the
  // rework-3 block (shared-scope aliasing = ONE ledger).

  it('S4 P2-2: capacity evidence without metricId is inapplicable', () => {
    const doc = withFacts('valid-catalog-two-facts', ['alpha-usd-grant-promo', 'alpha-usd-prepaid']);
    for (const e of doc['evidence'] as Array<Record<string, unknown>>) {
      if (e['kind'] === 'bound') e['payload'] = { bound: '500', enforcement: 'fixture' };
    }
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.evidenceNotApplicable');
    layers.rework2 += 1;
  });

  it('S4 P2-4: sub-millisecond anchor rejected at admission', () => {
    const doc = withFacts('full-boundary-valid', ['alpha-usd-grant-promo']);
    const resources = doc['resources'] as Array<Record<string, unknown>>;
    (resources[0]!['window'] as Record<string, unknown>)['anchorISO'] = '2026-01-01T09:00:00.123456Z';
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.anchorPrecisionUnsupported');
    layers.rework2 += 1;
  });
});

// ---------------------------------------------------------------------------
// Rework-2 clone variants ({catalog} entry) under Q1 D-C1-v3: identity is the
// RESOLVED physical key, so the clone mutations reuse the SAME factId as the
// first resource (round 2 asserted clones through distinct-factId groups and
// classification identity — both deleted with the default slot).
// ---------------------------------------------------------------------------
describe('codex rework-2 clone variants ({catalog} entry)', () => {
  const cases = JSON.parse(readFileSync(reworkCasesPath, 'utf8')) as {
    name: string;
    document: unknown;
    expected: boolean;
  }[];
  const catalog = createDescriptorCatalog([providerDescriptorJson as unknown as ProviderDescriptorInput]);
  const caseByName = new Map(cases.map((c) => [c.name, c.document]));
  const looseClone = (name: string): Record<string, unknown> => {
    const doc = JSON.parse(JSON.stringify(caseByName.get(name))) as Record<string, unknown>;
    return name === 'valid-catalog-two-facts' ? completePrepaid(doc) : doc;
  };

  it('clone-explicit-fact-different-group-still-rejected', () => {
    const doc = looseClone('valid-catalog-two-facts');
    const resources = doc['resources'] as Array<Record<string, unknown>>;
    // second resource points at the SAME verified fact as the first but moves
    // to its own group: groups organize consumption, they never mint identity
    resources[3]!['providerFactId'] = 'alpha-usd-grant-promo';
    resources[3]!['entitlementGroupId'] = 'eg-alpha-usd-2';
    (doc['entitlementGroups'] as unknown[]).push({
      groupId: 'eg-alpha-usd-2',
      scopeKind: 'account',
      scopeId: 'acct-demo-1',
      metricId: 'metric.usd.cents',
      unit: 'usd_cents',
      order: [{ resourceId: 'res-alpha-usd-second', providerOrder: 1 }],
    });
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false); // one resolved physical key declared twice must still be a clone
    expect(issueKeys(result)).toContain('ant.config.duplicateCommercialFact');
    layers.rework2 += 1;
  });

  it('clone-explicit-fact-different-resourceid-still-rejected', () => {
    const doc = looseClone('valid-catalog-two-facts');
    const resources = doc['resources'] as Array<Record<string, unknown>>;
    resources[3]!['providerFactId'] = 'alpha-usd-grant-promo'; // same fact as resources[1]
    resources[3]!['resourceId'] = 'res-alpha-usd-third';
    // fix references so ONLY the identity rule can reject it
    const order = ((doc['entitlementGroups'] as Array<Record<string, unknown>>)[0]![
      'order'
    ] ?? []) as Array<Record<string, unknown>>;
    order[1]!['resourceId'] = 'res-alpha-usd-third';
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false); // the same fact under a different resourceId must still be a clone
    expect(issueKeys(result)).toContain('ant.config.duplicateCommercialFact');
    layers.rework2 += 1;
  });

  it('explicit-fact-reorganization-stays-legal', () => {
    // identity does not depend on groups: explicitly verified resources stay
    // legal when their consumption organization changes.
    const doc = looseClone('valid-catalog-two-facts');
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(true);
    layers.rework2 += 1;
  });

  it('limit-anchor-equivalent-spelling-single-ledger', () => {
    // Q1: equivalent EXECUTION spellings collapse to one ledger — each
    // spelling must still RESOLVE to the verified fact (executable equality
    // with its natural-midnight window), so the pair uses explicit midnight
    // anchors on different dates. (Round 2 spelled the phase 09:00; under Q1
    // that phase does not match the natural-midnight fact at all →
    // factUnresolved — covered separately by the rework-3 window variants.)
    const doc = looseClone('full-boundary-valid');
    const resources = doc['resources'] as Array<Record<string, unknown>>;
    for (const r of resources) {
      if (r['commercialRole'] === 'entitlement') r['providerFactId'] = 'alpha-usd-grant-promo';
    }
    const daily = resources[0] as Record<string, unknown>;
    (daily['window'] as Record<string, unknown>)['anchorISO'] = '2026-01-01T00:00:00+08:00';
    const clone = JSON.parse(JSON.stringify(daily)) as Record<string, unknown>;
    clone['resourceId'] = 'res-alpha-daily-token-2';
    (clone['window'] as Record<string, unknown>)['anchorISO'] = '2027-06-15T00:00:00+08:00'; // equivalent phase
    resources.push(clone);
    // extend binding refs so ONLY identity can reject
    const bindings = doc['bindings'] as Array<Record<string, unknown>>;
    (bindings[0]!['resourceRefs'] as unknown[]).push('res-alpha-daily-token-2');
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.duplicateCommercialFact');
    layers.rework2 += 1;
  });
});

// ---------------------------------------------------------------------------
// Rework-3 full boundary parity (Q1 D-C1-v3, mirror of Go
// TestRework3FullBoundaryConfigCases + TestRework3ScopeIdentity +
// TestRework3WindowIdentityVariants + TestRework3CapacityConstraints +
// TestRework3BundleCoverage + the structural identity controls): the FULL
// boundary against the shared round-3 corpus ({catalog} entry) plus Go-mirror
// variants with COMPLETE reference surgery (negative cases break only the
// targeted invariant).
// ---------------------------------------------------------------------------
describe('codex rework-3 full boundary parity (17 shared cases, {catalog} entry)', () => {
  const cases = JSON.parse(readFileSync(rework3CasesPath, 'utf8')) as {
    name: string;
    document: unknown;
    expected: boolean;
    expectedPathPrefix?: string;
  }[];
  const catalog = createDescriptorCatalog([providerDescriptorJson as unknown as ProviderDescriptorInput]);

  it('rework corpus still carries the frozen 17-case matrix', () => {
    expect(cases.map((c) => c.name)).toEqual([
      'control-completed-verified-fact',
      'limit-no-anchor-vs-explicit-natural-anchor',
      'limit-hour-anchor-irrelevant-hour',
      'limit-timezone-equivalent-alias',
      'limit-changing-unit-cannot-mint-capacity',
      'unregistered-dimension-default-bucket',
      'default-bucket-cloned-by-window-only',
      'configured-capacity-exceeds-evidence-bound',
      'malformed-quantity-in-bound-evidence',
      'known-usd-fact-relabelled-eur',
      'verified-fact-relabelled-quantum',
      'evidence-wrong-precision-window-and-revision',
      'unregistered-resource-semantics',
      'unregistered-capability-profile',
      'offer-omits-required-billing-and-concurrency',
      'resource-extension-in-wrong-context',
      'nested-schema-error-must-locate-field',
    ]);
    expect(cases.map((c) => c.expected)).toEqual([
      true, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false,
      false,
    ]);
  });

  for (const c of cases) {
    it(c.name, () => {
      const result = validateConfigDocument(c.document, { catalog });
      expect(result.ok).toBe(c.expected);
      // C4 parity: schema errors locate the offending field (ajv instancePath).
      if (c.expectedPathPrefix && !result.ok) {
        expect(result.issues.some((issue) => issue.path.startsWith(c.expectedPathPrefix!))).toBe(true);
      }
      layers.rework3 += 1;
    });
  }

  const cloneCorpus = (name: string): Record<string, unknown> =>
    JSON.parse(JSON.stringify(cases.find((c) => c.name === name)!.document)) as Record<string, unknown>;
  const findResource = (doc: Record<string, unknown>, id: string): Record<string, unknown> =>
    (doc['resources'] as Array<Record<string, unknown>>).find((r) => r['resourceId'] === id)!;
  const findEvidence = (doc: Record<string, unknown>, id: string): Record<string, unknown> =>
    (doc['evidence'] as Array<Record<string, unknown>>).find((e) => e['evidenceId'] === id)!;

  it('Q1: the legal control compiles to the consumable execution input', () => {
    const result = validateConfigDocument(cloneCorpus('control-completed-verified-fact'), { catalog });
    expect(result.ok).toBe(true);
    const compiled = (result as { compiled?: CompiledConfigSummary }).compiled;
    expect(compiled).toBeDefined();
    expect(compiled!.resources.map((cr) => [cr.resourceId, cr.physicalKey])).toEqual([
      ['res-alpha-daily-token', 'phys-alpha:acct-demo-1:daily-token'],
      ['res-alpha-usd-entitlement', 'phys-alpha:acct-demo-1:usd-grant-promo'],
      ['res-alpha-concurrency', 'phys-alpha:acct-demo-1:concurrency'],
    ]);
    expect(compiled!.resources.map((cr) => cr.capacity)).toEqual(['1000000', '500', '1']);
    expect(compiled!.offers.map((o) => o.physicalKeys.length)).toEqual([3, 3]);
    layers.rework3 += 1;
  });

  // Q1 scope identity (mirror of Go TestRework3ScopeIdentity): independent
  // billing scopes each carry their own grant (distinct physical keys, both
  // legal); a shared scope aliasing accounts is ONE ledger (same physical key
  // = clone).
  it('scope identity: independent scopes hold the same factId as distinct ledgers', () => {
    const f1 = catalog.resolveScopeFact('acct-demo-1', 'alpha-usd-grant-promo');
    const f2 = catalog.resolveScopeFact('acct-demo-2', 'alpha-usd-grant-promo');
    expect(f1?.physicalKey).toBe('phys-alpha:acct-demo-1:usd-grant-promo');
    expect(f2?.physicalKey).toBe('phys-alpha:acct-demo-2:usd-grant-promo');
    expect(f1!.physicalKey).not.toBe(f2!.physicalKey);
    layers.rework3 += 1;
  });

  it('scope identity: shared-scope aliasing is ONE ledger across accounts', () => {
    const doc = cloneCorpus('control-completed-verified-fact');
    const resources = doc['resources'] as Array<Record<string, unknown>>;
    const monthly = JSON.parse(JSON.stringify(findResource(doc, 'res-alpha-daily-token'))) as Record<string, unknown>;
    monthly['resourceId'] = 'res-alpha-monthly-shared';
    monthly['window'] = { kind: 'fixed', period: 'month', timeZone: 'Asia/Singapore' };
    delete monthly['providerFactId'];
    resources.push(monthly);
    (doc['accounts'] as unknown[]).push({
      accountId: 'acct-demo-2',
      providerTypeId: 'provider-alpha',
      displayName: '第二账户（fixture）',
      enabled: true,
    });
    const monthly2 = JSON.parse(JSON.stringify(monthly)) as Record<string, unknown>;
    monthly2['resourceId'] = 'res-alpha-monthly-shared-2';
    resources.push(monthly2);
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.duplicateCommercialFact');
    layers.rework3 += 1;
  });

  // Structural window identity controls mirror the accepted Go normalization.
  it('window equivalence probe: aliases merge, phases decide, rolling self identity', () => {
    const daySGT = { kind: 'fixed', period: 'day', timeZone: 'Asia/Singapore', anchorISO: '', durationMillis: 0 };
    // natural midnight = explicit midnight (same local calendar phase)
    expect(windowsExecutableEqual(daySGT, { ...daySGT, anchorISO: '2026-01-01T00:00:00+08:00' })).toBe(true);
    // hour phase takes minute+second only — the anchor hour is irrelevant
    expect(
      windowsExecutableEqual(
        { kind: 'fixed', period: 'hour', timeZone: 'Asia/Singapore', anchorISO: '2026-01-01T01:15:00+08:00', durationMillis: 0 },
        { kind: 'fixed', period: 'hour', timeZone: 'Asia/Singapore', anchorISO: '2026-01-01T02:15:00+08:00', durationMillis: 0 },
      ),
    ).toBe(true);
    // UTC / Etc/UTC are one reset rule (zone aliases merge)
    expect(
      windowsExecutableEqual(
        { ...daySGT, timeZone: 'UTC' },
        { ...daySGT, timeZone: 'Etc/UTC' },
      ),
    ).toBe(true);
    // materially different zones/phases never merge
    expect(windowsExecutableEqual(daySGT, { ...daySGT, timeZone: 'UTC' })).toBe(false);
    expect(
      windowsExecutableEqual(
        daySGT,
        { ...daySGT, period: 'hour', anchorISO: '2026-01-01T00:15:00+08:00' },
      ),
    ).toBe(false);
    // rolling identity uses positive duration without fixed WindowAt bounds
    const rolling = { kind: 'rolling', period: '', timeZone: 'UTC', anchorISO: '', durationMillis: 60000 };
    expect(windowsExecutableEqual(rolling, rolling)).toBe(true);
    layers.rework3 += 1;
  });

  // Window identity variants (mirror of Go TestRework3WindowIdentityVariants):
  // equivalent window spellings collapse to one limit; materially different
  // windows are unresolved drafts; unit changes cannot mint capacity. Every
  // negative clone gets COMPLETE reference surgery so only the targeted
  // invariant can reject.
  const cloneDailyLimit = (mutate?: (clone: Record<string, unknown>) => void): Record<string, unknown> => {
    const doc = cloneCorpus('control-completed-verified-fact');
    const resources = doc['resources'] as Array<Record<string, unknown>>;
    const clone = JSON.parse(JSON.stringify(findResource(doc, 'res-alpha-daily-token'))) as Record<string, unknown>;
    clone['resourceId'] = 'res-alpha-daily-token-clone';
    mutate?.(clone);
    resources.push(clone);
    // complete every reference the clone needs (binding + offer + evidence
    // subjects) so ONLY the identity invariant can reject
    const bindings = doc['bindings'] as Array<Record<string, unknown>>;
    (bindings[0]!['resourceRefs'] as unknown[]).push(clone['resourceId']);
    const offers = doc['offers'] as Array<Record<string, unknown>>;
    (offers[0]!['resourceRefs'] as unknown[]).push(clone['resourceId']);
    const evidence = doc['evidence'] as Array<Record<string, unknown>>;
    const initial = JSON.parse(JSON.stringify(findEvidence(doc, 'ev-alpha-daily-initial'))) as Record<string, unknown>;
    initial['evidenceId'] = 'ev-alpha-daily-initial-clone';
    initial['subjectRef'] = clone['resourceId'];
    evidence.push(initial);
    clone['initialStateEvidenceRef'] = 'ev-alpha-daily-initial-clone';
    const cap = JSON.parse(JSON.stringify(findEvidence(doc, 'ev-alpha-daily-bound'))) as Record<string, unknown>;
    cap['evidenceId'] = 'ev-alpha-daily-bound-clone';
    evidence.push(cap);
    clone['capacityEvidenceRef'] = 'ev-alpha-daily-bound-clone';
    return doc;
  };

  it('window identity: natural vs explicit midnight is ONE ledger', () => {
    const doc = cloneDailyLimit((clone) => {
      clone['window'] = { kind: 'fixed', period: 'day', timeZone: 'Asia/Singapore', anchorISO: '2026-01-01T00:00:00+08:00' };
    });
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.duplicateCommercialFact');
    layers.rework3 += 1;
  });

  it('window identity: hour phase ignores the anchor hour (different phase → unresolved)', () => {
    const doc = cloneDailyLimit((clone) => {
      clone['window'] = { kind: 'fixed', period: 'hour', timeZone: 'Asia/Singapore', anchorISO: '2026-01-01T00:15:00+08:00' };
    });
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.factUnresolved'); // NOT a second ledger
    layers.rework3 += 1;
  });

  it('window identity: zone alias cannot mint (UTC day ≠ the SGT day fact)', () => {
    const doc = cloneDailyLimit((clone) => {
      clone['window'] = { kind: 'fixed', period: 'day', timeZone: 'Etc/UTC' };
    });
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.factUnresolved');
    layers.rework3 += 1;
  });

  it('window identity: unit change cannot mint', () => {
    const doc = cloneDailyLimit((clone) => {
      clone['unit'] = 'kilobyte';
    });
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.factUnresolved');
    layers.rework3 += 1;
  });

  it('window identity: identical dimensional copy resolves to the same key → clone', () => {
    const doc = cloneDailyLimit();
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.duplicateCommercialFact');
    layers.rework3 += 1;
  });

  // Capacity constraints (mirror of Go TestRework3CapacityConstraints, Q1/C2):
  // the verified bound caps the configuration; conservative lower values
  // compile; malformed evidence quantities are rejected.
  it('capacity: conservative lower value compiles with the configured capacity', () => {
    const doc = cloneCorpus('control-completed-verified-fact');
    findResource(doc, 'res-alpha-daily-token')['configuredCapacity'] = '400000';
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(true);
    const compiled = (result as { compiled?: CompiledConfigSummary }).compiled;
    const daily = compiled!.resources.find((cr) => cr.resourceId === 'res-alpha-daily-token');
    expect(daily!.capacity).toBe('400000'); // compiled capacity = configured value (decimal string)
    expect(daily!.physicalKey).toBe('phys-alpha:acct-demo-1:daily-token');
    layers.rework3 += 1;
  });

  it('capacity: above the verified bound is rejected and does not compile', () => {
    const doc = cloneCorpus('control-completed-verified-fact');
    findResource(doc, 'res-alpha-daily-token')['configuredCapacity'] = '1000000000';
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.capacityExceedsVerifiedBound');
    expect((result as { compiled?: CompiledConfigSummary }).compiled).toBeUndefined();
    layers.rework3 += 1;
  });

  it('capacity: malformed evidence bound is inapplicable', () => {
    const doc = cloneCorpus('control-completed-verified-fact');
    findEvidence(doc, 'ev-alpha-daily-bound')['payload'] = {
      metricId: 'metric.tokens.total',
      bound: 'not-a-number',
      enforcement: 'plan-hard-cap (fixture)',
    };
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.evidenceNotApplicable');
    layers.rework3 += 1;
  });

  // Bundle coverage (mirror of Go TestRework3BundleCoverage, Q1/C3): removing
  // the billing or concurrency dimension from an offer breaks the profile
  // requirement — the deleted limits cannot be silently re-added.
  it('bundle: dropping billing+concurrency from the offer breaks the profile requirement', () => {
    const doc = cloneCorpus('control-completed-verified-fact');
    (doc['offers'] as Array<Record<string, unknown>>)[0]!['resourceRefs'] = ['res-alpha-daily-token'];
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(issueKeys(result)).toContain('ant.config.incompleteRequiredBundle');
    layers.rework3 += 1;
  });

  // Context schema (Q1/C3): resource extensions compile against
  // resourceSchema — the generic whitelist is not a cross-object passport.
  it('context schema: planName on a resource is rejected by resourceSchema', () => {
    const doc = cloneCorpus('control-completed-verified-fact');
    findResource(doc, 'res-alpha-usd-entitlement')['providerExtensions'] = {
      planName: 'field-allowed-only-by-account-schema',
    };
    const result = validateConfigDocument(doc, { catalog });
    expect(result.ok).toBe(false);
    expect(
      result.ok
        ? false
        : result.issues.some(
            (i) => i.path === '/resources/1/providerExtensions' && i.messageKey === 'ant.config.extensionSchemaViolation',
          ),
    ).toBe(true);
    layers.rework3 += 1;
  });
});

// ---------------------------------------------------------------------------
// R04 quantities: canonical spelling (schema) + exact range (BigInt codec)
// ---------------------------------------------------------------------------
describe('quantity wire contract (R04: schema spelling + BigInt range)', () => {
  // Per-layer expectations: [value, codecVerdict, schemaVerdict]. The schema
  // pins canonical SPELLING only — 19-digit beyond-int64 spellings pass the
  // pattern ($defs/quantity: "range is checked exactly (BigInt/int64) by the
  // validators") and must be rejected by the codec instead.
  const wireCases: Array<[unknown, 'accept' | 'reject', 'accept' | 'reject']> = [
    ['42', 'accept', 'accept'],
    ['-5', 'accept', 'accept'],
    ['0', 'accept', 'accept'],
    ['9223372036854775807', 'accept', 'accept'],
    ['-9223372036854775808', 'accept', 'accept'],
    ['010', 'reject', 'reject'],
    ['-0', 'reject', 'reject'],
    ['00', 'reject', 'reject'],
    ['4.2', 'reject', 'reject'],
    ['1e3', 'reject', 'reject'],
    ['+5', 'reject', 'reject'],
    ['', 'reject', 'reject'],
    ['9223372036854775808', 'reject', 'accept'],
    ['-9223372036854775809', 'reject', 'accept'],
    ['99999999999999999999', 'reject', 'reject'],
    [null, 'reject', 'reject'],
    [42, 'reject', 'reject'],
    [4.2, 'reject', 'reject'],
  ];
  it('covers the frozen 18-case quantity matrix at both layers', () => {
    expect(wireCases).toHaveLength(18);
  });

  for (const [value, codecExpected, schemaExpected] of wireCases) {
    it(`quantity ${JSON.stringify(value)} → codec ${codecExpected}, schema ${schemaExpected}`, () => {
      // codec layer: BigInt, never Number (mirror of domain.ParseUnits)
      const parsed = parseQuantity(value);
      expect(parsed !== null).toBe(codecExpected === 'accept');
      if (codecExpected === 'accept') expect(typeof parsed).toBe('bigint');
      // schema layer: canonical spelling
      expect(quantitySchemaAccepts(value)).toBe(schemaExpected === 'accept');
      layers.codec += 1;
      layers.schema += 1;
    });
  }

  const capacityCases: Array<[string, 'accept' | 'reject', string]> = [
    ['0', 'accept', ''],
    ['-1', 'reject', 'ant.config.quantityInvalid'],
    ['9223372036854775808', 'reject', 'ant.config.quantityInvalid'],
    ['-9223372036854775808', 'reject', 'ant.config.quantityInvalid'],
  ];
  for (const [capacity, expected, issueKey] of capacityCases) {
    it(`configuredCapacity ${capacity} → ${expected}`, () => {
      const { doc, entities } = cloneValidFixture();
      entities('resources')[0]!['configuredCapacity'] = capacity;
      const result = validateConfigDocument(doc);
      expect(result.ok).toBe(expected === 'accept');
      if (issueKey !== '') expect(issueKeys(result)).toContain(issueKey);
      layers.web += 1;
    });
  }
});

// ---------------------------------------------------------------------------
// API fixtures: manifest.apiFixtures (schemaRef/schema/codec/guard per rule)
// ---------------------------------------------------------------------------
describe('api fixtures (manifest.apiFixtures, per-layer verdicts)', () => {
  const apiFixtures = manifest.apiFixtures;
  it('manifest declares the frozen 21-fixture api matrix', () => {
    expect(apiFixtures).toHaveLength(21);
  });

  for (const entry of apiFixtures) {
    const raw = readFixtureJson(path.join('api', entry.file));
    if (entry.role === 'request' || entry.role === 'response') {
      it(`${entry.file} [${entry.schemaRef} schema=${entry.schema}]`, () => {
        expect(entry.schemaRef).toBeTruthy();
        if (entry.file === 'operation.events.json') {
          // array of OperationEvent (manifest note)
          const events = raw as unknown[];
          expect(events.length).toBeGreaterThan(0);
          for (const event of events) expect(schemaAccepts(entry.schemaRef!, event)).toBe(true);
        } else {
          expect(schemaAccepts(entry.schemaRef!, raw)).toBe(entry.schema === 'accept');
        }
        layers.schema += 1;
      });
      continue;
    }
    if (entry.role !== 'rejects') {
      throw new Error(`unhandled manifest role "${entry.role}" for ${entry.file} — extend the test driver`);
    }
    it(`${entry.file} [rule=${entry.rule} schema=${entry.schema} guard=${entry.guard ?? '-'} codec=${entry.codec ?? '-'}]`, () => {
      runRejectRule(entry, raw);
    });
  }
});

function runRejectRule(entry: ManifestEntry, raw: unknown): void {
  switch (entry.rule) {
    case 'int64-range':
    case 'canonical-spelling': {
      // The fixture is an AdjustmentInput-style body; the quantity under test is `delta`.
      const delta = (raw as Record<string, unknown>)['delta'];
      expect(quantitySchemaAccepts(delta)).toBe(entry.schema === 'accept');
      layers.schema += 1;
      // Codec mirror of Go TestRejectionFixtures/quantity-overflow and the
      // entry note: both quantity rejections fail strict Units decoding. The
      // overflow manifest entry omits the codec key; reject unless explicitly
      // declared otherwise.
      expect(parseQuantity(delta) === null).toBe(entry.codec !== 'accept');
      layers.codec += 1;
      break;
    }
    case 'union-state': {
      expect(schemaAccepts('SubmitReply', raw)).toBe(entry.schema === 'accept');
      layers.schema += 1;
      expect(validateSubmitReply(raw).ok).toBe(entry.guard === 'accept');
      layers.guard += 1;
      break;
    }
    case 'enum': {
      expect(schemaAccepts('OperationResult', raw)).toBe(entry.schema === 'accept');
      layers.schema += 1;
      expect(validateOperationResult(raw).ok).toBe(entry.guard === 'accept');
      layers.guard += 1;
      break;
    }
    case 'receipt-hash': {
      const entries = raw as unknown[];
      expect(entries).toHaveLength(2);
      const first = entries[0] as ReorderPoolInput;
      const second = entries[1] as ReorderPoolInput;
      expect(schemaAccepts('ReorderPoolInput', entries[0])).toBe(entry.schema === 'accept');
      expect(schemaAccepts('ReorderPoolInput', entries[1])).toBe(entry.schema === 'accept');
      layers.schema += 2;
      expect(first.commandId).toBe(second.commandId);
      // same commandId, different bodies → different receipt hashes (canonical JSON stand-in)
      expect(canonicalJson(entries[0])).not.toBe(canonicalJson(entries[1]));
      break;
    }
    case 'version-guard': {
      expect(schemaAccepts('ReorderPoolInput', raw)).toBe(entry.schema === 'accept');
      layers.schema += 1;
      const body = raw as ReorderPoolInput;
      expect(validateIfMatch('4', body.expectedPoolVersion).ok).toBe(false); // header claims 4, body 3
      expect(validateIfMatch('3', body.expectedPoolVersion).ok).toBe(true);
      for (const invalid of ['W/"3"', 'W/3', '"3', '3"', '03', '+3', '3, "4"', '*']) {
        expect(validateIfMatch(invalid, body.expectedPoolVersion).ok).toBe(false);
      }
      layers.guard += 1;
      break;
    }
    case 'duplicate-key': {
      const text = readFixtureText(path.join('api', entry.file));
      // JSON.parse-based checks cannot see duplicate keys; only strict token decoding can
      expect(() => JSON.parse(text)).not.toThrow();
      expect(() => parseJsonStrict(text)).toThrow(/duplicate object key/);
      layers.codec += 1;
      break;
    }
    default:
      throw new Error(`unhandled manifest rule "${entry.rule}" for ${entry.file} — extend the test driver`);
  }
}

// ---------------------------------------------------------------------------
// R12: the local/test provider descriptor drives Go/Web validation alike
// ---------------------------------------------------------------------------
describe('provider-alpha descriptor (R12: restricted schemas + entitlement facts)', () => {
  const descriptor = providerDescriptorJson as unknown as ProviderDescriptorInput & {
    extensionSchema: object;
    entitlementFacts: Array<{ factId: string; metricId: string; unit: string }>;
  };

  const extensionValidator = (() => {
    const validate = ajv.compile(descriptor.extensionSchema);
    return (value: unknown): boolean => validate(value) === true;
  })();

  it('accepts a legal restricted extension', () => {
    expect(extensionValidator({ planName: 'fixture-only', region: 'test' })).toBe(true);
    expect(
      extensionValidator({ planName: 'x', region: 'test', endpointClass: 'chat', rateLimitTier: 'standard' }),
    ).toBe(true);
    layers.descriptor += 2;
  });

  it('rejects unknown fields, enum violations, wrong types, empty and nested-unknown shapes', () => {
    expect(extensionValidator({ unknownThing: true })).toBe(false);
    expect(extensionValidator({ planName: 'x', region: 'prod' })).toBe(false); // enum violation
    expect(extensionValidator({ planName: 42 })).toBe(false); // wrong type
    expect(extensionValidator({})).toBe(false); // minProperties 1
    expect(extensionValidator({ planName: 'x', region: { nested: 'object' } })).toBe(false); // nested unknown shape
    expect(extensionValidator({ planName: 'x', region: 'test', extra: { password: 'x' } })).toBe(false);
    layers.descriptor += 6;
  });

  it('delivers all four schema classes, domains, semantics and profiles (B4/Q1-C3 delivery)', () => {
    const catalog = createDescriptorCatalog([descriptor]);
    const delivered = catalog.descriptor('provider-alpha');
    expect(delivered).toBeDefined();
    expect(Object.keys(delivered?.configSchema ?? {}).length).toBeGreaterThan(0);
    expect(Object.keys(delivered?.resourceSchema ?? {}).length).toBeGreaterThan(0);
    expect(Object.keys(delivered?.capabilitySchema ?? {}).length).toBeGreaterThan(0);
    expect(Object.keys(delivered?.extensionSchema ?? {}).length).toBeGreaterThan(0);
    // Q1/C3: the descriptor also delivers the semantics registry and profiles
    expect((delivered?.semantics ?? []).map((s) => s.semanticsRef)).toEqual([
      'sem.fixed-window-counter',
      'sem.entitlement-pool',
      'sem.occupancy-slot',
    ]);
    expect((delivered?.profiles ?? []).map((p) => p.profileId)).toEqual(['profile.chat.basic']);
    expect(catalog.supportsDomain('provider-alpha', 'ai/text')).toBe(true);
    expect(catalog.supportsDomain('provider-alpha', 'mail/send')).toBe(false);
    expect(catalog.supportsDomain('provider-unknown', 'ai/text')).toBe(false);
    // a descriptor missing any schema class is rejected at construction,
    // like localtest.New ("must deliver all four schema classes")
    const partial = { ...descriptor, capabilitySchema: undefined };
    expect(() => createDescriptorCatalog([partial as unknown as ProviderDescriptorInput])).toThrow(
      /all four schema classes/,
    );
    layers.descriptor += 9;
  });

  it('verified-scope snapshot drives scope/dimensional resolution parity with the Go catalog (Q1)', () => {
    const catalog = createDescriptorCatalog([descriptor]);
    // resolveScopeFact: full verified instance, not a bare fact name —
    // physicalKey/capacityBound/semantics come from the controlled snapshot.
    expect(catalog.resolveScopeFact('acct-demo-1', 'alpha-usd-grant-promo')).toMatchObject({
      factId: 'alpha-usd-grant-promo',
      metricId: 'metric.usd.cents',
      unit: 'usd_cents',
      physicalKey: 'phys-alpha:acct-demo-1:usd-grant-promo',
      capacityBound: '500',
      metered: true,
    });
    expect(catalog.resolveScopeFact('acct-demo-2', 'alpha-usd-grant-promo')?.physicalKey).toBe(
      'phys-alpha:acct-demo-2:usd-grant-promo',
    );
    expect(catalog.resolveScopeFact('acct-demo-1', 'user-invented-bucket')).toBeUndefined();
    expect(catalog.resolveScopeFact('scope-unknown', 'alpha-usd-prepaid')).toBeUndefined();
    // resolveDimensionalFact: exactly one (metric, unit, executable window)
    // match per scope; two lifetime usd buckets → ambiguous (count 2).
    const ambiguous = catalog.resolveDimensionalFact(
      'acct-demo-1', 'provider-alpha', 'metric.usd.cents', 'usd_cents',
      (f) => f.windowKind === 'lifetime',
    );
    expect(ambiguous.count).toBe(2);
    expect(ambiguous.fact).toBeUndefined();
    const daily = catalog.resolveDimensionalFact(
      'acct-demo-1', 'provider-alpha', 'metric.tokens.total', 'token',
      (f) => windowsExecutableEqual(factWindowRule(f), windowRuleOf({
        kind: 'fixed', period: 'day', timeZone: 'Asia/Singapore', anchorISO: '', durationMillis: 0,
      })),
    );
    expect(daily.count).toBe(1);
    expect(daily.fact?.factId).toBe('alpha-daily-token-limit');
    // shared scope aliasing: the monthly token fact resolves for each aliased account
    const monthly = catalog.resolveDimensionalFact(
      'acct-demo-1', 'provider-alpha', 'metric.tokens.total', 'token',
      (f) => f.windowKind === 'fixed' && f.windowPeriod === 'month',
    );
    expect(monthly.fact?.physicalKey).toBe('phys-alpha:shared:tokens');
    // provider type filter: facts of other providers never resolve
    expect(
      catalog.resolveDimensionalFact('acct-demo-1', 'provider-other', 'metric.tokens.total', 'token', () => true)
        .count,
    ).toBe(0);
    layers.descriptor += 8;
  });

  it('catalog phase mirrors Go validateCatalog verdicts on the config document', () => {
    const catalog = createDescriptorCatalog([descriptor]);
    // S4 P2-3 adaptation: the round-1 fixture's fact-less entitlement needs
    // its explicit verified factId at the catalog phase (business assertion
    // unchanged: legal extensions pass on an executable document).
    const adapted = JSON.parse(JSON.stringify(validConfigFixture)) as Record<string, unknown>;
    for (const r of adapted['resources'] as Array<Record<string, unknown>>) {
      if (r['commercialRole'] === 'entitlement') r['providerFactId'] = 'alpha-usd-grant-promo';
    }
    expect(validateConfigDocument(adapted, { catalog }).ok).toBe(true); // legal extensions pass

    const known = cloneValidFixture();
    known.entities('resources')[1]!['providerFactId'] = 'alpha-usd-grant-promo';
    expect(validateConfigDocument(known.doc, { catalog }).ok).toBe(true);

    const invented = cloneValidFixture();
    invented.entities('resources')[1]!['providerFactId'] = 'user-invented-bucket';
    const inventedResult = validateConfigDocument(invented.doc, { catalog });
    expect(inventedResult.ok).toBe(false);
    expect(issueKeys(inventedResult)).toContain('ant.config.unknownProviderFact');

    const gamma = cloneValidFixture();
    gamma.entities('accounts')[0]!['providerTypeId'] = 'provider-gamma';
    const gammaResult = validateConfigDocument(gamma.doc, { catalog });
    expect(gammaResult.ok).toBe(false);
    expect(issueKeys(gammaResult)).toContain('ant.config.providerTypeNotRegistered');

    const secret = cloneValidFixture();
    secret.entities('accounts')[0]!['providerExtensions'] = { planName: 'x', credential: 'synthetic' };
    const secretResult = validateConfigDocument(secret.doc, { catalog });
    // Schema layer rejects first (noSecretObject forbids the exact key):
    expect(secretResult.ok).toBe(false);
    expect(issueKeys(secretResult)).toContain('ant.config.schemaViolation');
    // Structural layer (lenient doc) walks secret keys recursively/case-insensitively:
    expect(
      validateConfigDocumentSemantics(secret.doc).map((issue) => issue.messageKey),
    ).toContain('ant.config.secretMaterialForbidden');
    // Catalog layer: the registered restricted schema makes it unrepresentable
    // (generic whitelist layer = the binding/offer context class):
    expect(
      catalog.validateExtensionsInContext('provider-alpha', 'binding', { planName: 'x', credential: 'synthetic' }),
    ).toEqual([{ path: '', messageKey: 'ant.config.extensionSchemaViolation' }]);

    // absent catalog fails closed, like Go Validate(data, nil)
    const closed = validateConfigDocument(validConfigFixture, { catalog: null });
    expect(closed.ok).toBe(false);
    expect(issueKeys(closed)).toEqual(['ant.config.catalogUnavailable']);
    layers.descriptor += 6;
  });
});

// ---------------------------------------------------------------------------
// SubmitReply union guard + discriminated generated union (R05)
// ---------------------------------------------------------------------------
describe('SubmitReply codec guard (K6) and generated union', () => {
  it('narrowing the generated oneOf union by kind compiles and returns per-variant data', () => {
    const describeReply = (reply: SubmitReply): string => {
      switch (reply.kind) {
        case 'accepted':
          return reply.operationUrl;
        case 'inProgress':
          return reply.operation.operationId;
        case 'replay':
          return reply.result.state;
        case 'rejected':
          return reply.error.code;
      }
    };
    expect(describeReply).toBeTypeOf('function');
  });

  it('accepts the accepted and replay fixtures, rejects illegal unions', () => {
    expect(validateSubmitReply(readFixtureJson('api/operation.submit.accepted.response.json')).ok).toBe(true);
    expect(validateSubmitReply(readFixtureJson('api/operation.replay.response.json')).ok).toBe(true);
    expect(validateSubmitReply(readFixtureJson('api/rejection.accepted-with-terminal.json')).ok).toBe(false);
    expect(validateSubmitReply(null).ok).toBe(false);
    expect(validateSubmitReply({ kind: 'maybe' }).ok).toBe(false);
    layers.guard += 3;
  });
});

// ---------------------------------------------------------------------------
// Frozen totals — keep the matrix from silently shrinking (numbers feed the report)
// ---------------------------------------------------------------------------
describe('contract layer coverage totals', () => {
  it('executed the frozen number of per-layer assertions', () => {
    expect(manifest.version).toBe('1.1.21');
    expect(layers).toEqual({
      schema: expect.any(Number),
      codec: expect.any(Number),
      guard: expect.any(Number),
      semantic: 13, // 13 fixtures run the semantic pass; the 2 identity verdicts moved to the catalog phase (Q1)
      web: 17,
      probe: 15,
      descriptor: 31, // legal/restricted extensions 2+6, delivery 9, scope/dimensional resolution 8, catalog phase 6
      rework2: 24, // 16 corpus + 4 clone variants + P1-1/P2-2/P2-4 + 1 un-adapted draft check (P2-1 superseded by Q1 scope identity)
      rework3: 31, // 17 corpus + control compiles + 2 scope identity + 1 probe equivalence + 5 window variants + 3 capacity + 1 bundle + 1 context schema
      catalogIdentity: 2, // migrated manifest identity fixtures verified at the {catalog} entry (Q1 D-C1-v3)
    });
    expect(layers.schema).toBeGreaterThanOrEqual(12 + 13 + 2 + 18);
    expect(layers.codec).toBeGreaterThanOrEqual(18 + 2 + 1);
    expect(layers.guard).toBeGreaterThanOrEqual(3 + 3);
  });
});
