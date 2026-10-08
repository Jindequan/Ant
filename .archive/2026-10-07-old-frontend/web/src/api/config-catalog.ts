/**
 * Registered provider catalog (R12/B4, Q1 rework 3) — Web mirror of
 * internal/config document.go (ProviderCatalog, validateCatalog) and
 * internal/providers/localtest/catalog.go. Support judgments come from the
 * registered directory, never from document shape alone.
 *
 * Q1 D-C1-v3: the descriptor carries ALL FOUR schema classes, the semantics
 * registry and capability profiles, and the catalog loads the CONTROLLED
 * VERIFIED-SCOPE SNAPSHOT (contracts/providers/
 * provider-alpha.verified-scopes.json, embedded like Go's go:embed). Every
 * resource must RESOLVE to exactly one verified fact instance — by factId
 * (scope + fact) or dimensionally (metric+unit+executable-equal window) —
 * and the resolved instance's dimensions constrain the configuration.
 * An explicitly absent catalog fails closed ("catalogUnavailable") like
 * Go Validate(data, nil).
 */
import Ajv2020 from 'ajv/dist/2020';
import addFormats from 'ajv-formats';
import defaultVerifiedScopesJson from '../../../contracts/providers/provider-alpha.verified-scopes.json';
import type { ConfigIssue, Entity } from './config-helpers';
import { arr, obj, parseQuantity, str } from './config-helpers';
import { checkCommercialIdentity } from './config-graph';
import type { WindowRuleLike } from './config-helpers';

/** One provider-verified commercial bucket identity (descriptor listing). */
export type EntitlementFact = {
  factId: string;
  metricId?: string;
  unit?: string;
  description?: string;
};

/** Mirror of domain.RegisteredSemantics (Q1/C3). */
export type RegisteredSemanticsInput = {
  semanticsRef: string;
  acceptedRevisions: string[];
  resourceKind?: string;
  commercialRole?: string;
};

/** Mirror of domain.RequiredDimension / domain.RegisteredProfile (Q1/C3). */
export type RequiredDimensionInput = { metricId: string; role: string };
export type RegisteredProfileInput = {
  profileId: string;
  domain: string;
  requiredDimensions: RequiredDimensionInput[];
};

/**
 * Mirror of domain.VerifiedResourceFact: the SERVER-verified instance of a
 * capacity fact for one billing scope. capacityBound/initialRemaining are
 * canonical decimal strings (Go domain.Units) — parsed with parseQuantity.
 */
export type VerifiedResourceFact = {
  physicalKey: string;
  factId: string;
  scopeId?: string;
  providerTypeId?: string;
  metricId: string;
  unit: string;
  quantumExp: number;
  currency?: string;
  metered: boolean;
  semanticsRef: string;
  semanticsRevision: string;
  windowKind: string;
  windowPeriod?: string;
  windowTimeZone?: string;
  windowAnchorISO?: string;
  windowDurationMillis?: number;
  capacityBound: string;
  initialRemaining: string;
};

/** Mirror of domain.VerifiedScope: account scope or shared scope with aliases. */
export type VerifiedScopeInput = {
  scopeId: string;
  kind: string; // account|shared
  aliases?: string[];
  providerTypeId: string;
  facts: VerifiedResourceFact[];
};

export type ExtensionContext = 'account' | 'resource' | 'binding' | 'offer';

export type ProviderDescriptorInput = {
  typeId: string;
  supportedDomains?: string[];
  /** All four schema classes must be delivered (B4: no silent empty classes). */
  configSchema?: object;
  resourceSchema?: object;
  capabilitySchema?: object;
  /** Restricted draft-2020-12 JSON Schema for providerExtensions. */
  extensionSchema?: object;
  /** Provider-verified commercial bucket identities (R03/R12). */
  entitlementFacts?: EntitlementFact[];
  /** Q1/C3: registered resource semantics and capability profiles. */
  semantics?: RegisteredSemanticsInput[];
  profiles?: RegisteredProfileInput[];
};

