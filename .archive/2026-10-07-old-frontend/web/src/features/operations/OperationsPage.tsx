import { lazy, Suspense, useEffect, useRef, useState, type FormEvent } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { ApiError } from '../../api/client';
import { cancelOperation, operationEventsUrl, readOperation, readOperationDecisions, readOperationOutput, readOperations, reconcileAttempt, submitOperation, type OperationView, type ReconciliationInput } from '../../api/operations';
import { newCommandId } from '../../api/config';
import { readPools } from '../../api/pools';
import { useSession } from '../../auth/SessionContext';
import { PageStateFrame } from '../../components/PageState';
import { t } from '../../i18n';

const OperationAdminActions = lazy(() => import('./OperationAdminActions').then((module) => ({ default: module.OperationAdminActions })));

function errorText(error: unknown): string {
  if (error instanceof ApiError && error.status === 401) return t('config.error.session');
  if (error instanceof ApiError && error.status === 403) return t('config.error.permission');
  if (error instanceof ApiError && error.status === 404) return t('operations.error.notFound');
  return t('config.error.request');
}

function localizedMessage(prefix: string, value: string, fallbackKey: string): string {
  if (!value) return t(fallbackKey);
  const key = `${prefix}.${value}`;
  const message = t(key);
  return message === key ? t(fallbackKey) : message;
}

function resultSummary(code: string): string {
  return localizedMessage('operations.result.summary', code, 'operations.result.summary.unknown');
}

function requestDomainSummary(domain: string | undefined): string {
  return localizedMessage('operations.domain', domain ?? '', 'operations.domain.unknown');
}

function reasonSummary(group: string, code: string): string {
  const key = `operations.reason.${group}.${code}`;
  const message = t(key);
  return message === key ? t('operations.reason.unknown') : message;
}

function actionSummary(action: string): string {
  return localizedMessage('operations.action', action, 'operations.action.unknown');
}

function consumptionBasisSummary(basis: string): string {
  return localizedMessage('operations.basis', basis, 'operations.basis.unknown');
}

const operationListStates: OperationView['state'][] = ['running', 'succeeded', 'failed', 'cancelled', 'unknown'];

function operationListUrl(cursor: string, state: string): string {
  const params = new URLSearchParams();
  if (cursor) params.set('cursor', cursor);
  if (state) params.set('state', state);
  const query = params.toString();
  return query ? `/operations?${query}` : '/operations';
}

function outputText(pages: Array<{ chunks: Array<{ data: string }> }>): string {
  const parts = pages.flatMap((page) => page.chunks.map((chunk) => {
    const binary = atob(chunk.data);
    return Uint8Array.from(binary, (character) => character.charCodeAt(0));
  }));
  const size = parts.reduce((total, part) => total + part.length, 0);
  const joined = new Uint8Array(size);
  let offset = 0;
  for (const part of parts) { joined.set(part, offset); offset += part.length; }
  return new TextDecoder().decode(joined);
}

