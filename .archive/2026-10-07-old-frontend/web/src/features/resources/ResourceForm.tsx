import { useState } from 'react';
import type { ConfigDocument, Resource, Window } from '../../api/types';
import { t } from '../../i18n';
import { declaredResource, declaredTimestamp, draftWindow, validResourceInput } from './resource-draft';

interface Props {
  document: ConfigDocument;
  resource?: Resource;
  disabled: boolean;
  onApply: (resource: Resource) => void;
  onCancel: () => void;
}

export function ResourceForm({ document, resource, disabled, onApply, onCancel }: Props) {
  const [resourceId, setResourceId] = useState(resource?.resourceId ?? crypto.randomUUID());
  const [owner, setOwner] = useState(
    resource
      ? `${resource.ownerKind}:${resource.ownerId}`
      : document.accounts[0]
        ? `account:${document.accounts[0].accountId}`
        : '',
  );
  const [metric, setMetric] = useState(resource?.metricId ?? 'metric.tokens.total');
  const [unit, setUnit] = useState(resource?.unit ?? 'token');
  const [precision, setPrecision] = useState(String(resource?.quantumExp ?? 0));
  const [role, setRole] = useState(
    resource?.kind === 'occupancy' ? 'occupancy' : (resource?.commercialRole ?? 'limit'),
  );
  const [capacity, setCapacity] = useState(resource?.configuredCapacity ?? '');
  const [kind, setKind] = useState<Window['kind']>(resource?.window.kind ?? 'fixed');
  const [period, setPeriod] = useState(resource?.window.period ?? 'month');
  const [zone, setZone] = useState(resource?.window.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone);
  const [anchor, setAnchor] = useState(resource?.window.anchorISO ?? '');
  const [duration, setDuration] = useState(String(resource?.window.durationMillis ?? 60000));
  const [metered, setMetered] = useState(resource?.metered ?? false);
  const [currency, setCurrency] = useState(resource?.currency ?? '');
  const [expiry, setExpiry] = useState(
    resource?.expiresAtMillis ? new Date(resource.expiresAtMillis).toISOString() : '',
  );
  const [error, setError] = useState('');

  function submit() {
    const split = owner.indexOf(':');
    const ownerKind = owner.slice(0, split) as Resource['ownerKind'];
    const ownerId = owner.slice(split + 1);
    const exists =
      ownerKind === 'account'
        ? document.accounts.some((item) => item.accountId === ownerId)
        : ownerKind === 'binding' && document.bindings.some((item) => item.bindingId === ownerId);
    const definition: Resource = {
      resourceId: resourceId.trim(),
      ownerKind,
      ownerId,
      metricId: metric.trim(),
      unit: unit.trim(),
      quantumExp: Number(precision),
      kind: role === 'occupancy' ? 'occupancy' : 'counter',
      commercialRole: role === 'entitlement' ? 'entitlement' : 'limit',
      semanticsRef: 'sem.pending',
      semanticsRevision: 'pending',
      window: draftWindow(kind, period, zone.trim(), anchor, duration),
      metered,
      configuredCapacity: capacity.trim(),
      capacityEvidenceRef: '',
      ...(currency.trim() ? { currency: currency.trim() } : {}),
      ...(expiry.trim() ? { expiresAtMillis: declaredTimestamp(expiry.trim()) ?? NaN } : {}),
    };
    if (
      (kind === 'fixed' && anchor.trim() && declaredTimestamp(anchor.trim()) === null) ||
      !exists ||
      !precision.trim() ||
      !validResourceInput(definition) ||
      (kind === 'rolling' && !/^[1-9][0-9]*$/.test(duration)) ||
      (!resource && document.resources.some((item) => item.resourceId === definition.resourceId))
    ) {
      setError(t('resourceDraft.invalid'));
      return;
    }
    onApply(declaredResource(definition, resource));
  }

  return (
    <section className="config-state" aria-labelledby="resource-form-title">
      <h2 id="resource-form-title">{t(resource ? 'resourceDraft.edit' : 'resourceDraft.add')}</h2>
      <p className="config-note">{t('resourceDraft.declaration')}</p>
      {error && (
        <p role="alert" className="config-alert">
          {error}
        </p>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (!disabled) submit();
        }}
      >
        <fieldset disabled={disabled} className="resource-draft-fields config-account-form">
          <label>
            {t('resourceDraft.id')}
            <input
              value={resourceId}
              readOnly={!!resource}
              maxLength={64}
              onChange={(event) => setResourceId(event.currentTarget.value)}
            />
          </label>
          <label>
            {t('resourceDraft.owner')}
            <select value={owner} onChange={(event) => setOwner(event.currentTarget.value)}>
              <option value="">{t('resourceDraft.chooseOwner')}</option>
              {document.accounts.map((item) => (
                <option key={item.accountId} value={`account:${item.accountId}`}>
                  {item.displayName} · {item.providerTypeId}
                </option>
              ))}
              {document.bindings.map((item) => (
                <option key={item.bindingId} value={`binding:${item.bindingId}`}>
                  {item.bindingId} · {item.domain}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t('resourceDraft.metric')}
            <input
              value={metric}
              maxLength={64}
              list="resource-metrics"
              onChange={(event) => setMetric(event.currentTarget.value)}
            />
            <datalist id="resource-metrics">
              <option value="metric.tokens.total" />
              <option value="metric.requests" />
              <option value="metric.usd.cents" />
              <option value="metric.concurrency.request" />
            </datalist>
          </label>
          <label>
            {t('resourceDraft.unit')}
            <input
              value={unit}
              maxLength={32}
              list="resource-units"
              onChange={(event) => setUnit(event.currentTarget.value)}
            />
            <datalist id="resource-units">
              <option value="token" />
              <option value="request" />
              <option value="usd_cents" />
              <option value="slot" />
            </datalist>
          </label>
          <label>
            {t('resourceDraft.precision')}
            <input
              type="number"
              min="0"
              max="18"
              step="1"
              value={precision}
              onChange={(event) => setPrecision(event.currentTarget.value)}
            />
          </label>
          <label>
            {t('resourceDraft.role')}
            <select value={role} onChange={(event) => setRole(event.currentTarget.value)}>
              {['limit', 'entitlement', 'occupancy'].map((value) => (
                <option key={value} value={value}>
                  {t(`resources.role.${value}`)}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t('resourceDraft.capacity')}
            <input
              aria-label={t('resourceDraft.capacity')}
              inputMode="numeric"
              value={capacity}
              onChange={(event) => setCapacity(event.currentTarget.value)}
            />
            <small>{t('resourceDraft.quantityHelp')}</small>
          </label>
          <label>
            {t('resourceDraft.window')}
            <select value={kind} onChange={(event) => setKind(event.currentTarget.value as Window['kind'])}>
              {['fixed', 'rolling', 'lifetime'].map((value) => (
                <option key={value} value={value}>
                  {t(`resourceDraft.window.${value}`)}
                </option>
              ))}
            </select>
          </label>
          {kind === 'fixed' && (
            <>
              <label>
                {t('resourceDraft.period')}
                <select
                  value={period}
                  onChange={(event) => setPeriod(event.currentTarget.value as NonNullable<Window['period']>)}
                >
                  {['minute', 'hour', 'day', 'week', 'month'].map((value) => (
                    <option key={value} value={value}>
                      {t(`resourceDraft.period.${value}`)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                {t('resourceDraft.zone')}
                <input
                  value={zone}
                  onChange={(event) => setZone(event.currentTarget.value)}
                  placeholder="Asia/Singapore"
                />
              </label>
              <label>
                {t('resourceDraft.anchor')}
                <input
                  aria-label={t('resourceDraft.anchor')}
                  value={anchor}
                  onChange={(event) => setAnchor(event.currentTarget.value)}
                  placeholder="2026-10-01T00:00:00+08:00"
                />
                <small>{t('resourceDraft.anchorHelp')}</small>
              </label>
            </>
          )}
          {kind === 'rolling' && (
            <label>
              {t('resourceDraft.duration')}
              <input
                inputMode="numeric"
                value={duration}
                onChange={(event) => setDuration(event.currentTarget.value)}
              />
            </label>
          )}
          <label>
            {t('resourceDraft.currency')}
            <input
              value={currency}
              maxLength={3}
              onChange={(event) => setCurrency(event.currentTarget.value)}
              placeholder="USD"
            />
          </label>
          <label>
            {t('resourceDraft.expiry')}
            <input
              aria-label={t('resourceDraft.expiry')}
              value={expiry}
              onChange={(event) => setExpiry(event.currentTarget.value)}
              placeholder="2027-01-01T00:00:00+08:00"
            />
            <small>{t('resourceDraft.expiryHelp')}</small>
          </label>
          <label className="resource-draft-checkbox">
            <input type="checkbox" checked={metered} onChange={(event) => setMetered(event.currentTarget.checked)} />
            {t('resourceDraft.metered')}
          </label>
        </fieldset>
        <div className="config-actions">
          <button className="button-primary" disabled={disabled} type="submit">
            {t('resourceDraft.apply')}
          </button>
          <button className="button-secondary" type="button" disabled={disabled} onClick={onCancel}>
            {t('resourceDraft.cancel')}
          </button>
        </div>
      </form>
    </section>
  );
}