/** Mirror of config.ProviderCatalog (Q1 D-C1-v3). */
export type ProviderCatalog = {
  descriptor(typeId: string): ProviderDescriptorInput | undefined;
  supportsDomain(typeId: string, domainName: string): boolean;
  /** Context schema of the entity class — the generic whitelist is not a cross-object passport (Q1/C3). */
  validateExtensionsInContext(typeId: string, context: ExtensionContext, raw: unknown): ConfigIssue[];
  /** Resolves a fact instance by scope + factId (shared scopes via aliases). */
  resolveScopeFact(scopeId: string, factId: string): VerifiedResourceFact | undefined;
  /**
   * Resolves by (metricId, unit, executable-equal window); exactly one match
   * must exist. fact is set iff count === 1; count 0 = unresolved, >1 = ambiguous.
   */
  resolveDimensionalFact(
    scopeId: string,
    providerType: string,
    metricId: string,
    unit: string,
    windowEquals: (fact: VerifiedResourceFact) => boolean,
  ): { fact: VerifiedResourceFact | undefined; count: number };
};

type SchemaValidator = (value: unknown) => boolean;

function compileSchema(schema: object): SchemaValidator {
  const ajv = new Ajv2020({ allErrors: true });
  addFormats(ajv);
  return ajv.compile(schema) as SchemaValidator;
}

// Context → schema class (Q1/C3, mirror of localtest.New): account extensions
// compile against configSchema, resource extensions against resourceSchema,
// binding/offer extensions against extensionSchema (offer capabilities use
// capabilitySchema). The generic whitelist is no longer a cross-object
// passport — resource.planName is rejected by resourceSchema (shared corpus
// case resource-extension-in-wrong-context).
const CONTEXT_SCHEMA_FIELD: Record<ExtensionContext, 'configSchema' | 'resourceSchema' | 'extensionSchema' | 'capabilitySchema'> = {
  account: 'configSchema',
  resource: 'resourceSchema',
  binding: 'extensionSchema',
  offer: 'capabilitySchema',
};

const DEFAULT_SCOPE_SNAPSHOT = defaultVerifiedScopesJson as unknown as { scopes: VerifiedScopeInput[] };

/**
 * Build a catalog from registered descriptors (mirror of localtest.New). The
 * verified-scope snapshot is embedded like Go's go:embed and may be
 * overridden explicitly for tests.
 */
