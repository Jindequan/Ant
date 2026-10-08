import { apiJson } from './client';
import type { components } from './schema';

export type ObservationRefreshInput = components['schemas']['ObservationRefreshInput'];
export type ObservationRefreshResult = components['schemas']['RefreshResult'];
export type AdjustmentInput = components['schemas']['AdjustmentInput'];
export type ClearHoldInput = components['schemas']['ClearHoldInput'];
export type AdminCommandAck = components['schemas']['CommandAck'];

export function adjustResource(resourceId: string, input: AdjustmentInput, csrfToken: string): Promise<AdminCommandAck> {
  return apiJson<AdminCommandAck>(`/v1/resources/${encodeURIComponent(resourceId)}/adjustments`, { method: 'POST', csrfToken, body: input });
}

export function clearManagedHold(holdId: string, input: ClearHoldInput, csrfToken: string): Promise<AdminCommandAck> {
  return apiJson<AdminCommandAck>(`/v1/holds/${encodeURIComponent(holdId)}/clear`, { method: 'POST', csrfToken, body: input });
}

export function refreshResourceObservation(
  resourceId: string,
  input: ObservationRefreshInput,
  csrfToken: string,
): Promise<ObservationRefreshResult> {
  return apiJson<ObservationRefreshResult>(`/v1/resources/${encodeURIComponent(resourceId)}/observations`, {
    method: 'POST', csrfToken, body: input,
  });
}
