import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useBlocker } from 'react-router-dom';
import { ApiError } from '../../api/client';
import { readConfigDraft, readProviderCatalog, saveConfigDraft } from '../../api/config';
import { rememberDraft } from '../../api/draft-pointer';
import { validID } from '../../api/config-helpers';
import type { ConfigDocument, Resource } from '../../api/types';
import { useSession } from '../../auth/SessionContext';
import { PageStateFrame } from '../../components/PageState';
import { getLocale, t } from '../../i18n';
import { ResourceForm } from './ResourceForm';
import { renderableResourceDraft, resourceReferences } from './resource-draft';

export function ResourceDraftPage({ draftId }: { draftId: string }) {
  const { session, isLoading } = useSession();
  const canRead =
    session?.permissions.some((permission) => permission === 'read' || permission === 'configure') ?? false;
  const canConfigure = session?.permissions.includes('configure') ?? false;
  const queryClient = useQueryClient();
  const draftQueryKey = ['config-draft', draftId, session?.namespace, session?.principalId] as const;
  const query = useQuery({
    queryKey: draftQueryKey,
    queryFn: () => readConfigDraft(draftId),
    enabled: canRead,
    retry: false,
  });
  const catalog = useQuery({
    queryKey: ['provider-catalog'],
    queryFn: readProviderCatalog,
    enabled: canRead,
    staleTime: 60000,
  });
  const [document, setDocument] = useState<ConfigDocument | null>(null);
  const [saved, setSaved] = useState('');
  const [draftVersion, setDraftVersion] = useState(0);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<Resource | 'new' | null>(null);
  const [removeId, setRemoveId] = useState('');
  const [reloadConfirm, setReloadConfirm] = useState(false);
  const [providerId, setProviderId] = useState('');
  const [accountId, setAccountId] = useState('');
  const [accountName, setAccountName] = useState('');
  const loadedId = useRef('');
  const dirty = document !== null && JSON.stringify(document) !== saved;
  const formDirty = !!providerId || !!accountId || !!accountName || editing !== null;
  const blocked = useBlocker(dirty || formDirty);
  const writable =
    canConfigure && !!session?.csrfToken && !!query.data && ['editing', 'validated'].includes(query.data.status);

  useEffect(() => {
    if (query.data && loadedId.current !== draftId) {
      loadedId.current = draftId;
      setDocument(query.data.definition);
      setSaved(JSON.stringify(query.data.definition));
      setDraftVersion(query.data.version);
      queryClient.setQueryData(['config-draft', draftId], query.data);
    }
    if (query.data && session && ['editing', 'validated'].includes(query.data.status))
      rememberDraft(session.namespace, session.principalId, draftId);
  }, [query.data, draftId, session, queryClient]);
  useEffect(() => {
    if (query.error instanceof ApiError && query.error.status === 401) queryClient.setQueryData(['session'], null);
  }, [query.error, queryClient]);
  useEffect(() => {
    if (!dirty && !formDirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty, formDirty]);

  async function save() {
    if (!document || !query.data || !writable || !session?.csrfToken) return false;
    setSaving(true);
    setError('');
    setMessage('');
    try {
      const next = await saveConfigDraft({ ...query.data, version: draftVersion }, document, session.csrfToken);
      queryClient.setQueryData(draftQueryKey, next);
      queryClient.setQueryData(['config-draft', draftId], next);
      setDocument(next.definition);
      setSaved(JSON.stringify(next.definition));
      setDraftVersion(next.version);
      setMessage(t('resourceDraft.saved', { version: String(next.version) }));
      return true;
    } catch (failure) {
      setError(
        t(
          failure instanceof ApiError && failure.status === 409
            ? 'resourceDraft.conflict'
            : failure instanceof ApiError && failure.status === 403
              ? 'config.error.permission'
              : failure instanceof ApiError && failure.status === 401
                ? 'config.error.session'
                : 'config.error.request',
        ),
      );
      if (failure instanceof ApiError && failure.status === 401) queryClient.setQueryData(['session'], null);
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function reload() {
    const result = await query.refetch();
    if (result.data) {
      setDocument(result.data.definition);
      setSaved(JSON.stringify(result.data.definition));
      setDraftVersion(result.data.version);
      setError('');
      setMessage('');
      setEditing(null);
      setRemoveId('');
      setProviderId('');
      setAccountId('');
      setAccountName('');
    }
    if (result.error) setError(t('config.error.request'));
    setReloadConfirm(false);
  }

  function change(next: ConfigDocument) {
    setDocument(next);
    setError('');
    setMessage('');
  }
  function addAccount() {
    if (!document || !writable) return;
    const id = accountId.trim();
    const provider = providerId.trim();
    const name = accountName.trim();
    if (!validID(id) || !validID(provider) || !name || document.accounts.some((item) => item.accountId === id)) {
      setError(t('resourceDraft.accountInvalid'));
      return;
    }
    change({
      ...document,
      accounts: [...document.accounts, { accountId: id, providerTypeId: provider, displayName: name, enabled: true }],
    });
    setAccountId('');
    setProviderId('');
    setAccountName('');
  }

  if (isLoading || (canRead && (query.isPending || (!!query.data && loadedId.current !== draftId))))
    return (
      <section className="page">
        <h1>{t('page.resources.title')}</h1>
        <PageStateFrame phase="loading" />
      </section>
    );
  if (!session)
    return (
      <section className="page">
        <h1>{t('page.resources.title')}</h1>
        <PageStateFrame phase="empty">
          <p>{t('config.signInRequired')}</p>
          <Link to="/login" className="button-primary">
            {t('nav.login')}
          </Link>
        </PageStateFrame>
      </section>
    );
  if (!canRead)
    return (
      <section className="page">
        <h1>{t('page.resources.title')}</h1>
        <PageStateFrame phase="error" errorDetail={t('config.error.permission')} />
      </section>
    );
  if (query.error && !document)
    return (
      <section className="page">
        <h1>{t('page.resources.title')}</h1>
        <PageStateFrame
          phase="error"
          errorDetail={t('config.draft.unavailable')}
          onRetry={() => void query.refetch()}
        />
        <Link to="/resources?view=active">{t('resourceDraft.back')}</Link>
      </section>
    );
  if (!document || !renderableResourceDraft(document))
    return (
      <section className="page">
        <h1>{t('page.resources.title')}</h1>
        <p role="alert">{t('resourceDraft.shape')}</p>
        <Link to={`/providers?draft=${encodeURIComponent(draftId)}&view=advanced`}>{t('resourceDraft.advanced')}</Link>
      </section>
    );

  return (
    <section className="page">
      <header className="page-header">
        <div>
          <h1>{t('page.resources.title')}</h1>
          <p>{t('resourceDraft.description')}</p>
        </div>
        {writable && (
          <button
            className="button-primary"
            disabled={saving || !dirty || editing !== null}
            onClick={() => void save()}
          >
            {t(saving ? 'config.action.saving' : 'config.action.save')}
          </button>
        )}
      </header>
      <div className="config-workspace-meta">
        <span>{t('config.draft.id', { id: draftId })}</span>
        <span>{t('config.draft.version', { version: String(draftVersion) })}</span>
        <span className="config-status config-status-editing">
          {t(writable ? 'resourceDraft.pending' : `config.status.${query.data?.status}`)}
        </span>
        {dirty && <span role="status">{t('config.draft.unsaved')}</span>}
      </div>
      <p className="config-note">{t('resourceDraft.declaration')}</p>
      {message && <p role="status">{message}</p>}
      {error && (
        <p role="alert" className="config-alert">
          {error}
        </p>
      )}
      {!writable && (
        <p>
          {t(
            canConfigure && query.data && !['editing', 'validated'].includes(query.data.status)
              ? 'resourceDraft.locked'
              : 'config.readOnly',
          )}
        </p>
      )}
      {editing !== null && <p className="config-note">{t('resourceDraft.applyFirst')}</p>}
      {writable && (
        <section className="config-state" aria-labelledby="subscription-account-title">
          <h2 id="subscription-account-title">{t('resourceDraft.accountTitle')}</h2>
          <p>{t('resourceDraft.accountHelp')}</p>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (!saving) addAccount();
            }}
          >
            <fieldset disabled={saving} className="resource-draft-fields config-account-form">
              <label>
                {t('resourceDraft.provider')}
                <input
                  aria-label={t('resourceDraft.provider')}
                  maxLength={64}
                  value={providerId}
                  list="draft-providers"
                  onChange={(event) => setProviderId(event.currentTarget.value)}
                />
                <datalist id="draft-providers">
                  {catalog.data?.map((provider) => (
                    <option key={provider.typeId} value={provider.typeId} />
                  ))}
                </datalist>
                <small>{t('resourceDraft.providerHelp')}</small>
              </label>
              <label>
                {t('config.account.id')}
                <input maxLength={64} value={accountId} onChange={(event) => setAccountId(event.currentTarget.value)} />
              </label>
              <label>
                {t('config.account.name')}
                <input
                  maxLength={120}
                  value={accountName}
                  onChange={(event) => setAccountName(event.currentTarget.value)}
                />
              </label>
            </fieldset>
            <button type="submit" className="button-secondary" disabled={saving}>
              {t('config.account.add')}
            </button>
          </form>
        </section>
      )}
      {document.accounts.length > 0 && (
        <section className="config-state">
          <h2>{t('config.count.accounts')}</h2>
          <ul>
            {document.accounts.map((account) => (
              <li key={account.accountId}>
                <strong>{account.displayName}</strong> · {account.providerTypeId} · {account.accountId}
              </li>
            ))}
          </ul>
        </section>
      )}
      <section className="config-state">
        <div className="config-state-heading">
          <h2>{t('resourceDraft.list')}</h2>
          {writable && (
            <button
              className="button-secondary"
              disabled={saving || editing !== null || !document.accounts.length}
              onClick={() => {
                setEditing('new');
                setError('');
              }}
            >
              {t('resourceDraft.add')}
            </button>
          )}
        </div>
        {!document.accounts.length && <p>{t('resourceDraft.accountRequired')}</p>}
        {!document.resources.length ? (
          <PageStateFrame phase="empty">
            <p>{t('resourceDraft.empty')}</p>
          </PageStateFrame>
        ) : (
          <div className="table-scroll responsive-record-scroll resource-table-scroll">
            <table className="data-table responsive-record-table resource-table">
              <thead>
                <tr>
                  {['id', 'owner', 'capacity', 'window', 'actions'].map((key) => (
                    <th key={key} scope="col">
                      {t(`resourceDraft.${key}`)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {document.resources.map((resource) => {
                  const references = resourceReferences(document, resource.resourceId);
                  return (
                    <tr key={resource.resourceId}>
                      <td data-label={t('resourceDraft.id')}>
                        <strong>{resource.resourceId}</strong>
                        <small>{resource.metricId}</small>
                      </td>
                      <td data-label={t('resourceDraft.owner')}>
                        {document.accounts.find((item) => item.accountId === resource.ownerId)?.displayName ??
                          resource.ownerId}
                      </td>
                      <td data-label={t('resourceDraft.capacity')}>
                        {resource.configuredCapacity}
                        <small>
                          {resource.unit}
                          {resource.currency ? ` · ${resource.currency}` : ''} ·{' '}
                          {t(
                            resource.commercialRole === 'entitlement'
                              ? 'resources.role.entitlement'
                              : resource.kind === 'occupancy'
                                ? 'resources.role.occupancy'
                                : 'resources.role.limit',
                          )}{' '}
                          · {t('resourceDraft.pending')}
                        </small>
                      </td>
                      <td data-label={t('resourceDraft.window')}>
                        {t(`resourceDraft.window.${resource.window.kind}`)}
                        {resource.window.kind !== 'lifetime' ? ' · ' : ''}
                        {resource.window.kind === 'fixed'
                          ? `${t(`resourceDraft.period.${resource.window.period}`)} · ${resource.window.timeZone}`
                          : resource.window.kind === 'rolling'
                            ? t('resourceDraft.durationSummary', { duration: String(resource.window.durationMillis) })
                            : ''}
                        {resource.window.anchorISO && <small>{resource.window.anchorISO}</small>}
                        {resource.expiresAtMillis && (
                          <small>
                            {new Date(resource.expiresAtMillis).toLocaleString(
                              getLocale() === 'zh' ? 'zh-CN' : 'en-US',
                            )}
                          </small>
                        )}
                      </td>
                      <td data-label={t('resourceDraft.actions')}>
                        {writable && (
                          <>
                            <button
                              className="button-secondary"
                              disabled={saving || editing !== null || references.length > 0}
                              onClick={() => setEditing(resource)}
                            >
                              {t('resourceDraft.edit')}
                            </button>
                            <button
                              className="button-secondary"
                              disabled={saving || editing !== null || references.length > 0}
                              onClick={() => setRemoveId(resource.resourceId)}
                            >
                              {t('resourceDraft.remove')}
                            </button>
                            {references.length > 0 && (
                              <small>{t('resourceDraft.referenced', { references: references.join(', ') })}</small>
                            )}
                          </>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
      {editing !== null && (
        <ResourceForm
          key={editing === 'new' ? 'new' : editing.resourceId}
          document={document}
          resource={editing === 'new' ? undefined : editing}
          disabled={saving || !writable}
          onCancel={() => setEditing(null)}
          onApply={(resource) => {
            change({
              ...document,
              resources:
                editing === 'new'
                  ? [...document.resources, resource]
                  : document.resources.map((item) => (item.resourceId === resource.resourceId ? resource : item)),
            });
            setEditing(null);
          }}
        />
      )}
      {removeId && (
        <section className="config-state" aria-labelledby="remove-resource-title">
          <h2 id="remove-resource-title">{t('resourceDraft.removeConfirm', { id: removeId })}</h2>
          <p>{t('resourceDraft.removeHelp')}</p>
          <div className="config-actions">
            <button
              className="button-secondary"
              disabled={saving}
              onClick={() => {
                if (!resourceReferences(document, removeId).length)
                  change({ ...document, resources: document.resources.filter((item) => item.resourceId !== removeId) });
                setRemoveId('');
              }}
            >
              {t('resourceDraft.remove')}
            </button>
            <button className="button-secondary" onClick={() => setRemoveId('')}>
              {t('resourceDraft.cancel')}
            </button>
          </div>
        </section>
      )}
      {(query.data?.fieldIssues?.length ?? 0) > 0 && (
        <details className="config-state">
          <summary>{t('resourceDraft.issues', { count: String(query.data?.fieldIssues.length) })}</summary>
          <p>{t('resourceDraft.issueHelp')}</p>
          <ul>
            {query.data?.fieldIssues.map((issue, index) => (
              <li key={`${issue.path}:${index}`}>
                <code>{issue.path}</code> · {t(issue.messageKey)}
              </li>
            ))}
          </ul>
        </details>
      )}
      <div className="config-actions">
        <Link className="button-secondary" to={`/providers?draft=${encodeURIComponent(draftId)}&view=advanced`}>
          {t('resourceDraft.advanced')}
        </Link>
        <Link to="/resources?view=active">{t('resourceDraft.back')}</Link>
        {writable && (
          <button
            type="button"
            className="button-secondary"
            disabled={saving}
            onClick={() => {
              if (dirty || formDirty) setReloadConfirm(true);
              else void reload();
            }}
          >
            {t('resourceDraft.reload')}
          </button>
        )}
      </div>
      {reloadConfirm && (
        <section role="alertdialog" aria-labelledby="reload-draft-title" className="config-state">
          <h2 id="reload-draft-title">{t('resourceDraft.reloadConfirm')}</h2>
          <p>{t('resourceDraft.reloadHelp')}</p>
          <div className="config-actions">
            <button autoFocus className="button-secondary" onClick={() => setReloadConfirm(false)}>
              {t('resourceDraft.cancel')}
            </button>
            <button className="button-secondary" onClick={() => void reload()}>
              {t('resourceDraft.reload')}
            </button>
          </div>
        </section>
      )}
      {blocked.state === 'blocked' && (
        <section role="alertdialog" aria-labelledby="resource-unsaved-title" className="config-state">
          <h2 id="resource-unsaved-title">{t('config.draft.unsaved')}</h2>
          <p>{t('resourceDraft.leaveHelp')}</p>
          <div className="config-actions">
            <button autoFocus className="button-secondary" disabled={saving} onClick={() => blocked.reset()}>
              {t('resourceDraft.stay')}
            </button>
            <button className="button-secondary" disabled={saving} onClick={() => blocked.proceed()}>
              {t('resourceDraft.discardLeave')}
            </button>
            <button
              className="button-primary"
              disabled={saving || formDirty || !writable}
              onClick={async () => {
                if (await save()) blocked.proceed();
              }}
            >
              {t('resourceDraft.saveLeave')}
            </button>
          </div>
        </section>
      )}
    </section>
  );
}
