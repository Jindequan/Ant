import type { ConfigDocument, Resource, Window } from '../../api/types';
import {
  normalizeWindowRuleIdentity,
  windowRuleOf,
  parseQuantity,
  validID,
  validUnit,
  isRecord,
} from '../../api/config-helpers';

/** References must be repaired explicitly; deleting a resource never rewrites routing. */
export function resourceReferences(document: ConfigDocument, resourceId: string): string[] {
  return [
    ...document.bindings.filter((item) => item.resourceRefs.includes(resourceId)).map((item) => item.bindingId),
    ...document.offers.filter((item) => item.resourceRefs.includes(resourceId)).map((item) => item.offerId),
    ...document.entitlementGroups
      .filter((item) => item.order.some((step) => step.resourceId === resourceId))
      .map((item) => item.groupId),
  ];
}

export function validResourceInput(resource: Resource): boolean {
  const capacity = parseQuantity(resource.configuredCapacity);
  return (
    validID(resource.resourceId) &&
    validID(resource.ownerId) &&
    validID(resource.metricId) &&
    validUnit(resource.unit) &&
    capacity !== null &&
    capacity >= 0n &&
    Number.isInteger(resource.quantumExp) &&
    resource.quantumExp >= 0 &&
    resource.quantumExp <= 18 &&
    normalizeWindowRuleIdentity(windowRuleOf(resource.window as unknown as Record<string, unknown>)) !== null &&
    (resource.window.kind !== 'rolling' || Number.isSafeInteger(resource.window.durationMillis)) &&
    (!resource.currency || /^[A-Z]{3}$/.test(resource.currency)) &&
    (resource.expiresAtMillis === undefined ||
      (Number.isSafeInteger(resource.expiresAtMillis) && resource.expiresAtMillis > 0))
  );
}

export function declaredResource(input: Resource, previous?: Resource): Resource {
  // Changing a declaration cannot carry forward provider verification of the old rule.
  // Keep evidence records in the document, but remove claims from this resource.
  const { providerFactId: _fact, initialStateEvidenceRef: _initial, capacityEvidenceRef: _bound, ...fields } = input;
  return { ...fields, resourceId: previous?.resourceId ?? input.resourceId, capacityEvidenceRef: '' };
}

export function draftWindow(
  kind: Window['kind'],
  period: string,
  timeZone: string,
  anchorISO: string,
  duration: string,
): Window {
  if (kind === 'lifetime') return { kind };
  if (kind === 'rolling') return { kind, durationMillis: Number(duration) };
  return {
    kind,
    period: period as Window['period'],
    timeZone,
    ...(anchorISO.trim() ? { anchorISO: anchorISO.trim() } : {}),
  };
}

/** A saved invalid JSON draft must route to repair rather than crash this form. */
export function renderableResourceDraft(document: ConfigDocument): boolean {
  return (
    [document.accounts, document.bindings, document.resources, document.offers, document.entitlementGroups].every(
      Array.isArray,
    ) &&
    document.accounts.every(
      (item) =>
        isRecord(item) &&
        typeof item.accountId === 'string' &&
        typeof item.displayName === 'string' &&
        typeof item.providerTypeId === 'string',
    ) &&
    document.bindings.every(
      (item) =>
        isRecord(item) &&
        typeof item.bindingId === 'string' &&
        typeof item.accountId === 'string' &&
        Array.isArray(item.resourceRefs),
    ) &&
    document.offers.every(
      (item) => isRecord(item) && typeof item.offerId === 'string' && Array.isArray(item.resourceRefs),
    ) &&
    document.entitlementGroups.every(
      (item) =>
        isRecord(item) &&
        typeof item.groupId === 'string' &&
        Array.isArray(item.order) &&
        item.order.every((step) => isRecord(step) && typeof step.resourceId === 'string'),
    ) &&
    document.resources.every(
      (item) =>
        isRecord(item) &&
        typeof item.resourceId === 'string' &&
        typeof item.ownerId === 'string' &&
        typeof item.metricId === 'string' &&
        typeof item.unit === 'string' &&
        typeof item.configuredCapacity === 'string' &&
        isRecord(item.window) &&
        typeof item.window.kind === 'string',
    )
  );
}

/** Date.parse normalizes impossible dates; declarations must retain the user's intended date. */
export function declaredTimestamp(value: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.\d{1,3})?)?(Z|[+-](\d{2}):(\d{2}))$/.exec(
    value,
  );
  if (!match) return null;
  const year = Number(match[1]),
    month = Number(match[2]),
    day = Number(match[3]);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > days[month - 1]! ||
    Number(match[4]) > 23 ||
    Number(match[5]) > 59 ||
    Number(match[6] ?? 0) > 59 ||
    Number(match[8] ?? 0) > 23 ||
    Number(match[9] ?? 0) > 59
  )
    return null;
  const millis = Date.parse(value);
  return Number.isSafeInteger(millis) ? millis : null;
}
