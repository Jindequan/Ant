import { ApiError, apiJson } from './client';
import type { ConfigDocument } from './types';
import type { components } from './schema';

export type ProviderDescriptorView = components['schemas']['ProviderDescriptor'];
export type SecretMetadataView = components['schemas']['SecretMetadata'];
export type VerifiedScopeView = components['schemas']['VerifiedScope'];
export type VerifiedResourceFactView = components['schemas']['VerifiedResourceFact'];
export type VerifiedScopeCatalogView = components['schemas']['VerifiedScopeCatalogView'];
export type DeepSeekBalanceObservationView = components['schemas']['DeepSeekBalanceObservation'];

export function readProviderCatalog(): Promise<ProviderDescriptorView[]> {
  return apiJson<ProviderDescriptorView[]>('/v1/catalog/providers');
}

export function readProviderScopes(): Promise<VerifiedScopeCatalogView> {
  return apiJson<VerifiedScopeCatalogView>('/v1/catalog/scopes');
}

export function observeDeepSeekBalance(input: components['schemas']['DeepSeekBalanceObserveInput'], csrfToken: string, signal?: AbortSignal): Promise<DeepSeekBalanceObservationView> {
  return apiJson<DeepSeekBalanceObservationView>('/v1/providers/deepseek/balance-observations', {
    method: 'POST', csrfToken, body: input, signal,
  });
}

export function readDeepSeekBalanceObservation(commandId: string, signal?: AbortSignal): Promise<DeepSeekBalanceObservationView> {
  return apiJson<DeepSeekBalanceObservationView>(`/v1/providers/deepseek/balance-observations/${encodeURIComponent(commandId)}`, { signal });
}

export function writeSecret(value: string, csrfToken: string, commandId = newCommandId()): Promise<SecretMetadataView> {
  return apiJson<SecretMetadataView>('/v1/secrets', {
    method: 'POST', csrfToken, body: { commandId, value },
  });
}

export function rotateSecret(ref: string, expectedRevision: string, value: string, csrfToken: string, commandId = newCommandId()): Promise<SecretMetadataView> {
  return apiJson<SecretMetadataView>(`/v1/secrets/${encodeURIComponent(ref)}/rotate`, {
    method: 'POST', csrfToken, body: { commandId, expectedRevision, value },
  });
}

export interface ConfigDraftView {
  draftId: string;
  baseRevision: string;
  version: number;
  status: 'editing' | 'validated' | 'activated' | 'archived';
  definition: ConfigDocument;
  fieldIssues: Array<{ path: string; messageKey: string }>;
}

export interface ConfigProjectionView {
  revision: string;
  version: number;
  document: ConfigDocument;
}

export interface ActivationView {
  configRevision: string;
  controlVersion: number;
}

export const emptyConfigDocument = (): ConfigDocument => ({
  schemaVersion: 1,
  accounts: [],
  bindings: [],
  resources: [],
  entitlementGroups: [],
  offers: [],
  pools: [],
  grants: [],
  evidence: [],
  unsupportedDrafts: [],
});

export function newCommandId(): string {
  return crypto.randomUUID();
}

export async function readActiveConfig(): Promise<ConfigProjectionView | null> {
  try {
    return await apiJson<ConfigProjectionView>('/v1/config');
  } catch (error) {
    if (error instanceof ApiError && error.status === 404 && error.envelope.error.code === 'NotFound') return null;
    throw error;
  }
}

export function readConfigDraft(draftId: string): Promise<ConfigDraftView> {
  return apiJson<ConfigDraftView>(`/v1/config/drafts/${encodeURIComponent(draftId)}`);
}

export function createConfigDraft(definition: ConfigDocument, csrfToken: string, baseRevision = ''): Promise<ConfigDraftView> {
  return apiJson<ConfigDraftView>('/v1/config/drafts', {
    method: 'POST',
    csrfToken,
    body: { commandId: newCommandId(), draftId: newCommandId(), baseRevision: baseRevision || undefined, definition },
  });
}

export function saveConfigDraft(draft: ConfigDraftView, definition: ConfigDocument, csrfToken: string): Promise<ConfigDraftView> {
  return apiJson<ConfigDraftView>(`/v1/config/drafts/${encodeURIComponent(draft.draftId)}`, {
    method: 'PATCH',
    csrfToken,
    headers: { 'If-Match': `"${draft.version}"` },
    body: { commandId: newCommandId(), expectedDraftVersion: draft.version, definition },
  });
}

export function validateConfigDraft(draft: ConfigDraftView, csrfToken: string): Promise<ConfigDraftView> {
  return apiJson<ConfigDraftView>(`/v1/config/drafts/${encodeURIComponent(draft.draftId)}/validate`, {
    method: 'POST',
    csrfToken,
    headers: { 'If-Match': `"${draft.version}"` },
  });
}

export function activateConfigDraft(draft: ConfigDraftView, expectedConfigRevision: string, csrfToken: string): Promise<ActivationView> {
  return apiJson<ActivationView>(`/v1/config/drafts/${encodeURIComponent(draft.draftId)}/activate`, {
    method: 'POST',
    csrfToken,
    body: {
      commandId: newCommandId(),
      draftId: draft.draftId,
      expectedConfigRevision,
      expectedDraftVersion: draft.version,
    },
  });
}
