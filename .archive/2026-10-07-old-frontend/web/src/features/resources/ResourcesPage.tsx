import { lazy, Suspense, useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { ApiError } from '../../api/client';
import { createConfigDraft, emptyConfigDocument, readActiveConfig, type ConfigProjectionView } from '../../api/config';
import { rememberedDraft, rememberDraft } from '../../api/draft-pointer';
import { InventoryResourcesPage } from './InventoryPage';
import { ResourceDraftPage } from './ResourceDraftPage';
import { readPools } from '../../api/pools';
import type { ResourceView } from '../../api/types';

const ObservationRefreshForm = lazy(() => import('./ObservationRefreshForm').then((module) => ({ default: module.ObservationRefreshForm })));
import { useSession } from '../../auth/SessionContext';
import { PageStateFrame } from '../../components/PageState';
import { t } from '../../i18n';

export function ResourcesPage() {
  const [params] = useSearchParams();
  const draftId = params.get('draft');
  const { session } = useSession();
  if (params.get('view') !== 'legacy' && params.get('view') !== 'active') return <InventoryResourcesPage key={`${session?.namespace}:${session?.principalId}`} />;
  return draftId ? <ResourceDraftPage key={`${draftId}:${session?.namespace}:${session?.principalId}`} draftId={draftId} /> : <ActiveResourcesPage />;
}

function DraftActions({ active }: { active?: ConfigProjectionView | null }) {
  const { session } = useSession();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  if (!session?.permissions.includes('configure') || !session.csrfToken) return null;
  const resume = rememberedDraft(session.namespace, session.principalId);
  async function start() {
    if (!session?.csrfToken) return;
    setBusy(true); setError('');
    try {
      const draft = await createConfigDraft(active?.document ?? emptyConfigDocument(), session.csrfToken, active?.revision);
      queryClient.setQueryData(['config-draft', draft.draftId], draft);
      rememberDraft(session.namespace, session.principalId, draft.draftId);
      navigate(`/resources?draft=${encodeURIComponent(draft.draftId)}&view=legacy`);
    } catch { setError(t('config.error.request')); } finally { setBusy(false); }
  }
  return <div className="config-actions">
    <button className="button-primary" disabled={busy} onClick={() => void start()}>{t(busy ? 'config.draft.creating' : 'resourceDraft.start')}</button>
    {resume && <Link className="button-secondary" to={`/resources?draft=${encodeURIComponent(resume)}&view=legacy`}>{t('resourceDraft.resume')}</Link>}
    {error && <p role="alert">{error}</p>}
  </div>;
}

function ActiveResourcesPage() {
  const { session, isLoading: sessionLoading } = useSession();
  const queryClient = useQueryClient();
  const [observationResource, setObservationResource] = useState<ResourceView | null>(null);
  const canRead = session?.permissions.some((permission) => permission === 'read' || permission === 'configure') ?? false;
  const canManage = session?.permissions.includes('manage') ?? false;
  const query = useQuery({ queryKey: ['active-config'], queryFn: readActiveConfig, enabled: canRead });
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: readPools, enabled: canRead });
  const config = query.data?.document;
  const accounts = config?.accounts ?? [];
  const bindings = config?.bindings ?? [];
  const evidenceById = new Map((config?.evidence ?? []).map((evidence) => [evidence.evidenceId, evidence]));

  useEffect(() => {
    if ([query.error, poolsQuery.error].some((error) => error instanceof ApiError && error.status === 401)) {
      queryClient.setQueryData(['session'], null);
    }
  }, [poolsQuery.error, query.error, queryClient]);

  function errorText(error: unknown): string {
    if (error instanceof ApiError && error.status === 403) return t('config.error.permission');
    if (error instanceof ApiError && error.status === 401) return t('config.error.session');
    return t('config.error.request');
  }

  if (sessionLoading || (canRead && (query.isPending || poolsQuery.isPending))) return <section className="page"><h1>{t('page.resources.title')}</h1><PageStateFrame phase="loading" /></section>;
  if (!session) return <section className="page"><h1>{t('page.resources.title')}</h1><PageStateFrame phase="empty"><p>{t('config.signInRequired')}</p><Link className="button-primary" to="/login">{t('nav.login')}</Link></PageStateFrame></section>;
  if (!canRead) return <section className="page"><h1>{t('page.resources.title')}</h1><PageStateFrame phase="error" errorDetail={t('config.error.permission')} /></section>;
  if (query.error) {
    return <section className="page"><h1>{t('page.resources.title')}</h1><PageStateFrame phase="error" errorDetail={errorText(query.error)} onRetry={() => void query.refetch()} /></section>;
  }
  if (!config || config.resources.length === 0) {
    return <section className="page">
      <header className="page-header"><h1>{t('page.resources.title')}</h1></header>
      <PageStateFrame phase="empty">
        <h2>{t('resources.empty.title')}</h2>
        <p>{t('resources.empty.help')}</p>
        <Link className="button-secondary" to="/providers?view=advanced">{t('resources.empty.action')}</Link>
        <DraftActions active={query.data} />
      </PageStateFrame>
    </section>;
  }

  const liveRows = new Map<string, Array<{ poolName: string; resource: ResourceView; ambiguous: boolean }>>();
  for (const poolView of poolsQuery.data ?? []) {
    const byKey = new Map<string, { resource: ResourceView; ambiguous: boolean }>();
    for (const group of poolView.groups) {
      for (const member of group.members) {
        for (const resource of member.resources) {
          const key = `${resource.resourceId}\u0000${resource.windowKey}`;
          const existing = byKey.get(key);
          if (existing) {
            if (JSON.stringify(existing.resource) !== JSON.stringify(resource)) existing.ambiguous = true;
          } else {
            byKey.set(key, { resource, ambiguous: false });
          }
        }
      }
    }
    for (const { resource, ambiguous } of byKey.values()) {
      const rows = liveRows.get(resource.resourceId) ?? [];
      rows.push({ poolName: poolView.pool.name, resource, ambiguous });
      liveRows.set(resource.resourceId, rows);
    }
  }

  return <section className="page">
    <header className="page-header">
      <div><h1>{t('page.resources.title')}</h1><p className="config-subtitle">{t('resources.active.revision', { revision: query.data?.revision.slice(0, 12) ?? '' })}</p></div>
      <DraftActions active={query.data} />
    </header>
    <p className="config-note">{t(poolsQuery.error ? 'resources.balance.error' : 'resources.capacity.note')}
      {poolsQuery.error && <button className="button-secondary" type="button" onClick={() => void poolsQuery.refetch()}>{t('resources.balance.retry')}</button>}
    </p>
    <div className="table-scroll responsive-record-scroll resource-table-scroll">
      <table className="data-table responsive-record-table resource-table">
        <thead><tr>
          <th scope="col">{t('resources.col.resource')}</th>
          <th scope="col">{t('resources.col.owner')}</th>
          <th scope="col">{t('resources.col.dimension')}</th>
          <th scope="col">{t('resources.col.capacity')}</th>
          <th scope="col">{t('resources.col.pool')}</th>
          <th scope="col">{t('resources.col.available')}</th>
          <th scope="col">{t('resources.col.reserved')}</th>
          <th scope="col">{t('resources.col.active')}</th>
          <th scope="col">{t('resources.col.window')}</th>
          <th scope="col">{t('resources.col.evidence')}</th>
        </tr></thead>
        <tbody>{config.resources.map((resource) => {
          const accountId = resource.ownerKind === 'account'
            ? resource.ownerId
            : bindings.find((binding) => binding.bindingId === resource.ownerId)?.accountId;
          const account = accounts.find((item) => item.accountId === accountId);
          const owner = resource.ownerKind === 'binding'
            ? bindings.find((binding) => binding.bindingId === resource.ownerId)
            : undefined;
          const evidence = evidenceById.get(resource.capacityEvidenceRef);
          const windowValue = [resource.window.kind, resource.window.period, resource.window.timeZone].filter(Boolean).join(' · ');
          const balances = liveRows.get(resource.resourceId);
          const rows = balances?.length ? balances : [{ poolName: '', resource: undefined, ambiguous: false }];
          return rows.map((balance, index) => <tr key={`${resource.resourceId}:${balance.resource?.windowKey ?? 'unavailable'}:${balance.poolName || 'none'}:${index}`}>
            <td data-label={t('resources.col.resource')}><strong>{resource.metricId}</strong><small>{resource.resourceId}</small></td>
            <td data-label={t('resources.col.owner')}>{account?.displayName ?? accountId ?? resource.ownerId}<small>{owner?.domain ?? resource.ownerKind}</small></td>
            <td data-label={t('resources.col.dimension')}>{resource.commercialRole === 'entitlement' ? t('resources.role.entitlement') : resource.kind === 'occupancy' ? t('resources.role.occupancy') : t('resources.role.limit')}<small>{resource.unit}{resource.currency ? ` · ${resource.currency}` : ''}</small></td>
            <td data-label={t('resources.col.capacity')}><strong>{resource.configuredCapacity}</strong><small>{t('resources.capacity.configured')}</small></td>
            <td data-label={t('resources.col.pool')}>{balance.poolName || t('resources.pool.none')}{balance.resource && <small>{balance.resource.windowKey}</small>}</td>
            <td data-label={t('resources.col.available')}>{balance.ambiguous ? t('resources.balance.conflict') : balance.resource?.available ?? t('resources.balance.unknown')}
              {balance.resource && <small>{t(balance.resource.evidenceStatus === 'verified' ? 'resources.balance.evidence.verified' : balance.resource.evidenceStatus === 'expired' ? 'resources.balance.evidence.expired' : 'resources.balance.evidence.unknown')}</small>}
              {balance.resource?.observationRefreshNeeded && <small>{t('resources.balance.refresh')}</small>}
              {canManage && resource.kind === 'counter' && balance.resource && !balance.ambiguous && index === 0 && !poolsQuery.error
                ? <button className="resource-observation-trigger" type="button" onClick={() => setObservationResource(balance.resource)}>{t('resources.observe.open')}</button>
                : null}
            </td>
            <td data-label={t('resources.col.reserved')}>{balance.ambiguous ? t('resources.balance.conflict') : balance.resource?.reserved ?? t('resources.balance.unknown')}</td>
            <td data-label={t('resources.col.active')}>{balance.ambiguous ? t('resources.balance.conflict') : balance.resource?.active ?? t('resources.balance.unknown')}</td>
            <td data-label={t('resources.col.window')}>{windowValue || '—'}<small>{resource.metered ? t('resources.metered') : t('resources.notMetered')}</small></td>
            <td data-label={t('resources.col.evidence')}>{evidence?.sourceRef.startsWith('localtest-fixture:') ? <span className="config-status config-status-editing">{t('config.fixtureLabel')}</span> : evidence?.sourceRef ?? '—'}<small>{evidence ? t('resources.evidence.expires', { at: new Date(evidence.expiresAtMillis).toLocaleString() }) : t('resources.evidence.missing')}</small></td>
          </tr>);
        })}</tbody>
      </table>
    </div>
    {observationResource && session.csrfToken ? <Suspense fallback={<section className="config-state resource-observation"><p>{t('resources.observe.loadingForm')}</p></section>}>
      <ObservationRefreshForm resource={observationResource} csrfToken={session.csrfToken} onClose={() => setObservationResource(null)} />
    </Suspense> : null}
  </section>;
}