export function OperationsPage() {
  const { session, isLoading: sessionLoading } = useSession();
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const operationId = searchParams.get('operation') ?? '';
  const cursor = searchParams.get('cursor') ?? '';
  const returnCursor = searchParams.get('returnCursor') ?? '';
  const returnState = searchParams.get('returnState') ?? '';
  const rawStateFilter = searchParams.get('state') ?? '';
  const stateFilter = operationListStates.find((state) => state === rawStateFilter) ?? '';
  const operationsListHref = operationListUrl(returnCursor, returnState);
  const [streamStatus, setStreamStatus] = useState<'idle' | 'connecting' | 'connected' | 'reconnecting' | 'closed' | 'unavailable'>('idle');
  const [poolId, setPoolId] = useState('');
  const [model, setModel] = useState('model-x');
  const [prompt, setPrompt] = useState('');
  const [maxTokens, setMaxTokens] = useState(128);
  const [allowMetered, setAllowMetered] = useState(false);
  const [reconcileAmounts, setReconcileAmounts] = useState<Record<string, string>>({});
  const [reconcileOutcome, setReconcileOutcome] = useState('');
  const [reconcileRetrySafety, setReconcileRetrySafety] = useState('');
  const [reconcileEvidence, setReconcileEvidence] = useState('');
  const canRead = session?.permissions.some((permission) => permission === 'read' || permission === 'configure') ?? false;
  const canExecute = session?.permissions.includes('execute') ?? false;
  const canManage = session?.permissions.includes('manage') ?? false;
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: readPools, enabled: canRead });
  const pools = (poolsQuery.data ?? []).filter((pool) => pool.pool.enabled && pool.groups.some((group) => group.domain === 'ai/text' && group.capabilityProfile === 'profile.chat.basic'));
  const selectedPool = pools.find((pool) => pool.pool.poolId === poolId) ?? pools[0];
  const pendingSubmission = useRef<{ key: string; body: Parameters<typeof submitOperation>[1] } | null>(null);
  const submitMutation = useMutation({
    mutationFn: (input: { poolId: string; body: Parameters<typeof submitOperation>[1]; csrfToken: string }) => submitOperation(input.poolId, input.body, input.csrfToken),
    onSuccess: async (reply) => {
      if (pendingSubmission.current?.body.operationId === reply.operationId) pendingSubmission.current = null;
      if (reply.kind === 'rejected') return;
      await Promise.all([queryClient.invalidateQueries({ queryKey: ['operations'] }), queryClient.invalidateQueries({ queryKey: ['operation', reply.operationId] })]);
      setSearchParams({ operation: reply.operationId });
    },
  });
  const cancelCommand = useRef<{ operationId: string; commandId: string } | null>(null);
  const reconcileCommand = useRef<{ attemptId: string; commandId: string } | null>(null);
  const cancelMutation = useMutation({
    mutationFn: (input: { operationId: string; commandId: string; csrfToken: string }) => cancelOperation(input.operationId, input.csrfToken, input.commandId),
    onError: (error, input) => {
      if (error instanceof ApiError && error.status === 409 && cancelCommand.current?.operationId === input.operationId) {
        cancelCommand.current = null;
      }
    },
    onSuccess: async (_ack, input) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['operation', input.operationId] }),
        queryClient.invalidateQueries({ queryKey: ['operations'] }),
      ]);
    },
  });
  const reconcileMutation = useMutation({
    mutationFn: (input: { attemptId: string; body: ReconciliationInput; csrfToken: string }) => reconcileAttempt(input.attemptId, input.body, input.csrfToken),
    onError: (error, variables) => {
      if (error instanceof ApiError && error.status === 409 && reconcileCommand.current?.attemptId === variables.attemptId) reconcileCommand.current = null;
    },
    onSuccess: async () => {
      reconcileCommand.current = null;
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['operation', operationId] }),
        queryClient.invalidateQueries({ queryKey: ['operations'] }),
      ]);
    },
  });
  const listQuery = useQuery({
    queryKey: ['operations', stateFilter, cursor],
    queryFn: () => readOperations(cursor, 50, stateFilter),
    enabled: canRead && operationId === '',
    refetchInterval: (query) => (query.state.data?.operations ?? []).some((operation) => operation.state === 'running') ? 5_000 : false,
  });
  const detailQuery = useQuery({
    queryKey: ['operation', operationId],
    queryFn: () => readOperation(operationId),
    enabled: canRead && operationId !== '',
    refetchInterval: (query) => query.state.data?.state === 'running' && streamStatus !== 'connected' ? 5_000 : false,
  });
  const decisionsQuery = useQuery({
    queryKey: ['operation-decisions', operationId],
    queryFn: () => readOperationDecisions(operationId),
    enabled: canRead && operationId !== '',
    refetchInterval: () => detailQuery.data?.state === 'running' && streamStatus !== 'connected' ? 5_000 : false,
  });
  const outputQuery = useInfiniteQuery({
    queryKey: ['operation-output', operationId],
    queryFn: ({ pageParam }) => readOperationOutput(operationId, pageParam),
    initialPageParam: 0,
    getNextPageParam: (page) => page.nextSequence,
    enabled: canRead && operationId !== '',
    refetchInterval: (query) => query.state.data?.pages.at(-1)?.complete === false && streamStatus !== 'connected' ? 5_000 : false,
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!session?.csrfToken || !selectedPool || !canExecute || submitMutation.isPending) return;
    const key = JSON.stringify([selectedPool.pool.poolId, model.trim(), prompt, maxTokens, allowMetered]);
    let body = pendingSubmission.current?.key === key ? pendingSubmission.current.body : null;
    if (!body) {
      body = {
        operationId: newCommandId(), poolId: selectedPool.pool.poolId,
        request: { domain: 'ai/text', capabilityProfile: 'profile.chat.basic', payload: { model: model.trim(), messages: [{ role: 'user', content: prompt }], max_tokens: maxTokens } },
        deadlineAt: Date.now() + 5 * 60_000, maxAttempts: 1, allowMetered,
      };
      pendingSubmission.current = { key, body };
    }
    submitMutation.mutate({ poolId: selectedPool.pool.poolId, csrfToken: session.csrfToken, body });
  }

  function submitReconciliation(event: FormEvent<HTMLFormElement>, operation: OperationView, resources: NonNullable<OperationView['result']>['resources']) {
    event.preventDefault();
    const attempt = operation.attempts.length === 1 ? operation.attempts[0] : undefined;
    if (!session?.csrfToken || !attempt || !canManage || reconcileMutation.isPending || !reconcileOutcome || !reconcileRetrySafety) return;
    const evidenceRefs = [...new Set(reconcileEvidence.split(/[\n,]/).map((value) => value.trim()).filter(Boolean))];
    if (evidenceRefs.length === 0 || (reconcileOutcome !== 'not_executed' && resources.some((resource) => resource.occupancy === 'not_applicable' && resource.consumed === null && !reconcileAmounts[resource.resourceId]))) return;
    const corrections: ReconciliationInput['corrections'] = resources.map((resource) => {
      const occupancyResource = resource.occupancy !== 'not_applicable';
      return {
        resourceId: resource.resourceId,
        windowKey: resource.windowKey,
        metricId: resource.metricId,
        unit: resource.unit,
        consumed: occupancyResource ? null : reconcileOutcome === 'not_executed' ? '0' : (reconcileAmounts[resource.resourceId] ?? resource.consumed),
        consumptionBasis: occupancyResource ? 'not_applicable' : 'adjustment',
        occupancy: occupancyResource ? (reconcileOutcome === 'unknown' ? 'uncertain' : 'released') : 'not_applicable',
        evidenceRefs: [],
      };
    });
    if (!reconcileCommand.current || reconcileCommand.current.attemptId !== attempt.attemptId) {
      reconcileCommand.current = { attemptId: attempt.attemptId, commandId: newCommandId() };
    }
    reconcileMutation.mutate({
      attemptId: attempt.attemptId,
      csrfToken: session.csrfToken,
      body: {
        commandId: reconcileCommand.current.commandId,
        confirmedOutcome: reconcileOutcome,
        retrySafety: reconcileRetrySafety,
        evidenceRefs,
        corrections,
      } as ReconciliationInput,
    });
  }


  useEffect(() => {
    if (!canRead || !operationId || detailQuery.data?.state !== 'running') {
      setStreamStatus(operationId ? 'closed' : 'idle');
      return;
    }
    if (typeof EventSource === 'undefined') {
      setStreamStatus('unavailable');
      return;
    }
    setStreamStatus('connecting');
    const source = new EventSource(operationEventsUrl(operationId), { withCredentials: true });
    source.onopen = () => setStreamStatus('connected');
    source.onerror = () => setStreamStatus('reconnecting');
    const eventTypes = ['accepted', 'attempt_prepared', 'dispatch_authorized', 'output_stored', 'cancel_requested', 'attempt_settled', 'operation_terminal', 'reconciled'];
    const refresh = (event: Event) => {
      if (event.type === 'operation_terminal') {
        source.close();
        setStreamStatus('closed');
      }
      void queryClient.invalidateQueries({ queryKey: ['operation', operationId] });
      void queryClient.invalidateQueries({ queryKey: ['operation-decisions', operationId] });
      void queryClient.invalidateQueries({ queryKey: ['operation-output', operationId] });
      void queryClient.invalidateQueries({ queryKey: ['operations'] });
    };
    for (const eventType of eventTypes) source.addEventListener(eventType, refresh);
    return () => {
      source.close();
      for (const eventType of eventTypes) source.removeEventListener(eventType, refresh);
    };
  }, [canRead, operationId, detailQuery.data?.state, queryClient]);

  useEffect(() => {
    const errors = [listQuery.error, detailQuery.error, decisionsQuery.error];
    if (errors.some((error) => error instanceof ApiError && error.status === 401)) queryClient.setQueryData(['session'], null);
  }, [listQuery.error, detailQuery.error, decisionsQuery.error, queryClient]);

  if (sessionLoading || (canRead && ((operationId && detailQuery.isPending) || (!operationId && listQuery.isPending)))) {
    return <section className="page"><h1>{t('page.operations.title')}</h1><PageStateFrame phase="loading" /></section>;
  }
  if (!session) return <section className="page"><h1>{t('page.operations.title')}</h1><PageStateFrame phase="empty"><p>{t('config.signInRequired')}</p><Link className="button-primary" to="/login">{t('nav.login')}</Link></PageStateFrame></section>;
  if (!canRead) return <section className="page"><h1>{t('page.operations.title')}</h1><PageStateFrame phase="error" errorDetail={t('config.error.permission')} /></section>;

  if (operationId) {
    if (detailQuery.error) return <section className="page"><h1>{t('page.operations.title')}</h1><PageStateFrame phase="error" errorDetail={errorText(detailQuery.error)} onRetry={() => void detailQuery.refetch()} /><Link to="/operations">{t('operations.backToList')}</Link></section>;
    const operation = detailQuery.data;
    if (!operation) return <section className="page"><h1>{t('page.operations.title')}</h1><PageStateFrame phase="loading" /></section>;
    const result = operation.result;
    const reasons = result?.reasons ?? [];
    const resources = result?.resources ?? [];
    const attempts = operation.attempts ?? [];
    const reconciliations = operation.reconciliations ?? [];
    return <section className="page">
      <header className="page-header">
        <div><h1>{t('operations.detail.title')}</h1><p className="config-subtitle">{operation.operationId}</p></div>
        <Link className="button-secondary" to={operationsListHref}>{t('operations.backToList')}</Link>
      </header>
      <div className="operation-summary config-state">
        <div><span>{t('operations.col.state')}</span><strong className={`config-status config-status-${operation.state === 'running' ? 'editing' : operation.state === 'succeeded' ? 'active' : 'archived'}`}>{t(`operations.state.${operation.state}`)}</strong></div>
        <div><span>{t('operations.col.pool')}</span><strong>{operation.poolId}</strong></div>
        <div><span>{t('operations.col.updated')}</span><strong>{new Date(operation.updatedAt).toLocaleString()}</strong></div>
        <div><span>{t('operations.cancelRequested')}</span><strong>{operation.cancelRequested ? t('common.yes') : t('common.no')}</strong></div>
      </div>
      <section className="config-state operation-section">
        <h2>{t('operations.decisions.title')}</h2>
        {decisionsQuery.isPending ? <p>{t('operations.decisions.loading')}</p> : null}
        {decisionsQuery.isError ? <div role="alert" className="config-alert">{errorText(decisionsQuery.error)} <button type="button" className="button-secondary" onClick={() => void decisionsQuery.refetch()}>{t('operations.decisions.retry')}</button></div> : null}
        {decisionsQuery.isSuccess && decisionsQuery.data.length === 0 ? <p className="config-note">{t('operations.decisions.empty')}</p> : null}
        {decisionsQuery.data?.map((decision) => <article className="operation-decision" key={decision.decisionId}>
          <header><strong>{t(`operations.decisions.stage.${decision.stage}`)}</strong><small>{decision.attemptId ?? decision.decisionId}</small></header>
          <dl className="operation-result-grid">
            <div><dt>{t('operations.decisions.poolVersion')}</dt><dd>{decision.poolId} · v{decision.poolVersion}</dd></div>
            <div><dt>{t('operations.decisions.configRevision')}</dt><dd>{decision.configRevision || '—'}</dd></div>
            <div><dt>{t('operations.decisions.selected')}</dt><dd>{decision.offerId || '—'}{decision.offerId ? ` · ${decision.memberPriority}` : ''}</dd></div>
            {decision.hookRegistryRevision ? <div><dt>{t('operations.decisions.hookRevision')}</dt><dd><code>{decision.hookRegistryRevision}</code></dd></div> : null}
          </dl>
          {decision.plannerCandidateOrder?.length || decision.hookCandidateOrder?.length ? <div className="operation-decision-orders">
            <div><h3>{t('operations.decisions.plannerOrder')}</h3><ol>{(decision.plannerCandidateOrder ?? []).map((offerId) => <li key={offerId}>{offerId}</li>)}</ol></div>
            <div><h3>{t('operations.decisions.hookOrder')}</h3><ol>{(decision.hookCandidateOrder ?? []).map((offerId) => <li key={offerId}>{offerId}</li>)}</ol></div>
          </div> : null}
          {decision.candidates.length ? <div className="table-scroll"><table className="data-table"><thead><tr><th>{t('operations.decisions.candidate')}</th><th>{t('operations.decisions.priority')}</th><th>{t('operations.decisions.reasons')}</th></tr></thead><tbody>{decision.candidates.map((candidate) => <tr key={candidate.offerId}>
            <td>{candidate.offerId}{candidate.offerId === decision.offerId ? <small>{t('operations.decisions.chosen')}</small> : null}</td>
            <td>{candidate.priority}</td>
            <td>{candidate.reasons.length ? candidate.reasons.map((reason) => reasonSummary(reason.group, reason.code)).join(' · ') : '—'}</td>
          </tr>)}</tbody></table></div> : null}
          {decision.traceTruncated ? <p className="config-note">{t('operations.decisions.truncated')}</p> : null}
          {decision.hookRegistryManifest ? <details><summary>{t('operations.decisions.manifest')}</summary><pre className="operation-output">{JSON.stringify(decision.hookRegistryManifest, null, 2)}</pre></details> : null}
        </article>)}
      </section>
      {operation.state === 'running' && session.permissions.includes('cancel') ? <section className="config-state operation-section">
        <h2>{t('operations.cancel.title')}</h2>
        <p>{t('operations.cancel.help')}</p>
        <button type="button" className="button-secondary" disabled={cancelMutation.isPending || operation.cancelRequested} onClick={() => {
          if (!session.csrfToken) return;
          if (!cancelCommand.current || cancelCommand.current.operationId !== operation.operationId) {
            cancelCommand.current = { operationId: operation.operationId, commandId: newCommandId() };
          }
          cancelMutation.mutate({ operationId: operation.operationId, commandId: cancelCommand.current.commandId, csrfToken: session.csrfToken });
        }}>{cancelMutation.isPending ? t('operations.cancel.sending') : t('operations.cancel.button')}</button>
        {operation.cancelRequested ? <p role="status" aria-live="polite">{t('operations.cancel.persisted')}</p> : null}
        {cancelMutation.variables?.operationId === operation.operationId && cancelMutation.data ? <p role="status" aria-live="polite">{t(`operations.cancel.status.${cancelMutation.data.status}`)}</p> : null}
        {cancelMutation.variables?.operationId === operation.operationId && cancelMutation.error ? <p role="alert" className="config-alert">{cancelMutation.error instanceof ApiError && cancelMutation.error.status === 403 ? t('config.error.permission') : cancelMutation.error instanceof ApiError && cancelMutation.error.status === 409 ? t('operations.cancel.conflict') : t('operations.cancel.error')}</p> : null}
      </section> : null}
      {result ? <section className="config-state operation-section">
        <h2>{t('operations.result.title')}</h2>
        <dl className="operation-result-grid">
          <div><dt>{t('operations.result.summaryLabel')}</dt><dd>{resultSummary(result.code)}</dd></div>
          <div><dt>{t('operations.result.retrySafety')}</dt><dd>{t(`operations.retry.${result.retrySafety}`)}</dd></div>
          <div><dt>{t('operations.result.output')}</dt><dd>{t(`operations.output.${result.outputAvailability}`)}</dd></div>
          <div><dt>{t('operations.result.action')}</dt><dd>{result.suggestedAction ? actionSummary(result.suggestedAction) : '—'}</dd></div>
        </dl>
        {reasons.length ? <div><h3>{t('operations.result.reasons')}</h3><ul>{reasons.map((reason, index) => <li key={`${reason.group}-${reason.code}-${index}`}>{reasonSummary(reason.group, reason.code)}</li>)}</ul></div> : null}
        {resources.length ? <div className="table-scroll"><table className="data-table"><thead><tr><th>{t('operations.resource.metric')}</th><th>{t('operations.resource.consumed')}</th><th>{t('operations.resource.basis')}</th><th>{t('operations.resource.occupancy')}</th><th>{t('operations.resource.window')}</th></tr></thead><tbody>{resources.map((resource) => <tr key={`${resource.resourceId}:${resource.windowKey}`}><td>{resource.metricId}<small>{resource.unit}</small></td><td>{resource.consumed === null ? t('operations.value.unknown') : resource.consumed}</td><td>{consumptionBasisSummary(resource.consumptionBasis)}<small>{t('operations.resource.evidenceCount', { count: String(resource.evidenceRefs?.length ?? 0) })}</small></td><td>{t(`operations.occupancy.${resource.occupancy}`)}</td><td>{resource.windowKey}</td></tr>)}</tbody></table></div> : null}
        {result.unrepresentableMetrics?.length ? <div className="config-alert"><h3>{t('operations.result.review')}</h3><ul>{result.unrepresentableMetrics.map((metric) => <li key={`${metric.metricId}:${metric.unit}`}>{metric.metricId}: {metric.actualOutOfRangeDecimal} {metric.unit}</li>)}</ul></div> : null}
      </section> : <p className="config-note">{t('operations.result.pending')}</p>}
      <section className="config-state operation-section">
        <h2>{t('operations.outputBody.title')}</h2>
        {outputQuery.isError ? <p role="alert" className="config-alert">{errorText(outputQuery.error)}</p> : null}
        {outputQuery.data?.pages.some((page) => page.chunks.length > 0) ? <pre className="operation-output">{outputText(outputQuery.data.pages)}</pre> : <p className="config-note">{t('operations.outputBody.empty')}</p>}
        {outputQuery.hasNextPage ? <button type="button" className="button-secondary" disabled={outputQuery.isFetchingNextPage} onClick={() => void outputQuery.fetchNextPage()}>{outputQuery.isFetchingNextPage ? t('operations.outputBody.loading') : t('operations.outputBody.more')}</button> : null}
      </section>
      <section className="config-state operation-section">
        <h2>{t('operations.attempts.title')}</h2>
        {attempts.length === 0 ? <p>{t('operations.attempts.empty')}</p> : <div className="table-scroll"><table className="data-table"><thead><tr><th>{t('operations.attempt.id')}</th><th>{t('operations.attempt.binding')}</th><th>{t('operations.attempt.phase')}</th><th>{t('operations.attempt.outcome')}</th><th>{t('operations.attempt.providerRequest')}</th></tr></thead><tbody>{attempts.map((attempt) => <tr key={attempt.attemptId}><td>{attempt.attemptId}</td><td>{attempt.accountId}<small>{attempt.bindingId}</small></td><td>{t(`operations.phase.${attempt.phase}`)}</td><td>{attempt.outcome ? t(`operations.outcome.${attempt.outcome}`) : '—'}</td><td>{attempt.providerRequestId ?? '—'}</td></tr>)}</tbody></table></div>}
      </section>
      {reconciliations.length ? <section className="config-state operation-section"><h2>{t('operations.reconciliations.title')}</h2><ul>{reconciliations.map((item) => <li key={item.reconciliationId}>
        <strong>{t(`operations.outcome.${item.confirmedOutcome}`)} · {new Date(item.createdAt).toLocaleString()}</strong>
        <small>{t('operations.resource.evidenceCount', { count: String(item.evidenceRefs.length) })}</small>
        {item.corrections.length ? <ul>{item.corrections.map((correction) => <li key={`${item.reconciliationId}:${correction.resourceId}:${correction.windowKey}`}>
          {correction.metricId} · {correction.consumed === null ? t('operations.value.unknown') : `${correction.consumed} ${correction.unit}`} · {consumptionBasisSummary(correction.consumptionBasis)} · {t(`operations.occupancy.${correction.occupancy}`)}
        </li>)}</ul> : null}
      </li>)}</ul></section> : null}
      {canManage ? <Suspense fallback={null}><OperationAdminActions operation={operation} /></Suspense> : null}
      {canManage && operation.state === 'unknown' && attempts.length === 1 && attempts[0]?.outcome === 'unknown' && resources.length ? <section className="config-state operation-section">
        <h2>{t('operations.reconcile.title')}</h2>
        <p>{t('operations.reconcile.help')}</p>
        <form className="operation-form" onSubmit={(event) => submitReconciliation(event, operation, resources)}>
          <label>{t('operations.reconcile.outcome')}<select value={reconcileOutcome} onChange={(event) => setReconcileOutcome(event.target.value)} required>
            <option value="">{t('operations.reconcile.choose')}</option>
            {(['known_success', 'known_failure', 'not_executed', 'unknown'] as const).map((outcome) => <option key={outcome} value={outcome}>{t(`operations.outcome.${outcome}`)}</option>)}
          </select></label>
          <label>{t('operations.reconcile.retrySafety')}<select value={reconcileRetrySafety} onChange={(event) => setReconcileRetrySafety(event.target.value)} required>
            <option value="">{t('operations.reconcile.choose')}</option>
            {(['proved_safe', 'unsafe', 'undetermined'] as const).map((safety) => <option key={safety} value={safety}>{t(`operations.retry.${safety}`)}</option>)}
          </select></label>
          {resources.filter((resource) => resource.occupancy === 'not_applicable').map((resource) => <label key={`${resource.resourceId}:${resource.windowKey}`}>
            {t('operations.reconcile.usage', { metric: resource.metricId, unit: resource.unit })}
            <input type="text" inputMode="numeric" pattern="^(0|[1-9][0-9]{0,18})$" value={reconcileOutcome === 'not_executed' ? '0' : (reconcileAmounts[resource.resourceId] ?? resource.consumed ?? '')} onChange={(event) => {
              const value = event.currentTarget.value;
              setReconcileAmounts((previous) => ({ ...previous, [resource.resourceId]: value }));
            }} disabled={reconcileOutcome === 'not_executed'} required={reconcileOutcome !== 'not_executed'} />
          </label>)}
          <label>{t('operations.reconcile.evidence')}<textarea value={reconcileEvidence} onChange={(event) => setReconcileEvidence(event.target.value)} required rows={3} /></label>
          <p className="config-note">{t('operations.reconcile.evidenceHelp')}</p>
          <button className="button-primary" type="submit" disabled={reconcileMutation.isPending || !reconcileOutcome || !reconcileRetrySafety || !reconcileEvidence.trim()}>{reconcileMutation.isPending ? t('operations.reconcile.sending') : t('operations.reconcile.submit')}</button>
          {reconcileMutation.data && reconcileMutation.variables?.attemptId === attempts[0]?.attemptId ? <p role="status" aria-live="polite">{t('operations.reconcile.saved')}</p> : null}
          {reconcileMutation.error && reconcileMutation.variables?.attemptId === attempts[0]?.attemptId ? <p role="alert" className="config-alert">{reconcileMutation.error instanceof ApiError && reconcileMutation.error.status === 409 ? t('operations.reconcile.conflict') : t('operations.reconcile.error')}</p> : null}
        </form>
      </section> : null}
      <p className="config-note" role="status" aria-live="polite">{t(`operations.detail.stream.${streamStatus}`)} {t('operations.detail.polling')}</p>
    </section>;
  }

  if (listQuery.error) return <section className="page"><h1>{t('page.operations.title')}</h1><PageStateFrame phase="error" errorDetail={errorText(listQuery.error)} onRetry={() => void listQuery.refetch()} /></section>;
  const page = listQuery.data;
  const operationRows = page?.operations ?? [];
  const stateFilterControl = <label className="operation-filter">{t('operations.filter.state')}<select value={stateFilter} onChange={(event) => setSearchParams(event.currentTarget.value ? { state: event.currentTarget.value } : {})}>
    <option value="">{t('operations.filter.all')}</option>
    {operationListStates.map((state) => <option key={state} value={state}>{t(`operations.state.${state}`)}</option>)}
  </select></label>;
  const composer = canExecute ? <section className="config-state operation-section">
    <h2>{t('operations.submit.title')}</h2>
    <p className="config-note">{t('operations.submit.localtestNotice')}</p>
    {poolsQuery.isError ? <p role="alert" className="config-alert">{errorText(poolsQuery.error)}</p> : null}
    {pools.length === 0 ? <p className="config-note">{t('operations.submit.noPool')}</p> : <form className="operation-form" onSubmit={submit}>
      <label>{t('operations.submit.pool')}<select value={selectedPool?.pool.poolId ?? ''} onChange={(event) => setPoolId(event.target.value)} required>{pools.map((pool) => <option key={pool.pool.poolId} value={pool.pool.poolId}>{pool.pool.name} · {pool.pool.poolId}</option>)}</select></label>
      <label>{t('operations.submit.model')}<input value={model} onChange={(event) => setModel(event.target.value)} required maxLength={128} /></label>
      <label>{t('operations.submit.prompt')}<textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} required maxLength={12000} rows={5} /></label>
      <label>{t('operations.submit.maxTokens')}<input type="number" min={1} max={8192} value={maxTokens} onChange={(event) => setMaxTokens(Number(event.target.value))} required /></label>
      <label className="operation-metered"><input type="checkbox" checked={allowMetered} onChange={(event) => setAllowMetered(event.target.checked)} />{t('operations.submit.allowMetered')}</label>
      <p className="config-note">{t('operations.submit.meteredHelp')}</p>
      <button className="button-primary" type="submit" disabled={submitMutation.isPending || !prompt.trim() || !model.trim() || !allowMetered}>{submitMutation.isPending ? t('operations.submit.sending') : t('operations.submit.button')}</button>
      {submitMutation.error ? <p role="alert" className="config-alert">{errorText(submitMutation.error)}</p> : null}
      {submitMutation.error ? <p className="config-note">{t('operations.submit.retrySafe')}</p> : null}
      {submitMutation.data?.kind === 'rejected' ? <p role="alert" className="config-alert">{t('operations.submit.rejected', { code: submitMutation.data.error.code })}</p> : null}
    </form>}
  </section> : null;
  if (!page || operationRows.length === 0) return <section className="page"><header className="page-header"><h1>{t('page.operations.title')}</h1></header>{composer}{stateFilterControl}<PageStateFrame phase="empty">{stateFilter ? <><h2>{t('operations.filter.empty')}</h2><button type="button" className="button-secondary" onClick={() => setSearchParams({})}>{t('operations.filter.clear')}</button></> : <><h2>{t('operations.empty.title')}</h2><p>{t('operations.empty.help')}</p></>}</PageStateFrame></section>;

  return <section className="page">
    <header className="page-header"><div><h1>{t('page.operations.title')}</h1><p className="config-subtitle">{t('operations.list.help')}</p></div></header>
    {composer}
    {stateFilterControl}
    <div className="table-scroll responsive-record-scroll operations-table-scroll"><table className="data-table responsive-record-table operations-table"><thead><tr><th>{t('operations.col.id')}</th><th>{t('operations.list.requestType')}</th><th>{t('operations.col.pool')}</th><th>{t('operations.col.state')}</th><th>{t('operations.list.result')}</th><th>{t('operations.list.uncertainOccupancy')}</th><th>{t('operations.col.attempts')}</th><th>{t('operations.col.updated')}</th></tr></thead><tbody>{operationRows.map((operation) => {
      const detailParams = new URLSearchParams({ operation: operation.operationId });
      if (cursor) detailParams.set('returnCursor', cursor);
      if (stateFilter) detailParams.set('returnState', stateFilter);
      const uncertainOccupancyCount = (operation.resources ?? []).filter((resource) => resource.occupancy === 'uncertain').length;
      return <tr key={operation.operationId}>
        <td data-label={t('operations.col.id')}><Link to={`/operations?${detailParams.toString()}`}>{operation.operationId}</Link></td>
        <td data-label={t('operations.list.requestType')}>{requestDomainSummary(operation.requestDomain)}</td>
        <td data-label={t('operations.col.pool')}>{operation.poolId}</td>
        <td data-label={t('operations.col.state')}><span className={`config-status config-status-${operation.state}`}>{t(`operations.state.${operation.state}`)}</span></td>
        <td data-label={t('operations.list.result')}>{operation.result?.code ? resultSummary(operation.result.code) : t('operations.list.resultPending')}</td>
        <td data-label={t('operations.list.uncertainOccupancy')}>{uncertainOccupancyCount > 0 ? t('operations.list.uncertainOccupancyCount', { count: String(uncertainOccupancyCount) }) : t('operations.list.noUncertainOccupancy')}</td>
        <td data-label={t('operations.col.attempts')}>{operation.attempts.length}</td>
        <td data-label={t('operations.col.updated')}>{new Date(operation.updatedAt).toLocaleString()}</td>
      </tr>;
    })}</tbody></table></div>
    {page.nextCursor ? <div className="config-actions"><button type="button" className="button-secondary" onClick={() => setSearchParams(stateFilter ? { cursor: page.nextCursor!, state: stateFilter } : { cursor: page.nextCursor! })}>{t('operations.nextPage')}</button></div> : null}
    <p className="config-note">{t('operations.list.polling')}</p>
  </section>;
}