export function createDescriptorCatalog(
  descriptors: ProviderDescriptorInput[],
  scopes: { scopes: VerifiedScopeInput[] } = DEFAULT_SCOPE_SNAPSHOT,
): ProviderCatalog {
  const byType = new Map<string, ProviderDescriptorInput>();
  const contextValidators = new Map<string, Map<ExtensionContext, SchemaValidator>>();
  const scopeByID = new Map<string, VerifiedScopeInput>();
  const sharedScopes: VerifiedScopeInput[] = [];
  for (const descriptor of descriptors) {
    // B4: the reader must deliver ALL FOUR schema classes — a descriptor that
    // drops any of them is a delivery bug, not a usable form contract.
    if (
      !descriptor.configSchema ||
      Object.keys(descriptor.configSchema).length === 0 ||
      !descriptor.resourceSchema ||
      Object.keys(descriptor.resourceSchema).length === 0 ||
      !descriptor.capabilitySchema ||
      Object.keys(descriptor.capabilitySchema).length === 0 ||
      !descriptor.extensionSchema ||
      Object.keys(descriptor.extensionSchema).length === 0
    ) {
      throw new Error('descriptor must deliver all four schema classes');
    }
    byType.set(descriptor.typeId, descriptor);
    // Q1/C3: every schema class is actually compiled and consumed via its
    // context mapping, not just delivered.
    const compiled = new Map<ExtensionContext, SchemaValidator>();
    for (const context of Object.keys(CONTEXT_SCHEMA_FIELD) as ExtensionContext[]) {
      compiled.set(context, compileSchema(descriptor[CONTEXT_SCHEMA_FIELD[context]] as object));
    }
    contextValidators.set(descriptor.typeId, compiled);
  }
  for (const source of scopes.scopes) {
    if (!source.scopeId || !source.providerTypeId) throw new Error('verified scope: scopeId and providerTypeId are required');
    if (!byType.has(source.providerTypeId)) throw new Error('verified scope: providerTypeId is not registered');
    if (source.kind !== 'account' && source.kind !== 'shared') throw new Error('verified scope: unsupported kind');
    if (scopeByID.has(source.scopeId)) throw new Error('verified scope: duplicate scopeId');
    const scope = { ...source, facts: source.facts.map((fact) => {
      if (fact.scopeId && fact.scopeId !== source.scopeId) throw new Error('verified fact: scopeId contradicts parent');
      if (fact.providerTypeId && fact.providerTypeId !== source.providerTypeId) throw new Error('verified fact: providerTypeId contradicts parent');
      return { ...fact, scopeId: source.scopeId, providerTypeId: source.providerTypeId };
    }) };
    scopeByID.set(scope.scopeId, scope);
    if (scope.kind === 'shared') sharedScopes.push(scope);
  }
  const aliasingShared = (scopeId: string): VerifiedScopeInput[] =>
    sharedScopes.filter((scope) => (scope.aliases ?? []).includes(scopeId));
  const factsOf = (scope: VerifiedScopeInput): VerifiedResourceFact[] => scope.facts ?? [];
  return {
    descriptor: (typeId) => byType.get(typeId),
    supportsDomain(typeId, domainName) {
      const descriptor = byType.get(typeId);
      if (!descriptor) return false;
      return (descriptor.supportedDomains ?? []).some((dom) => dom === domainName);
    },
    validateExtensionsInContext(typeId, context, raw) {
      if (!byType.has(typeId)) {
        return [{ path: '', messageKey: 'ant.config.providerTypeNotRegistered' }];
      }
      if (raw === undefined) return []; // omitted extensions are fine; present ones must validate
      if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
        return [{ path: '', messageKey: 'ant.config.extensionSchemaViolation' }];
      }
      const validate = contextValidators.get(typeId)?.get(context);
      if (!validate) return [{ path: '', messageKey: 'ant.config.providerTypeNotRegistered' }];
      return validate(raw) ? [] : [{ path: '', messageKey: 'ant.config.extensionSchemaViolation' }];
    },
    // localtest ResolveScopeFact: direct scope first, then shared scopes
    // aliasing the scope id; factId picks exactly one instance.
    resolveScopeFact(scopeId, factId) {
      const direct = scopeByID.get(scopeId);
      if (direct) {
        const hit = factsOf(direct).find((f) => f.factId === factId);
        if (hit) return hit;
      }
      for (const scope of aliasingShared(scopeId)) {
        const hit = factsOf(scope).find((f) => f.factId === factId);
        if (hit) return hit;
      }
      return undefined;
    },
    // localtest ResolveDimensionalFact: (metricId, unit, windowEquals) within
    // the scope and its shared scopes, filtered by provider type; exactly one
    // match must exist.
    resolveDimensionalFact(scopeId, providerType, metricId, unit, windowEquals) {
      const matches: VerifiedResourceFact[] = [];
      const check = (scope: VerifiedScopeInput): void => {
        if (scope.providerTypeId !== providerType) return;
        for (const fact of factsOf(scope)) {
          if (fact.metricId === metricId && fact.unit === unit && windowEquals(fact)) matches.push(fact);
        }
      };
      const direct = scopeByID.get(scopeId);
      if (direct) check(direct);
      for (const scope of aliasingShared(scopeId)) check(scope);
      if (matches.length === 1) return { fact: matches[0], count: 1 };
      return { fact: undefined, count: matches.length };
    },
  };
}

// ---------------------------------------------------------------------------
// Catalog validation phase — mirror of Go config.validateCatalog (Q1/C1-C3):
// resolution identity (config-graph checkCommercialIdentity), verified-capacity
// constraints, semantics registry, context schemas, profile registration and
// full required-bundle coverage. Nil catalog fails closed.
// ---------------------------------------------------------------------------

export type CatalogPhaseResult = {
  issues: ConfigIssue[];
  /** resourceId → resolved verified fact (only successfully resolved resources). */
  resolutions: Map<string, VerifiedResourceFact>;
};

