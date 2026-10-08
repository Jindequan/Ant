/**
 * Cross-entity edge and commercial-identity checks — Web mirror of the
 * back half of server/internal/config/validate_graph.go plus the Q1
 * resolution identity from compile.go/document.go (rework 3):
 * entitlement groups (order/scope/metric-unit edges and the B3 quantity
 * space), the resource→group back edge, pools, grants (B3 scope/namespace
 * resolution) and evidence typing plus B3 applicability, and the RESOLUTION
 * identity (Q1 D-C1-v3): every resource must resolve to exactly one
 * verified fact instance. Issue messageKeys equal the Go keys.
 */
import type { ConfigIssue, Entity } from './config-helpers';
import {
  arr,
  evidenceKindsByID,
  factWindowRule,
  GRANT_PERMISSIONS,
  GRANT_SCOPE_KINDS,
  idSet,
  num,
  obj,
  parseQuantity,
  resourceIndex,
  str,
  validID,
  validSourceRef,
  validUnit,
  walkSecretKeys,
  windowRuleOf,
  windowsExecutableEqual,
} from './config-helpers';
import type { ProviderCatalog, VerifiedResourceFact } from './config-catalog';

// ---------------------------------------------------------------------------
// Q1 D-C1-v3 resolution identity (mirror of Go resolveResource +
// verifyFactConstrainsResource + the duplicate-physicalKey pass in
// validateCatalog). The default-slot/classification implementation of round 2
// is DELETED: identity is the RESOLUTION RESULT of the registered directory —
// classification strings, groups, providerOrder and resource ids never mint
// capacity. Runs only with a catalog (the structural pass stays catalog-free;
// Go keeps the legacy classification in ValidateStructural, the web drops it
// deliberately — the two legacy duplicate fixtures verify at the {catalog}
// entry now).
// ---------------------------------------------------------------------------

function bindingAccountID(doc: Entity, bindingId: string): string {
  for (const rawBinding of arr(doc.bindings)) {
    const b = obj(rawBinding);
    if (str(b.bindingId) === bindingId) return str(b.accountId);
  }
  return '';
}

function providerTypeOfAccount(doc: Entity, accountId: string): string {
  for (const rawAccount of arr(doc.accounts)) {
    const a = obj(rawAccount);
    if (str(a.accountId) === accountId) return str(a.providerTypeId);
  }
  return '';
}

/** factWindowMatches (compile.go): executable equality of fact and config window. */
function factWindowMatches(fact: VerifiedResourceFact, window: Entity): boolean {
  return windowsExecutableEqual(factWindowRule(fact), windowRuleOf(obj(window)));
}

/**
 * Resolution identity for every resource. Issues unresolvableResourceOwner /
 * unknownProviderFact / factUnresolved / ambiguousFact on resolution failure,
 * providerFactMetadataMismatch when the resolved instance's dimensions
 * disagree with the resource (metric/unit/quantumExp/currency/metered/
 * semantics/window), and duplicateCommercialFact when two resources resolve to
 * the SAME physical ledger. Returns resourceId → resolved fact for the
 * capacity/semantics/bundle checks downstream.
 */
