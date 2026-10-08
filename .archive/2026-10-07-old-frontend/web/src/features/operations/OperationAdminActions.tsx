import { useRef, useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError } from '../../api/client';
import { newCommandId } from '../../api/config';
import { adjustResource, clearManagedHold, type AdjustmentInput, type ClearHoldInput } from '../../api/resources';
import type { OperationView } from '../../api/operations';
import { useSession } from '../../auth/SessionContext';
import { t } from '../../i18n';

export function OperationAdminActions({ operation }: { operation: OperationView }) {
  const { session } = useSession();
  const queryClient = useQueryClient();
  const [adjustmentEventId, setAdjustmentEventId] = useState('');
  const [adjustmentDelta, setAdjustmentDelta] = useState('');
  const [adjustmentEvidence, setAdjustmentEvidence] = useState('');
  const [holdEvidence, setHoldEvidence] = useState<Record<string, string>>({});
  const adjustmentCommand = useRef<{ key: string; body: AdjustmentInput } | null>(null);
  const holdCommands = useRef<Record<string, { key: string; body: ClearHoldInput }>>({});
  const adjustmentMutation = useMutation({
    mutationFn: (input: { resourceId: string; body: AdjustmentInput; csrfToken: string }) => adjustResource(input.resourceId, input.body, input.csrfToken),
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        adjustmentCommand.current = null;
        void queryClient.invalidateQueries({ queryKey: ['operation', operation.operationId] });
        void queryClient.invalidateQueries({ queryKey: ['pools'] });
      }
    },
    onSuccess: async () => {
      adjustmentCommand.current = null;
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['operation', operation.operationId] }),
        queryClient.invalidateQueries({ queryKey: ['operations'] }),
        queryClient.invalidateQueries({ queryKey: ['pools'] }),
      ]);
    },
  });
  const holdMutation = useMutation({
    mutationFn: (input: { holdId: string; body: ClearHoldInput; csrfToken: string }) => clearManagedHold(input.holdId, input.body, input.csrfToken),
    onError: (error, input) => {
      if (error instanceof ApiError && error.status === 409) {
        delete holdCommands.current[input.holdId];
        void queryClient.invalidateQueries({ queryKey: ['operation', operation.operationId] });
        void queryClient.invalidateQueries({ queryKey: ['pools'] });
      }
    },
    onSuccess: async (_ack, input) => {
      delete holdCommands.current[input.holdId];
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['operation', operation.operationId] }),
        queryClient.invalidateQueries({ queryKey: ['operations'] }),
        queryClient.invalidateQueries({ queryKey: ['pools'] }),
      ]);
    },
  });
  const charges = operation.adjustableCharges ?? [];
  const holds = operation.managedHolds ?? [];
  const selectedCharge = charges.find((item) => item.eventId === adjustmentEventId);
  const deltaMatches = /^-?(0|[1-9][0-9]{0,18})$/.test(adjustmentDelta) && adjustmentDelta !== '0';
  const deltaExceedsReversible = Boolean(selectedCharge && deltaMatches && BigInt(adjustmentDelta) < 0n &&
    (BigInt(adjustmentDelta) === (-9223372036854775808n) || -BigInt(adjustmentDelta) > BigInt(selectedCharge.remainingReversible)));
  if (charges.length === 0 && holds.length === 0) return null;

  function submitAdjustment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const charge = charges.find((item) => item.eventId === adjustmentEventId);
    if (!session?.csrfToken || !session.permissions.includes('manage') || adjustmentMutation.isPending || !charge || !/^-?(0|[1-9][0-9]{0,18})$/.test(adjustmentDelta) || adjustmentDelta === '0') return;
    const delta = BigInt(adjustmentDelta);
    if (delta < 0n && (delta === (-9223372036854775808n) || -delta > BigInt(charge.remainingReversible))) return;
    if (!adjustmentEvidence.trim()) return;
    const evidenceRefs = [...new Set(adjustmentEvidence.split(/[\n,]/).map((value) => value.trim()).filter(Boolean))];
    const key = JSON.stringify([charge.resourceId, charge.windowKey, adjustmentDelta, delta < 0n ? charge.eventId : '', evidenceRefs]);
    let body = adjustmentCommand.current?.key === key ? adjustmentCommand.current.body : undefined;
    if (!body) {
      body = { commandId: newCommandId(), windowKey: charge.windowKey, delta: adjustmentDelta, ...(delta < 0n ? { originalEventId: charge.eventId } : {}), evidenceRefs };
      adjustmentCommand.current = { key, body };
    }
    adjustmentMutation.mutate({ resourceId: charge.resourceId, body, csrfToken: session.csrfToken });
  }

  function submitHoldClear(holdId: string, evidenceText: string) {
    if (!session?.csrfToken || !session.permissions.includes('manage') || holdMutation.isPending) return;
    const evidenceIds = [...new Set(evidenceText.split(/[\n,]/).map((value) => value.trim()).filter(Boolean))];
    if (!evidenceIds.length) return;
    const key = JSON.stringify(evidenceIds);
    let body = holdCommands.current[holdId]?.key === key ? holdCommands.current[holdId].body : undefined;
    if (!body) {
      body = { commandId: newCommandId(), evidenceIds };
      holdCommands.current[holdId] = { key, body };
    }
    holdMutation.mutate({ holdId, body, csrfToken: session.csrfToken });
  }

  return <section className="config-state operation-section">
    <h2>{t('operations.admin.title')}</h2>
    {charges.length ? <><h3>{t('operations.admin.adjustmentTitle')}</h3><ul>{charges.map((charge) => <li key={charge.eventId}>{charge.metricId} · {charge.remainingReversible} {charge.unit} · {charge.eventId}</li>)}</ul>
      <form className="operation-form" onSubmit={submitAdjustment}>
        <label>{t('operations.admin.charge')}<select value={adjustmentEventId} onChange={(event) => setAdjustmentEventId(event.target.value)} required><option value="">{t('operations.reconcile.choose')}</option>{charges.map((charge) => <option key={charge.eventId} value={charge.eventId}>{charge.metricId} · {charge.remainingReversible} {charge.unit} · {charge.eventId}</option>)}</select></label>
        <label>{t('operations.admin.delta')}<input type="text" inputMode="numeric" pattern="^-?(0|[1-9][0-9]{0,18})$" value={adjustmentDelta} onChange={(event) => setAdjustmentDelta(event.target.value)} required /></label>
        <label>{t('operations.admin.evidence')}<textarea value={adjustmentEvidence} onChange={(event) => setAdjustmentEvidence(event.target.value)} required rows={2} /></label>
        <p className="config-note">{t('operations.admin.adjustmentHelp')}</p>
        {deltaExceedsReversible ? <p role="alert" className="config-alert">{t('operations.admin.deltaTooLarge', { amount: selectedCharge?.remainingReversible ?? '0' })}</p> : null}
        <button className="button-primary" type="submit" disabled={adjustmentMutation.isPending || !adjustmentEventId || !deltaMatches || deltaExceedsReversible || !adjustmentEvidence.trim()}>{adjustmentMutation.isPending ? t('operations.admin.sending') : t('operations.admin.adjust')}</button>
        {adjustmentMutation.data ? <p role="status" aria-live="polite">{t(`operations.admin.status.${adjustmentMutation.data.status}`)}</p> : null}
        {adjustmentMutation.error ? <p role="alert" className="config-alert">{adjustmentMutation.error instanceof ApiError && adjustmentMutation.error.status === 409 ? t('operations.admin.conflict') : t('config.error.request')}</p> : null}
      </form></> : null}
    {holds.length ? <><h3>{t('operations.admin.holdsTitle')}</h3>{holds.map((hold) => <div className="config-state" key={`${hold.holdId}:${hold.resourceId}`}>
      <p><strong>{hold.reason}</strong> · {hold.scopeKind}:{hold.scopeId} · {hold.resourceId}</p>
      {hold.evidenceRefs.length ? <p>{t('operations.admin.existingEvidence')}: {hold.evidenceRefs.join(', ')}</p> : null}
      {!hold.canClear ? <p className="config-note">{t('operations.admin.holdBlocked')}</p> : null}
      <label>{t('operations.admin.clearEvidence')}<textarea value={holdEvidence[hold.holdId] ?? ''} onChange={(event) => setHoldEvidence((previous) => ({ ...previous, [hold.holdId]: event.target.value }))} rows={2} /></label>
      <button type="button" className="button-secondary" disabled={!hold.canClear || holdMutation.isPending || !(holdEvidence[hold.holdId] ?? '').trim()} onClick={() => submitHoldClear(hold.holdId, holdEvidence[hold.holdId] ?? '')}>{holdMutation.isPending && holdMutation.variables?.holdId === hold.holdId ? t('operations.admin.sending') : t('operations.admin.clear')}</button>
      {holdMutation.data && holdMutation.variables?.holdId === hold.holdId ? <p role="status" aria-live="polite">{t(`operations.admin.status.${holdMutation.data.status}`)}</p> : null}
      {holdMutation.error && holdMutation.variables?.holdId === hold.holdId ? <p role="alert" className="config-alert">{holdMutation.error instanceof ApiError && holdMutation.error.status === 409 ? t('operations.admin.conflict') : t('config.error.request')}</p> : null}
    </div>)}</> : null}
  </section>;
}
