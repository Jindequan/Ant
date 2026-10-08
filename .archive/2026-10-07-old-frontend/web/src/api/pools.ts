import { apiJson } from './client';
import { newCommandId } from './config';
import type { PoolView } from './types';

export function readPools(): Promise<PoolView[]> {
  return apiJson<PoolView[]>('/v1/pools');
}

export function reorderPool(
  pool: PoolView['pool'],
  configRevision: string,
  offerIds: string[],
  csrfToken: string,
): Promise<PoolView> {
  return apiJson<PoolView>(`/v1/pools/${encodeURIComponent(pool.poolId)}/order`, {
    method: 'PUT',
    csrfToken,
    headers: { 'If-Match': `"${pool.version}"` },
    body: {
      commandId: newCommandId(),
      expectedPoolVersion: pool.version,
      expectedConfigRevision: configRevision,
      entries: offerIds.map((offerId, index) => ({ offerId, priority: (index + 1) * 10 })),
    },
  });
}