export function validateCatalogPhase(doc: Entity, catalog: ProviderCatalog | null): CatalogPhaseResult {
  if (catalog === null) {
    return {
      issues: [{ path: '/', messageKey: 'ant.config.catalogUnavailable' }],
      resolutions: new Map(),
    };
  }
  const issues: ConfigIssue[] = [];
  const prefix = (pre: string, list: ConfigIssue[]): void => {
    for (const issue of list) issues.push({ path: pre + issue.path, messageKey: issue.messageKey });
  };
  // Q1/C1: resolution identity runs first (config-graph) and yields the
  // resolved facts the constraint checks below consume.
  const resolutions = checkCommercialIdentity(doc, catalog, issues);

  const draftTypes = new Set(arr(doc.unsupportedDrafts).map((d) => str(obj(d).providerTypeId)));
  const accountByID = new Map<string, Entity>(); // last wins, like the Go map
  for (const rawAccount of arr(doc.accounts)) {
    const a = obj(rawAccount);
    accountByID.set(str(a.accountId), a);
  }

  // Accounts: registered type + account-context schema (configSchema).
  arr(doc.accounts).forEach((rawAccount, i) => {
    const a = obj(rawAccount);
    const p = `/accounts/${i}`;
    const typeId = str(a.providerTypeId);
    if (!catalog.descriptor(typeId)) {
      issues.push({
        path: `${p}/providerTypeId`,
        messageKey: draftTypes.has(typeId)
          ? 'ant.config.unsupportedProviderExecutable'
          : 'ant.config.providerTypeNotRegistered',
      });
      return;
    }
    prefix(`${p}/providerExtensions`, catalog.validateExtensionsInContext(typeId, 'account', a.providerExtensions));
  });

  // Bindings: domain support + registered capability profiles (Q1/C3) +
  // binding-context schema.
  arr(doc.bindings).forEach((rawBinding, i) => {
    const b = obj(rawBinding);
    const p = `/bindings/${i}`;
    const acct = accountByID.get(str(b.accountId));
    if (acct === undefined) return; // unknownAccount already reported structurally
    const typeId = str(acct.providerTypeId);
    // Context support: the binding's business domain must be one the
    // registered provider handles (unsupported domains are not executable).
    if (!catalog.supportsDomain(typeId, str(b.domain))) {
      issues.push({ path: `${p}/domain`, messageKey: 'ant.config.unsupportedDomain' });
    }
    // Registered capability profile: every declared profile must exist in the
    // catalogue (Q1/C3 — self-consistent declarations prove nothing).
    const desc = catalog.descriptor(typeId);
    if (desc) {
      const registered = new Set(arr(desc.profiles).map((prof) => str(obj(prof).profileId)));
      for (const cp of arr(b.capabilityProfiles)) {
        if (!registered.has(str(cp))) {
          issues.push({ path: `${p}/capabilityProfiles`, messageKey: 'ant.config.unregisteredCapabilityProfile' });
        }
      }
    }
    prefix(`${p}/providerExtensions`, catalog.validateExtensionsInContext(typeId, 'binding', b.providerExtensions));
  });

  const bindingAccount = new Map<string, string>();
  for (const rawBinding of arr(doc.bindings)) {
    const b = obj(rawBinding);
    bindingAccount.set(str(b.bindingId), str(b.accountId));
  }
  const bindingByID = new Map<string, Entity>(); // every binding, like Go bindingIndexByID
  for (const rawBinding of arr(doc.bindings)) {
    const b = obj(rawBinding);
    bindingByID.set(str(b.bindingId), b);
  }
  const accountTypeOf = (accountId: string): string => str(obj(accountByID.get(accountId) ?? {}).providerTypeId);
  const evidenceByID = new Map<string, Entity>(); // first occurrence wins (Go evidenceByID)
  for (const rawEvidence of arr(doc.evidence)) {
    const ev = obj(rawEvidence);
    const id = str(ev.evidenceId);
    if (!evidenceByID.has(id)) evidenceByID.set(id, ev);
  }

  // Resources: resolved facts constrain the configuration (Q1/C2). Resources
  // that failed to resolve are drafts — their catalog constraint checks are
  // skipped exactly like Go's `continue` after resolveResource.
  arr(doc.resources).forEach((rawResource, i) => {
    const r = obj(rawResource);
    const p = `/resources/${i}`;
    const fact = resolutions.get(str(r.resourceId));
    if (fact === undefined) return;
    const bound = parseQuantity(fact.capacityBound) ?? 0n;
    // Capacity cannot exceed the verified bound (evidence never self-certifies
    // growth; conservative lower values stay legal).
    const configured = parseQuantity(r.configuredCapacity);
    if (configured === null || configured > bound) {
      issues.push({ path: `${p}/configuredCapacity`, messageKey: 'ant.config.capacityExceedsVerifiedBound' });
    }
    const checkQuantity = (ref: string, kinds: string[], field: string, verified: string, path: string): void => {
      const ev = evidenceByID.get(ref);
      if (ev === undefined || !kinds.includes(str(ev.kind))) return;
      const claimed = parseQuantity(obj(ev.payload)[field]);
      if (claimed === null || claimed < 0n) {
        issues.push({ path, messageKey: 'ant.config.evidenceNotApplicable' });
      } else if (claimed !== parseQuantity(verified)) {
        issues.push({ path, messageKey: 'ant.config.evidenceConflictsVerifiedFact' });
      }
    };
    checkQuantity(str(r.initialStateEvidenceRef), ['initial_state'], 'remaining', fact.initialRemaining, `${p}/initialStateEvidenceRef`);
    checkQuantity(str(r.capacityEvidenceRef), ['bound', 'hard_stop'], 'bound', fact.capacityBound, `${p}/capacityEvidenceRef`);
    // Registered semantics (Q1/C3): reference and revision must be registered
    // for the provider type.
    const ownerAccount =
      str(r.ownerKind) === 'account' ? str(r.ownerId) : (bindingAccount.get(str(r.ownerId)) ?? '');
    const typeId = accountTypeOf(ownerAccount);
    const desc = typeId !== '' ? catalog.descriptor(typeId) : undefined;
    if (fact.providerTypeId !== typeId) {
      issues.push({ path: `${p}/ownerId`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    if (desc) {
      const registrations = (desc.semantics ?? []).filter((sem) =>
        sem.semanticsRef === fact.semanticsRef && sem.acceptedRevisions.includes(fact.semanticsRevision));
      const sem = registrations[0];
      if (registrations.length !== 1 || !sem || !['counter', 'occupancy'].includes(sem.resourceKind ?? '') ||
          !['limit', 'entitlement'].includes(sem.commercialRole ?? '')) {
        issues.push({ path: `${p}/semanticsRef`, messageKey: 'ant.config.unregisteredResourceSemantics' });
      } else {
        if (str(r.kind) !== sem.resourceKind) issues.push({ path: `${p}/kind`, messageKey: 'ant.config.providerFactMetadataMismatch' });
        if (str(r.commercialRole) !== sem.commercialRole) issues.push({ path: `${p}/commercialRole`, messageKey: 'ant.config.providerFactMetadataMismatch' });
      }
    }
    prefix(`${p}/providerExtensions`, catalog.validateExtensionsInContext(typeId, 'resource', r.providerExtensions));
  });

  // Offers (Q1/C3): registered profile and full required-dimension coverage
  // by RESOLVED resources — removing the billing or concurrency limit from an
  // offer makes it non-executable.
  arr(doc.offers).forEach((rawOffer, i) => {
    const o = obj(rawOffer);
    const p = `/offers/${i}`;
    const bindingId = str(o.bindingId);
    const binding = bindingByID.get(bindingId);
    if (binding === undefined) return; // unknownBinding already reported structurally
    const acct = accountByID.get(str(binding.accountId));
    if (acct === undefined) return;
    const typeId = str(acct.providerTypeId);
    if (!catalog.supportsDomain(typeId, str(o.domain))) {
      issues.push({ path: `${p}/domain`, messageKey: 'ant.config.unsupportedDomain' });
    }
    const desc = catalog.descriptor(typeId);
    if (!desc) return;
    const profile = arr(desc.profiles).map((raw) => obj(raw)).find((prof) => str(prof.profileId) === str(o.capabilityProfile));
    if (profile === undefined) {
      issues.push({ path: `${p}/capabilityProfile`, messageKey: 'ant.config.unregisteredCapabilityProfile' });
      return;
    }
    if (str(profile.domain) !== str(o.domain)) {
      issues.push({ path: `${p}/capabilityProfile`, messageKey: 'ant.config.profileDomainMismatch' });
    }
    const resourceByID = new Map(arr(doc.resources).map((raw) => [str(obj(raw).resourceId), obj(raw)]));
    const missing = arr(profile.requiredDimensions).some((raw) => {
      const dim = obj(raw);
      return !arr(o.resourceRefs).some((ref) => {
        const fact = resolutions.get(str(ref)), resource = resourceByID.get(str(ref));
        if (!fact || !resource || fact.metricId !== str(dim.metricId)) return false;
        const role = str(dim.role);
        if (role === 'occupancy') return str(resource.kind) === 'occupancy';
        if (role === 'limit' || role === 'entitlement') return str(resource.commercialRole) === role;
        return false;
      });
    });
    if (missing) {
      issues.push({ path: `${p}/resourceRefs`, messageKey: 'ant.config.incompleteRequiredBundle' });
    }
  });

  return { issues, resolutions };
}

// ---------------------------------------------------------------------------
// Compiled config (Q1): zero issues yield the consumable execution summary —
// mirror of Go CompiledConfig/CompiledResource/CompiledOffer. Quantities are
// BigInt (domain.Units); the window is the RESOLVED FACT's canonical window.
// ---------------------------------------------------------------------------

export type CompiledResourceSummary = {
  resourceId: string;
  physicalKey: string;
  scopeId: string;
  ownerKind: string;
  ownerId: string;
  metricId: string;
  unit: string;
  quantumExp: number;
  currency: string;
  metered: boolean;
  kind: string;
  commercialRole: string;
  semanticsRef: string;
  semanticsRevision: string;
  window: WindowRuleLike;
  capacity: string;
  initialRemaining: string;
  evidenceRefs: string[];
};

export type CompiledOfferSummary = {
  offerId: string;
  domain: string;
  capabilityProfile: string;
  physicalKeys: string[];
};

export type CompiledConfigSummary = {
  resources: CompiledResourceSummary[];
  offers: CompiledOfferSummary[];
};

/** Go mustCapacity: malformed values compile as 0 (structural already flagged them). */
const mustCapacity = (raw: unknown): bigint => parseQuantity(raw) ?? 0n;

export function buildCompiledConfig(doc: Entity, resolutions: Map<string, VerifiedResourceFact>): CompiledConfigSummary {
  const resources: CompiledResourceSummary[] = [];
  for (const rawResource of arr(doc.resources)) {
    const r = obj(rawResource);
    const fact = resolutions.get(str(r.resourceId));
    if (fact === undefined) continue; // unreachable at the compiled stage (zero issues)
    const evidenceRefs: string[] = [];
    if (str(r.capacityEvidenceRef) !== '') evidenceRefs.push(str(r.capacityEvidenceRef));
    if (str(r.initialStateEvidenceRef) !== '') evidenceRefs.push(str(r.initialStateEvidenceRef));
    resources.push({
      resourceId: str(r.resourceId),
      physicalKey: fact.physicalKey,
      scopeId: fact.scopeId ?? '',
      ownerKind: str(r.ownerKind),
      ownerId: str(r.ownerId),
      metricId: fact.metricId,
      unit: fact.unit,
      quantumExp: fact.quantumExp,
      currency: fact.currency ?? '',
      metered: fact.metered,
      kind: str(r.kind),
      commercialRole: str(r.commercialRole),
      semanticsRef: fact.semanticsRef,
      semanticsRevision: fact.semanticsRevision,
      window: {
        kind: fact.windowKind,
        period: fact.windowPeriod ?? '',
        timeZone: fact.windowTimeZone ?? '',
        anchorISO: fact.windowAnchorISO ?? '',
        durationMillis: fact.windowDurationMillis ?? 0,
      },
      capacity: (mustCapacity(r.configuredCapacity)).toString(),
      initialRemaining: (mustCapacity(fact.initialRemaining)).toString(),
      evidenceRefs,
    });
  }
  const byResourceId = new Map(resources.map((cr) => [cr.resourceId, cr]));
  const offers: CompiledOfferSummary[] = arr(doc.offers).map((rawOffer) => {
    const o = obj(rawOffer);
    const physicalKeys: string[] = [];
    for (const ref of arr(o.resourceRefs)) {
      const cr = byResourceId.get(str(ref));
      if (cr) physicalKeys.push(cr.physicalKey);
    }
    return {
      offerId: str(o.offerId),
      domain: str(o.domain),
      capabilityProfile: str(o.capabilityProfile),
      physicalKeys,
    };
  });
  return { resources, offers };
}
