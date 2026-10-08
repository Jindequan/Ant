import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { ApiError } from '../../api/client';
import { readActiveConfig } from '../../api/config';
import { readPools, reorderPool } from '../../api/pools';
import type { PoolMemberView, PoolView } from '../../api/types';
import { useSession } from '../../auth/SessionContext';
import { PageStateFrame } from '../../components/PageState';
import { t } from '../../i18n';

function flattenedMembers(pool: PoolView): PoolMemberView[] {
  return pool.groups.flatMap((group) => group.members)
    .sort((a, b) => a.member.priority - b.member.priority || a.offer.offerId.localeCompare(b.offer.offerId));
}

function errorText(error: unknown): string {
  if (error instanceof ApiError && error.status === 403) return t('config.error.permission');
  if (error instanceof ApiError && error.status === 401) return t('config.error.session');
  if (error instanceof ApiError && error.status === 409) return t('pools.order.conflict');
  return t('config.error.request');
}

export function PoolsPage() {
  const { session, isLoading: sessionLoading } = useSession();
  const queryClient = useQueryClient();
  const canRead = session?.permissions.some((permission) => permission === 'read' || permission === 'configure') ?? false;
  const canConfigure = session?.permissions.includes('configure') ?? false;
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: readPools, enabled: canRead });
  const configQuery = useQuery({ queryKey: ['active-config'], queryFn: readActiveConfig, enabled: canRead });
  const [poolOrders, setPoolOrders] = useState<Record<string, string[]>>({});
  const [editingPoolId, setEditingPoolId] = useState('');
  const [saving, setSaving] = useState(false);
  const [orderError, setOrderError] = useState('');

  useEffect(() => {
    if ([poolsQuery.error, configQuery.error].some((error) => error instanceof ApiError && error.status === 401)) {
      queryClient.setQueryData(['session'], null);
    }
  }, [configQuery.error, poolsQuery.error, queryClient]);

  function getOrder(pool: PoolView): string[] {
    const liveIds = flattenedMembers(pool).map((member) => member.offer.offerId);
    const savedOrder = poolOrders[pool.pool.poolId];
    if (!savedOrder) return liveIds;
    const liveSet = new Set(liveIds);
    return [...savedOrder.filter((offerId) => liveSet.has(offerId)), ...liveIds.filter((offerId) => !savedOrder.includes(offerId))];
  }

  function moveMember(pool: PoolView, offerId: string, delta: -1 | 1) {
    const current = getOrder(pool);
    const index = current.indexOf(offerId);
    const nextIndex = index + delta;
    if (index < 0 || nextIndex < 0 || nextIndex >= current.length) return;
    const next = [...current];
    [next[index], next[nextIndex]] = [next[nextIndex]!, next[index]!];
    setPoolOrders((orders) => ({ ...orders, [pool.pool.poolId]: next }));
    setOrderError('');
  }

  async function saveOrder(pool: PoolView) {
    const configRevision = configQuery.data?.revision;
    const csrfToken = session?.csrfToken;
    if (!configRevision || !csrfToken || !canConfigure) return;
    setSaving(true);
    setOrderError('');
    try {
      const updated = await reorderPool(pool.pool, configRevision, getOrder(pool), csrfToken);
      queryClient.setQueryData<PoolView[]>(['pools'], (pools) => pools?.map((item) => item.pool.poolId === updated.pool.poolId ? updated : item));
      setEditingPoolId('');
      setPoolOrders((orders) => { const next = { ...orders }; delete next[pool.pool.poolId]; return next; });
    } catch (error) {
      setOrderError(errorText(error));
      if (error instanceof ApiError && error.status === 409) {
        void poolsQuery.refetch();
        void configQuery.refetch();
      }
    } finally {
      setSaving(false);
    }
  }

  if (sessionLoading || (canRead && (poolsQuery.isPending || configQuery.isPending))) return <section className="page"><h1>{t('page.pools.title')}</h1><PageStateFrame phase="loading" /></section>;
  if (!session) return <section className="page"><h1>{t('page.pools.title')}</h1><PageStateFrame phase="empty"><p>{t('config.signInRequired')}</p><Link className="button-primary" to="/login">{t('nav.login')}</Link></PageStateFrame></section>;
  if (!canRead) return <section className="page"><h1>{t('page.pools.title')}</h1><PageStateFrame phase="error" errorDetail={t('config.error.permission')} /></section>;
  const error = poolsQuery.error ?? configQuery.error;
  if (error) return <section className="page"><h1>{t('page.pools.title')}</h1><PageStateFrame phase="error" errorDetail={errorText(error)} onRetry={() => { void poolsQuery.refetch(); void configQuery.refetch(); }} /></section>;
  const pools = poolsQuery.data ?? [];

  if (pools.length === 0) return <section className="page">
    <header className="page-header"><h1>{t('page.pools.title')}</h1></header>
    <PageStateFrame phase="empty">
      <h2>{t('pools.empty.title')}</h2>
      <p>{t('pools.empty.help')}</p>
      <Link className="button-primary" to="/providers">{t('pools.empty.action')}</Link>
    </PageStateFrame>
  </section>;

  return <section className="page">
    <header className="page-header">
      <div><h1>{t('page.pools.title')}</h1><p className="config-subtitle">{t('pools.live')}</p></div>
      <Link className="button-secondary" to="/providers">{t('pools.edit')}</Link>
    </header>
    <div className="pool-list">{pools.map((poolView) => {
      const { pool } = poolView;
      const members = flattenedMembers(poolView);
      const order = getOrder(poolView);
      const orderedMembers = order.map((offerId) => members.find((member) => member.offer.offerId === offerId)).filter((member): member is PoolMemberView => member !== undefined);
      const editing = editingPoolId === pool.poolId;
      return <article className="config-state pool-card" key={pool.poolId}>
        <header className="pool-card-header">
          <div><h2>{pool.name}</h2><p>{t('pools.namespace', { namespace: pool.namespace })} · {pool.poolId}</p></div>
          <span className={`config-status ${pool.enabled ? 'config-status-active' : 'config-status-archived'}`}>{pool.enabled ? t('pools.enabled') : t('pools.disabled')}</span>
        </header>
        {orderedMembers.length === 0 ? <p>{t('pools.members.empty')}</p> : <ol className="pool-members">{orderedMembers.map((member, index) => {
          const label = `${member.offer.domain} · ${member.offer.capabilityProfile}`;
          return <li key={member.offer.offerId}>
            <span className="pool-priority" aria-label={t('pools.priority')}>{editing ? (index + 1) * 10 : member.member.priority}</span>
            <div className="pool-member-detail">
              <strong>{label}</strong>
              <small>{t('pools.offerId', { id: member.offer.offerId })} · {t('pools.bindingId', { id: member.offer.bindingId })}</small>
              <span className={`config-status ${member.status === 'available' ? 'config-status-active' : 'config-status-archived'}`}>{t(`pools.status.${member.status}`)}</span>
              {member.resources.map((resource) => <small className="pool-resource" key={`${resource.resourceId}:${resource.windowKey}`}>
                {resource.metricId}: {resource.available ?? t('pools.available.unknown')} {resource.unit}
                {resource.observationRefreshNeeded ? ` · ${t('pools.refreshNeeded')}` : ''}
              </small>)}
              {member.reasons.length > 0 ? <small className="pool-reasons">{t('pools.reasons', { count: String(member.reasons.length) })}</small> : null}
            </div>
            {editing ? <span className="pool-order-controls">
              <button type="button" className="button-secondary" aria-label={t('pools.order.up', { offer: member.offer.offerId })} disabled={index === 0 || saving} onClick={() => moveMember(poolView, member.offer.offerId, -1)}>↑</button>
              <button type="button" className="button-secondary" aria-label={t('pools.order.down', { offer: member.offer.offerId })} disabled={index === orderedMembers.length - 1 || saving} onClick={() => moveMember(poolView, member.offer.offerId, 1)}>↓</button>
            </span> : null}
          </li>;
        })}</ol>}
        {canConfigure && members.length > 1 ? <div className="config-actions pool-order-actions">
          {editing ? <>
            <button type="button" className="button-primary" disabled={saving || !configQuery.data} onClick={() => void saveOrder(poolView)}>{saving ? t('pools.order.saving') : t('pools.order.save')}</button>
            <button type="button" className="button-secondary" disabled={saving} onClick={() => { setEditingPoolId(''); setPoolOrders((orders) => { const next = { ...orders }; delete next[pool.poolId]; return next; }); setOrderError(''); }}>{t('pools.order.cancel')}</button>
          </> : <button type="button" className="button-secondary" onClick={() => { setEditingPoolId(pool.poolId); setPoolOrders((orders) => ({ ...orders, [pool.poolId]: getOrder(poolView) })); }}>{t('pools.order.edit')}</button>}
        </div> : null}
      </article>;
    })}</div>
    {orderError ? <p className="config-alert" role="alert">{orderError}</p> : null}
  </section>;
}