export function checkCommercialIdentity(
  doc: Entity,
  catalog: ProviderCatalog,
  issues: ConfigIssue[],
): Map<string, VerifiedResourceFact> {
  const resolutions = new Map<string, VerifiedResourceFact>();
  const usedPhysicalKeys = new Set<string>();
  arr(doc.resources).forEach((rawResource, i) => {
    const r = obj(rawResource);
    const p = `/resources/${i}`;
    // resolveResource: binding-owned resources resolve through their binding
    // to the account; a resource without a resolvable owner scope cannot
    // resolve at all.
    let scopeId = '';
    switch (str(r.ownerKind)) {
      case 'account':
        scopeId = str(r.ownerId);
        break;
      case 'binding':
        scopeId = bindingAccountID(doc, str(r.ownerId));
        if (scopeId === '') {
          issues.push({ path: `${p}/ownerId`, messageKey: 'ant.config.unresolvableResourceOwner' });
          return;
        }
        break;
      default:
        return; // ownerKindInvalid already reported structurally
    }
    const providerType = providerTypeOfAccount(doc, scopeId);
    if (providerType === '') {
      issues.push({ path: `${p}/ownerId`, messageKey: 'ant.config.unresolvableResourceOwner' });
      return;
    }
    // Explicit factId resolves by name; otherwise the resource resolves
    // dimensionally (metric+unit+executable-equal window) within the owner's
    // billing scopes — 0 matches = unresolved draft, >1 = ambiguous.
    const factId = str(r.providerFactId);
    let fact: VerifiedResourceFact | undefined;
    if (factId !== '') {
      fact = catalog.resolveScopeFact(scopeId, factId);
      if (fact === undefined) {
        issues.push({ path: `${p}/providerFactId`, messageKey: 'ant.config.unknownProviderFact' });
        return;
      }
    } else {
      const resolved = catalog.resolveDimensionalFact(
        scopeId,
        providerType,
        str(r.metricId),
        str(r.unit),
        (candidate) => factWindowMatches(candidate, obj(r.window)),
      );
      if (resolved.fact === undefined) {
        issues.push({
          path: `${p}/providerFactId`,
          messageKey: resolved.count > 1 ? 'ant.config.ambiguousFact' : 'ant.config.factUnresolved',
        });
        return;
      }
      fact = resolved.fact;
    }
    // Two resources resolving to one physicalKey = one ledger declared twice
    // (clone); renaming/regrouping cannot mint a second one.
    if (usedPhysicalKeys.has(fact.physicalKey)) {
      issues.push({ path: `${p}/providerFactId`, messageKey: 'ant.config.duplicateCommercialFact' });
    } else {
      usedPhysicalKeys.add(fact.physicalKey);
    }
    // verifyFactConstrainsResource (Q1/C2): commercial nature comes from the
    // verified instance, not from the configuration's claims.
    if (fact.metricId !== str(r.metricId)) {
      issues.push({ path: `${p}/metricId`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    if (fact.unit !== str(r.unit)) {
      issues.push({ path: `${p}/unit`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    if (fact.quantumExp !== num(r.quantumExp)) {
      issues.push({ path: `${p}/quantumExp`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    if ((fact.currency ?? '') !== str(r.currency)) {
      issues.push({ path: `${p}/currency`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    if (fact.metered !== (r.metered === true)) {
      issues.push({ path: `${p}/metered`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    if (fact.semanticsRef !== str(r.semanticsRef) || fact.semanticsRevision !== str(r.semanticsRevision)) {
      issues.push({ path: `${p}/semanticsRef`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    // The configured window must BE the verified fact's window (executable
    // equality) — window re-spelling cannot mint a second ledger.
    if (!factWindowMatches(fact, obj(r.window))) {
      issues.push({ path: `${p}/window`, messageKey: 'ant.config.providerFactMetadataMismatch' });
    }
    resolutions.set(str(r.resourceId), fact);
  });
  return resolutions;
}

export function checkEntitlementGroups(doc: Entity, resourceByID: Map<string, Entity>, issues: ConfigIssue[]): void {
  const groups = new Set<string>();
  const groupIdentity = new Set<string>(); // scopeKind|scopeId|metricId|unit
  const evidenceByID = evidenceKindsByID(doc);
  const evidenceEntities = new Map<string, Entity>();
  arr(doc.evidence).forEach((rawEv) => {
    const ev = obj(rawEv);
    evidenceEntities.set(str(ev.evidenceId), ev);
  });
  arr(doc.entitlementGroups).forEach((rawGroup, i) => {
    const g = obj(rawGroup);
    const p = `/entitlementGroups/${i}`;
    const groupId = str(g.groupId);
    if (!validID(groupId) || groups.has(groupId)) {
      issues.push({ path: `${p}/groupId`, messageKey: 'ant.config.invalidOrDuplicateId' });
    }
    groups.add(groupId);
    const scopeKind = str(g.scopeKind);
    if (scopeKind !== 'account' && scopeKind !== 'binding') {
      issues.push({ path: `${p}/scopeKind`, messageKey: 'ant.config.scopeKindInvalid' });
    }
    // scopeId must resolve per scopeKind (R02 edge).
    const scopeId = str(g.scopeId);
    if (scopeKind === 'account' && !idSet(doc.accounts, 'accountId').has(scopeId)) {
      issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.unknownScope' });
    }
    if (scopeKind === 'binding' && !idSet(doc.bindings, 'bindingId').has(scopeId)) {
      issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.unknownScope' });
    }
    if (!validID(str(g.metricId)) || !validUnit(str(g.unit))) {
      issues.push({ path: p, messageKey: 'ant.config.invalidGroupIdentity' });
    }
    const identity = `${scopeKind}|${scopeId}|${str(g.metricId)}|${str(g.unit)}`;
    if (groupIdentity.has(identity)) issues.push({ path: p, messageKey: 'ant.config.duplicateEntitlementGroup' });
    groupIdentity.add(identity);
    const selectorId = str(g.selectorEvidenceId);
    if (selectorId !== '') {
      // The selector evidence must exist, be of the selector kind, AND
      // certify THIS group: subjectRef === groupId (B3/S4 P1-1 — one group's
      // authority evidence must not back another group's order).
      const selector = evidenceEntities.get(selectorId);
      if (selector !== undefined && selector['subjectRef'] !== undefined && str(selector['subjectRef']) !== groupId) {
        issues.push({ path: `${p}/selectorEvidenceId`, messageKey: 'ant.config.evidenceNotApplicable' });
      }
      // The selector evidence must exist and be of the selector kind: it is
      // what makes the group's bucket order authoritative (R02).
      const kind = evidenceByID.get(selectorId);
      if (kind === undefined) {
        issues.push({ path: `${p}/selectorEvidenceId`, messageKey: 'ant.config.unknownEvidence' });
      } else if (kind !== 'entitlement_selector') {
        issues.push({ path: `${p}/selectorEvidenceId`, messageKey: 'ant.config.evidenceSubjectTypeMismatch' });
      }
    }
    const orders = new Set<string>();
    const seen = new Set<string>();
    let first: Entity | undefined;
    arr(g.order).forEach((rawStep, j) => {
      const s = obj(rawStep);
      const sp = `${p}/order/${j}`;
      const resourceId = str(s.resourceId);
      const resource = resourceByID.get(resourceId);
      if (resource === undefined || seen.has(resourceId)) {
        issues.push({ path: `${sp}/resourceId`, messageKey: 'ant.config.unknownOrDuplicateResource' });
        return;
      }
      seen.add(resourceId);
      const orderKey = `${num(s.providerOrder)}`;
      if (orders.has(orderKey)) {
        issues.push({ path: `${sp}/providerOrder`, messageKey: 'ant.config.providerOrderDuplicate' });
      }
      orders.add(orderKey);
      // Group members are the declared independent buckets: same metric/unit
      // as the group, owned by the scope, enrolled in the group, order owned
      // by the group.
      if (str(resource.metricId) !== str(g.metricId) || str(resource.unit) !== str(g.unit)) {
        issues.push({ path: `${sp}/resourceId`, messageKey: 'ant.config.groupMetricUnitMismatch' });
      }
      // Quantity-space equality (B3): same unit TEXT with different precision
      // or currency is not the same quantity space; implicit mixing is
      // forbidden. Group members participate in one waterfall, so they must
      // be directly comparable.
      if (first !== undefined) {
        if (num(first.quantumExp) !== num(resource.quantumExp)) {
          issues.push({ path: `${sp}/resourceId`, messageKey: 'ant.config.groupQuantumMismatch' });
        }
        if (str(first.currency) !== str(resource.currency)) {
          issues.push({ path: `${sp}/resourceId`, messageKey: 'ant.config.groupCurrencyMismatch' });
        }
      } else {
        first = resource;
      }
      if (
        (scopeKind === 'account' || scopeKind === 'binding') &&
        (str(resource.ownerKind) !== scopeKind || str(resource.ownerId) !== scopeId)
      ) {
        issues.push({ path: `${sp}/resourceId`, messageKey: 'ant.config.groupScopeOwnerMismatch' });
      }
      if (str(resource.entitlementGroupId) !== groupId) {
        issues.push({ path: `${sp}/resourceId`, messageKey: 'ant.config.resourceNotInGroup' });
      }
      if (resource.providerOrder !== undefined && resource.providerOrder !== null) {
        issues.push({
          path: `/resources/${resourceIndex(doc, resourceId)}/providerOrder`,
          messageKey: 'ant.config.providerOrderOwnedByGroup',
        });
      }
    });
  });
}

// Resource→group back edge: a resource claiming a group must point at an
// existing group that lists it.
export function checkEntitlementGroupMembership(doc: Entity, issues: ConfigIssue[]): void {
  const groups = new Set(arr(doc.entitlementGroups).map((g) => str(obj(g).groupId)));
  arr(doc.resources).forEach((rawResource, i) => {
    const r = obj(rawResource);
    const claimed = str(r.entitlementGroupId);
    if (claimed === '') return;
    if (!groups.has(claimed)) {
      issues.push({ path: `/resources/${i}/entitlementGroupId`, messageKey: 'ant.config.unknownEntitlementGroup' });
      return;
    }
    const resourceId = str(r.resourceId);
    const listed = arr(doc.entitlementGroups).some((g) =>
      arr(obj(g).order).some((s) => str(obj(s).resourceId) === resourceId && str(obj(g).groupId) === claimed),
    );
    if (!listed) {
      issues.push({ path: `/resources/${i}/entitlementGroupId`, messageKey: 'ant.config.resourceNotInGroup' });
    }
  });
}

export function checkPools(doc: Entity, offers: Set<string>, issues: ConfigIssue[]): void {
  const pools = new Set<string>();
  arr(doc.pools).forEach((rawPool, i) => {
    const pl = obj(rawPool);
    const p = `/pools/${i}`;
    const poolId = str(pl.poolId);
    if (!validID(poolId) || pools.has(poolId)) {
      issues.push({ path: `${p}/poolId`, messageKey: 'ant.config.invalidOrDuplicateId' });
    }
    pools.add(poolId);
    if (str(pl.namespace) === '') {
      issues.push({ path: `${p}/namespace`, messageKey: 'ant.config.namespaceRequired' });
    }
    const members = new Set<string>();
    arr(pl.members).forEach((rawMember, j) => {
      const m = obj(rawMember);
      const mp = `${p}/members/${j}`;
      const offerId = str(m.offerId);
      if (!offers.has(offerId) || members.has(offerId)) {
        issues.push({ path: `${mp}/offerId`, messageKey: 'ant.config.unknownOrDuplicateOffer' });
      }
      members.add(offerId);
      const priority = num(m.priority);
      if (priority < 0 || priority > 2147483647) {
        issues.push({ path: `${mp}/priority`, messageKey: 'ant.config.priorityRange' });
      }
    });
  });
}

export function checkGrants(doc: Entity, issues: ConfigIssue[]): void {
  const seen = new Set<string>();
  const accounts = idSet(doc.accounts, 'accountId');
  const bindings = idSet(doc.bindings, 'bindingId');
  const offers = idSet(doc.offers, 'offerId');
  arr(doc.grants).forEach((rawGrant, i) => {
    const g = obj(rawGrant);
    const p = `/grants/${i}`;
    if (!GRANT_PERMISSIONS.includes(str(g.permission))) {
      issues.push({ path: `${p}/permission`, messageKey: 'ant.config.permissionInvalid' });
    }
    if (!GRANT_SCOPE_KINDS.includes(str(g.scopeKind))) {
      issues.push({ path: `${p}/scopeKind`, messageKey: 'ant.config.scopeKindInvalid' });
    }
    if (str(g.namespace) === '' || !validID(str(g.principalId)) || !validID(str(g.scopeId))) {
      issues.push({ path: p, messageKey: 'ant.config.invalidGrantIdentity' });
    }
    // The scope target must EXIST and belong to the grant's namespace where
    // the target carries one (B3: dangling targets and namespace mismatches
    // are authorization errors, not cosmetic).
    const scopeId = str(g.scopeId);
    switch (str(g.scopeKind)) {
      case 'namespace':
        if (scopeId !== str(g.namespace)) {
          issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.grantScopeNamespaceMismatch' });
        }
        break;
      case 'pool': {
        const pool = arr(doc.pools).map((pl) => obj(pl)).find((pl) => str(pl.poolId) === scopeId);
        if (pool === undefined) {
          issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.unknownGrantScope' });
        } else if (str(pool.namespace) !== str(g.namespace)) {
          issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.grantScopeNamespaceMismatch' });
        }
        break;
      }
      case 'offer':
        if (!offers.has(scopeId)) {
          issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.unknownGrantScope' });
        }
        break;
      case 'binding':
        if (!bindings.has(scopeId)) {
          issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.unknownGrantScope' });
        }
        break;
      case 'account':
        if (!accounts.has(scopeId)) {
          issues.push({ path: `${p}/scopeId`, messageKey: 'ant.config.unknownGrantScope' });
        }
        break;
      default:
        break;
    }
    const key = `${str(g.principalId)}|${str(g.namespace)}|${str(g.permission)}|${str(g.scopeKind)}|${str(g.scopeId)}`;
    if (seen.has(key)) issues.push({ path: p, messageKey: 'ant.config.duplicateGrant' });
    seen.add(key);
  });
}

// Every evidence subject is typed: bound/hard_stop certify an account or
// binding, initial_state a resource, entitlement_selector an existing group,
// guard may target any known identity; resource evidence refs must exist AND
// carry the right kind.
export function checkEvidence(doc: Entity, issues: ConfigIssue[]): void {
  const accounts = idSet(doc.accounts, 'accountId');
  const bindings = idSet(doc.bindings, 'bindingId');
  const resources = idSet(doc.resources, 'resourceId');
  const pools = idSet(doc.pools, 'poolId');
  const offers = idSet(doc.offers, 'offerId');
  const groups = idSet(doc.entitlementGroups, 'groupId');
  // evidenceId -> kind; filled in while iterating exactly like the Go map
  // (a duplicate id is flagged when a previous entry mapped it to a kind).
  const ids = new Map<string, string>();
  arr(doc.evidence).forEach((rawEvidence, i) => {
    const e = obj(rawEvidence);
    const p = `/evidence/${i}`;
    const evidenceId = str(e.evidenceId);
    const existing = ids.get(evidenceId);
    if (!validID(evidenceId) || (existing !== undefined && existing !== '')) {
      issues.push({ path: `${p}/evidenceId`, messageKey: 'ant.config.invalidOrDuplicateId' });
    }
    ids.set(evidenceId, str(e.kind));
    if (!['bound', 'initial_state', 'hard_stop', 'guard', 'entitlement_selector'].includes(str(e.kind))) {
      issues.push({ path: `${p}/kind`, messageKey: 'ant.config.evidenceKindInvalid' });
    }
    if (!validID(str(e.subjectRef)) || !validSourceRef(str(e.sourceRef))) {
      issues.push({ path: p, messageKey: 'ant.config.invalidEvidenceIdentity' });
    }
    if (num(e.expiresAtMillis) <= num(e.verifiedAtMillis)) {
      issues.push({ path: `${p}/expiresAtMillis`, messageKey: 'ant.config.evidenceExpiryInvalid' });
    }
    walkSecretKeys(`${p}/payload`, e.payload, issues);
    const subjectRef = str(e.subjectRef);
    const subjectKnown =
      accounts.has(subjectRef) || bindings.has(subjectRef) || resources.has(subjectRef) ||
      pools.has(subjectRef) || offers.has(subjectRef) || groups.has(subjectRef);
    if (!subjectKnown) {
      issues.push({ path: `${p}/subjectRef`, messageKey: 'ant.config.unknownEvidenceSubject' });
    }
    switch (str(e.kind)) {
      case 'bound':
      case 'hard_stop':
        if (!accounts.has(subjectRef) && !bindings.has(subjectRef)) {
          issues.push({ path: `${p}/subjectRef`, messageKey: 'ant.config.evidenceSubjectTypeMismatch' });
        }
        break;
      case 'initial_state':
        if (!resources.has(subjectRef)) {
          issues.push({ path: `${p}/subjectRef`, messageKey: 'ant.config.evidenceSubjectTypeMismatch' });
        }
        break;
      case 'entitlement_selector':
        if (!groups.has(subjectRef)) {
          issues.push({ path: `${p}/subjectRef`, messageKey: 'ant.config.evidenceSubjectTypeMismatch' });
        }
        break;
      default:
        break;
    }
  });
  // Resource evidence applicability (B3): the evidence must EXIST, carry the
  // right kind, AND actually support this resource's assertion — correct
  // subject CLASS alone is not enough.
  const bindingAccount = new Map<string, string>(); // every binding, like Go
  for (const rawBinding of arr(doc.bindings)) {
    const b = obj(rawBinding);
    bindingAccount.set(str(b.bindingId), str(b.accountId));
  }
  const evidenceByID = new Map<string, Entity>(); // first occurrence wins (Go byIndex)
  for (const rawEvidence of arr(doc.evidence)) {
    const e = obj(rawEvidence);
    const id = str(e.evidenceId);
    if (!evidenceByID.has(id)) evidenceByID.set(id, e);
  }
  const payloadOf = (e: Entity): Entity => obj(e.payload);
  arr(doc.resources).forEach((rawResource, i) => {
    const r = obj(rawResource);
    const p = `/resources/${i}`;
    const capacityRef = str(r.capacityEvidenceRef);
    if (capacityRef !== '') {
      const kind = ids.get(capacityRef);
      if (kind === undefined) {
        issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.unknownEvidence' });
      } else if (kind !== 'bound' && kind !== 'hard_stop') {
        issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceSubjectTypeMismatch' });
      } else {
        const ev = evidenceByID.get(capacityRef);
        if (ev !== undefined) {
          // Capacity evidence certifies the OWNER: account-owned resources
          // need the owner account exactly; binding-owned resources accept
          // their account's evidence (account covers its bindings). Metric
          // and unit must match the resource — a concurrency bound cannot
          // back a token limit.
          const ownerId = str(r.ownerId);
          let subjectOK = str(ev.subjectRef) === ownerId;
          if (!subjectOK && str(r.ownerKind) === 'binding' && (bindingAccount.get(ownerId) ?? '') !== '') {
            subjectOK = str(ev.subjectRef) === bindingAccount.get(ownerId);
          }
          if (!subjectOK) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
          const payload = payloadOf(ev);
          // Metric is REQUIRED on capacity evidence (S4 P2-2): an owner
          // holds many metrics — dimensionless evidence supports nothing.
          if (!Object.hasOwn(payload, 'metricId')) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          } else if (str(payload['metricId']) !== str(r.metricId)) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
          if (Object.hasOwn(payload, 'unit') && str(payload['unit']) !== str(r.unit)) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
          // Explicit dimension claims must agree with the resource declaration
          // (Q1/C2: wrong precision, currency, window and rule version are
          // incompatible, not ignorable).
          if (Object.hasOwn(payload, 'quantumExp')) {
            const claimedQuantum = parseQuantity(payload['quantumExp']);
            if (claimedQuantum === null || Number(claimedQuantum) !== num(r.quantumExp)) {
              issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
            }
          }
          if (Object.hasOwn(payload, 'currency') && str(payload['currency']) !== str(r.currency)) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
          if (Object.hasOwn(payload, 'windowKind') && str(payload['windowKind']) !== str(obj(r.window).kind)) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
          if (
            Object.hasOwn(payload, 'windowDurationMillis') &&
            str(payload['windowDurationMillis']) !== String(num(obj(r.window).durationMillis))
          ) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
          if (
            Object.hasOwn(payload, 'semanticsRevision') &&
            str(payload['semanticsRevision']) !== str(r.semanticsRevision)
          ) {
            issues.push({ path: `${p}/capacityEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
        }
      }
    }
    const initialRef = str(r.initialStateEvidenceRef);
    if (initialRef !== '') {
      const kind = ids.get(initialRef);
      if (kind === undefined) {
        issues.push({ path: `${p}/initialStateEvidenceRef`, messageKey: 'ant.config.unknownEvidence' });
      } else if (kind !== 'initial_state') {
        issues.push({ path: `${p}/initialStateEvidenceRef`, messageKey: 'ant.config.evidenceSubjectTypeMismatch' });
      } else {
        const ev = evidenceByID.get(initialRef);
        if (ev !== undefined) {
          // Initial state is THE resource's own opening balance: the subject
          // must be the resource itself and the metric must match (a USD
          // opening balance cannot initialize a token resource).
          if (str(ev.subjectRef) !== str(r.resourceId)) {
            issues.push({ path: `${p}/initialStateEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
          const payload = payloadOf(ev);
          if (Object.hasOwn(payload, 'metricId') && str(payload['metricId']) !== str(r.metricId)) {
            issues.push({ path: `${p}/initialStateEvidenceRef`, messageKey: 'ant.config.evidenceNotApplicable' });
          }
        }
      }
    }
  });
  // Group selector evidence applicability (B3): the selector's metric, when
  // declared, must match the group's metric.
  arr(doc.entitlementGroups).forEach((rawGroup, i) => {
    const g = obj(rawGroup);
    const selectorId = str(g.selectorEvidenceId);
    if (selectorId === '') return;
    const ev = evidenceByID.get(selectorId);
    if (ev !== undefined) {
      const payload = payloadOf(ev);
      if (Object.hasOwn(payload, 'metricId') && str(payload['metricId']) !== str(g.metricId)) {
        issues.push({
          path: `/entitlementGroups/${i}/selectorEvidenceId`,
          messageKey: 'ant.config.evidenceNotApplicable',
        });
      }
    }
  });
}
