import { apiJson, resolveBaseUrl } from './client';
import { newCommandId } from './config';
import type { components } from './schema';

export type OperationView = components['schemas']['OperationView'];
export type OperationPageView = components['schemas']['OperationPage'];
export type CancellationAckView = components['schemas']['CancellationAck'];
export type OutputPageView = components['schemas']['OutputPage'];
export type ExecuteInput = components['schemas']['ExecuteInput'];
export type SubmitReply = components['schemas']['SubmitReply'];
export type ReconciliationInput = components['schemas']['ReconciliationInput'];
export type ReconciliationView = components['schemas']['ReconciliationView'];
export type DecisionView = components['schemas']['DecisionView'];

export async function submitOperation(poolId: string, input: ExecuteInput, csrfToken: string): Promise<SubmitReply> {
  const reply = await apiJson<SubmitReply>(`/v1/pools/${encodeURIComponent(poolId)}/operations`, {
    method: 'POST', csrfToken, body: input,
  });
  const { validateSubmitReply } = await import('./validate');
  const validation = validateSubmitReply(reply);
  if (!validation.ok) throw new Error('Invalid operation submission reply');
  return reply;
}

export function readOperations(cursor = '', limit = 50, state: OperationView['state'] | '' = ''): Promise<OperationPageView> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (cursor) query.set('cursor', cursor);
  if (state) query.set('state', state);
  return apiJson<OperationPageView>(`/v1/operations?${query.toString()}`);
}

export function readOperation(operationId: string): Promise<OperationView> {
  return apiJson<OperationView>(`/v1/operations/${encodeURIComponent(operationId)}`);
}

export function readOperationDecisions(operationId: string): Promise<DecisionView[]> {
  return apiJson<DecisionView[]>(`/v1/operations/${encodeURIComponent(operationId)}/decisions`);
}

export function readOperationOutput(operationId: string, afterSequence = 0): Promise<OutputPageView> {
  const query = new URLSearchParams({ afterSequence: String(afterSequence), limit: '50' });
  return apiJson<OutputPageView>(`/v1/operations/${encodeURIComponent(operationId)}/output?${query.toString()}`);
}

export function cancelOperation(operationId: string, csrfToken: string, commandId = newCommandId()): Promise<CancellationAckView> {
  return apiJson<CancellationAckView>(`/v1/operations/${encodeURIComponent(operationId)}/cancel`, {
    method: 'POST', csrfToken, body: { commandId },
  });
}

export function reconcileAttempt(attemptId: string, input: ReconciliationInput, csrfToken: string): Promise<ReconciliationView> {
  return apiJson<ReconciliationView>(`/v1/attempts/${encodeURIComponent(attemptId)}/reconciliations`, {
    method: 'POST', csrfToken, body: input,
  });
}

export function operationEventsUrl(operationId: string): string {
  return `${resolveBaseUrl()}/v1/operations/${encodeURIComponent(operationId)}/events`;
}
