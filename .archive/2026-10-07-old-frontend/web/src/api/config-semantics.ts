/**
 * Typed reference-graph semantics for the ConfigDocument — rule-by-rule Web
 * mirror of server/internal/config/validate_graph.go (R02/R03). Ordering
 * follows the Go passes: identity/shape → entity-local rules → cross-entity
 * edges (config-graph.ts) → commercial identity (R03) → evidence typing.
 * Every edge has a negative fixture in contracts/fixtures/
 * config-document-invalid-*.json and in the shared audit config-cases;
 * issue messageKeys equal the Go keys.
 *
 * Entry points live in validate.ts (schema gate + optional catalog phase).
 */
import type { ConfigIssue, Entity } from './config-helpers';
import { rfc3339SubMillisecond } from './config-helpers';
import {
  arr,
  checkIDList,
  num,
  obj,
  parseQuantity,
  rfc3339Valid,
  str,
  timeZoneValid,
  validID,
  validUnit,
  walkSecretKeys,
} from './config-helpers';
import {
  checkEntitlementGroupMembership,
  checkEntitlementGroups,
  checkEvidence,
  checkGrants,
  checkPools,
} from './config-graph';

/**
 * Structural-only semantic pass on a leniently decoded document — the mirror
 * of the shared probe entry (Go: strict decode + ValidateStructural, no
 * catalog). The schema-gated and catalog-aware boundary lives in validate.ts.
 */
export function validateConfigDocumentSemantics(json: unknown): ConfigIssue[] {
  return runStructural(obj(json));
}

function checkWindow(p: string, wRaw: unknown, issues: ConfigIssue[]): void {
  const w = obj(wRaw);
  switch (str(w.kind)) {
    case 'fixed': {
      if (!['minute', 'hour', 'day', 'week', 'month'].includes(str(w.period))) {
        issues.push({ path: `${p}/period`, messageKey: 'ant.config.windowPeriodInvalid' });
      }
      if (!timeZoneValid(str(w.timeZone))) {
        issues.push({ path: `${p}/timeZone`, messageKey: 'ant.config.timeZoneInvalid' });
      }
      const anchor = str(w.anchorISO);
      if (anchor !== '' && !rfc3339Valid(anchor)) {
        issues.push({ path: `${p}/anchorISO`, messageKey: 'ant.config.anchorInvalid' });
      } else if (anchor !== '' && rfc3339SubMillisecond(anchor)) {
        // Admission matches WindowAt exactly (B5/S4 P2-4): anchors support
        // millisecond precision; finer precision is an explicit rejection.
        issues.push({ path: `${p}/anchorISO`, messageKey: 'ant.config.anchorPrecisionUnsupported' });
      }
      break;
    }
    case 'rolling':
      if (!(num(w.durationMillis) > 0)) {
        issues.push({ path: `${p}/durationMillis`, messageKey: 'ant.config.windowDurationInvalid' });
      }
      break;
    case 'lifetime':
      break;
    default:
      issues.push({ path: `${p}/kind`, messageKey: 'ant.config.windowKindInvalid' });
  }
}

/**
 * Structural mirror of validate_graph.go run(). Order of passes:
 * identity/shape → entity-local rules → cross-entity edges → commercial
 * identity (R03) → evidence typing.
 */
export function runStructural(doc: Entity): ConfigIssue[] {
  const issues: ConfigIssue[] = [];
  if (doc.schemaVersion !== 1) {
    issues.push({ path: '/schemaVersion', messageKey: 'ant.config.schemaVersionUnsupported' });
  }
  // Root arrays must be PRESENT (absent/null invalid; empty = explicit "none").
  for (const key of [
    'accounts', 'bindings', 'resources', 'entitlementGroups',
    'offers', 'pools', 'grants', 'evidence', 'unsupportedDrafts',
  ]) {
    if (!Array.isArray(doc[key])) {
      issues.push({ path: `/${key}`, messageKey: 'ant.config.requiredArrayMissing' });
    }
  }

  const unsupported = new Set(arr(doc.unsupportedDrafts).map((d) => str(obj(d).providerTypeId)));

  const accounts = new Set<string>();
  arr(doc.accounts).forEach((rawAccount, i) => {
    const a = obj(rawAccount);
    const p = `/accounts/${i}`;
    const accountId = str(a.accountId);
    if (!validID(accountId) || accounts.has(accountId)) {
      issues.push({ path: `${p}/accountId`, messageKey: 'ant.config.invalidOrDuplicateId' });
    }
    accounts.add(accountId);
    if (!validID(str(a.providerTypeId))) {
      issues.push({ path: `${p}/providerTypeId`, messageKey: 'ant.config.invalidId' });
    }
    if (unsupported.has(str(a.providerTypeId))) {
      issues.push({ path: `${p}/providerTypeId`, messageKey: 'ant.config.unsupportedProviderExecutable' });
    }
    walkSecretKeys(`${p}/providerExtensions`, a.providerExtensions, issues);
  });

  const bindings = new Set<string>();
  const bindingByID = new Map<string, Entity>();
  arr(doc.bindings).forEach((rawBinding, i) => {
    const b = obj(rawBinding);
    const p = `/bindings/${i}`;
    const bindingId = str(b.bindingId);
    if (!validID(bindingId) || bindings.has(bindingId)) {
      issues.push({ path: `${p}/bindingId`, messageKey: 'ant.config.invalidOrDuplicateId' });
    }
    if (!bindingByID.has(bindingId)) bindingByID.set(bindingId, b);
    bindings.add(bindingId);
    if (!accounts.has(str(b.accountId))) {
      issues.push({ path: `${p}/accountId`, messageKey: 'ant.config.unknownAccount' });
    }
    if (str(b.domain) === '' || str(b.endpoint) === '') {
      issues.push({ path: p, messageKey: 'ant.config.domainAndEndpointRequired' });
    }
    if (!validID(str(b.credentialRef)) || !validID(str(b.credentialRevision))) {
      issues.push({ path: `${p}/credentialRef`, messageKey: 'ant.config.credentialRefRequired' });
    }
    checkIDList(`${p}/capabilityProfiles`, arr(b.capabilityProfiles), null, issues);
    walkSecretKeys(`${p}/providerExtensions`, b.providerExtensions, issues);
  });

  const resources = new Set<string>();
  const resourceByID = new Map<string, Entity>();
  arr(doc.resources).forEach((rawResource, i) => {
    const r = obj(rawResource);
    const p = `/resources/${i}`;
    const resourceId = str(r.resourceId);
    if (!validID(resourceId) || resources.has(resourceId)) {
      issues.push({ path: `${p}/resourceId`, messageKey: 'ant.config.invalidOrDuplicateId' });
    }
    resources.add(resourceId);
    if (!resourceByID.has(resourceId)) resourceByID.set(resourceId, r);
    // ownerId must resolve according to ownerKind (R02 edge).
    switch (str(r.ownerKind)) {
      case 'account':
        if (!accounts.has(str(r.ownerId))) {
          issues.push({ path: `${p}/ownerId`, messageKey: 'ant.config.unknownOwner' });
        }
        break;
      case 'binding':
        if (!bindings.has(str(r.ownerId))) {
          issues.push({ path: `${p}/ownerId`, messageKey: 'ant.config.unknownOwner' });
        }
        break;
      default:
        issues.push({ path: `${p}/ownerKind`, messageKey: 'ant.config.ownerKindInvalid' });
    }
    if (!validID(str(r.metricId))) {
      issues.push({ path: `${p}/metricId`, messageKey: 'ant.config.invalidId' });
    }
    if (!validUnit(str(r.unit))) {
      issues.push({ path: `${p}/unit`, messageKey: 'ant.config.unitInvalid' });
    }
    const currency = str(r.currency);
    if (currency !== '' && currency.length !== 3) {
      issues.push({ path: `${p}/currency`, messageKey: 'ant.config.currencyInvalid' });
    }
    const quantumExp = num(r.quantumExp);
    if (quantumExp < 0 || quantumExp > 18) {
      issues.push({ path: `${p}/quantumExp`, messageKey: 'ant.config.quantumExpRange' });
    }
    if (str(r.kind) !== 'counter' && str(r.kind) !== 'occupancy') {
      issues.push({ path: `${p}/kind`, messageKey: 'ant.config.kindInvalid' });
    }
    if (str(r.commercialRole) !== 'limit' && str(r.commercialRole) !== 'entitlement') {
      issues.push({ path: `${p}/commercialRole`, messageKey: 'ant.config.commercialRoleInvalid' });
    }
    const factId = str(r.providerFactId);
    if (factId !== '' && !validID(factId)) {
      issues.push({ path: `${p}/providerFactId`, messageKey: 'ant.config.invalidId' });
    }
    const capacity = parseQuantity(r.configuredCapacity);
    if (capacity === null || capacity < 0n) {
      issues.push({ path: `${p}/configuredCapacity`, messageKey: 'ant.config.quantityInvalid' });
    }
    if (!validID(str(r.capacityEvidenceRef))) {
      issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.capacityEvidenceRequired' });
    }
    checkWindow(`${p}/window`, r.window, issues);
    walkSecretKeys(`${p}/refillRule`, r.refillRule, issues);
    walkSecretKeys(`${p}/providerExtensions`, r.providerExtensions, issues);
  });

  // binding.resourceRefs existence needs the full resource set (R02 edge).
  arr(doc.bindings).forEach((rawBinding, i) => {
    checkIDList(`/bindings/${i}/resourceRefs`, arr(obj(rawBinding).resourceRefs), resources, issues);
  });

  checkEntitlementGroups(doc, resourceByID, issues);
  checkEntitlementGroupMembership(doc, issues);
  // Commercial identity (Q1 D-C1-v3) is RESOLUTION-based and runs in the
  // catalog phase only (config-graph checkCommercialIdentity needs the
  // registered verified-scope directory). The catalog-free structural pass
  // cannot resolve, so it mints no identity verdicts — the legacy duplicate
  // fixtures verify at the {catalog} entry (see contract.test.ts).

  const offers = new Set<string>();
  // requiredEvidenceRefs must reference evidence of kind "bound" (Go: evidenceKinds(doc, "bound")).
  const boundEvidence = new Set<string>();
  for (const rawEvidence of arr(doc.evidence)) {
    const e = obj(rawEvidence);
    if (str(e.kind) === 'bound') boundEvidence.add(str(e.evidenceId));
  }
  arr(doc.offers).forEach((rawOffer, i) => {
    const o = obj(rawOffer);
    const p = `/offers/${i}`;
    const offerId = str(o.offerId);
    if (!validID(offerId) || offers.has(offerId)) {
      issues.push({ path: `${p}/offerId`, messageKey: 'ant.config.invalidOrDuplicateId' });
    }
    offers.add(offerId);
    const bindingId = str(o.bindingId);
    const binding = bindingByID.get(bindingId);
    if (!validID(bindingId) || binding === undefined) {
      issues.push({ path: `${p}/bindingId`, messageKey: 'ant.config.unknownBinding' });
    } else {
      // The offer executes on its binding's path: domain and capability must
      // match the binding declaration (R02 edges).
      if (str(o.domain) !== str(binding.domain)) {
        issues.push({ path: `${p}/domain`, messageKey: 'ant.config.offerDomainMismatch' });
      }
      if (!arr(binding.capabilityProfiles).includes(o.capabilityProfile)) {
        issues.push({ path: `${p}/capabilityProfile`, messageKey: 'ant.config.offerCapabilityMismatch' });
      }
      if (!validID(str(o.capabilityProfile))) {
        issues.push({ path: `${p}/capabilityProfile`, messageKey: 'ant.config.invalidId' });
      }
    }
    if (str(o.domain) === '') {
      issues.push({ path: p, messageKey: 'ant.config.domainAndCapabilityRequired' });
    }
    checkIDList(`${p}/resourceRefs`, arr(o.resourceRefs), resources, issues);
    if (arr(o.resourceRefs).length === 0) {
      issues.push({ path: `${p}/resourceRefs`, messageKey: 'ant.config.bundleRequired' });
    }
    // The offer's bundle cannot exceed what its binding references.
    if (binding !== undefined) {
      const allowed = new Set(arr(binding.resourceRefs).map((id) => str(id)));
      arr(o.resourceRefs).forEach((rawId, j) => {
        if (!allowed.has(str(rawId))) {
          issues.push({ path: `${p}/resourceRefs/${j}`, messageKey: 'ant.config.resourceNotOnBinding' });
        }
      });
    }
    checkIDList(`${p}/requiredEvidenceRefs`, arr(o.requiredEvidenceRefs), boundEvidence, issues);
  });

  checkPools(doc, offers, issues);
  checkGrants(doc, issues);
  checkEvidence(doc, issues);

  arr(doc.unsupportedDrafts).forEach((rawDraft, i) => {
    const d = obj(rawDraft);
    if (!validID(str(d.draftId)) || !validID(str(d.providerTypeId))) {
      issues.push({ path: `/unsupportedDrafts/${i}`, messageKey: 'ant.config.invalidId' });
    }
  });

  return issues;
}
