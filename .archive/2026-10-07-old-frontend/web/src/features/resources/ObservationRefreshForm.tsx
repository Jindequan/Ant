import { useRef, useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError } from '../../api/client';
import { newCommandId } from '../../api/config';
import { refreshResourceObservation, type ObservationRefreshInput } from '../../api/resources';
import type { ResourceView } from '../../api/types';
import { t } from '../../i18n';

function localDateTimeInput(date: Date): string {
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 401) return t('config.error.session');
  if (error instanceof ApiError && error.status === 403) return t('config.error.permission');
  if (error instanceof ApiError && error.status === 404) return t('resources.observe.error.notFound');
  if (error instanceof ApiError && error.status === 409) return t('resources.observe.error.conflict');
  return t('resources.observe.error.request');
}

export function ObservationRefreshForm({ resource, csrfToken, onClose }: { resource: ResourceView; csrfToken: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [sourceRef, setSourceRef] = useState('');
  const [remaining, setRemaining] = useState('');
  const [observedAt, setObservedAt] = useState(() => localDateTimeInput(new Date()));
  const [expiresAt, setExpiresAt] = useState('');
  const pendingCommand = useRef<{ fingerprint: string; body: ObservationRefreshInput } | null>(null);
  const mutation = useMutation({
    mutationFn: (body: ObservationRefreshInput) => refreshResourceObservation(resource.resourceId, body, csrfToken),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['pools'] });
    },
    onError: async (error) => {
      if (error instanceof ApiError && error.status === 401) queryClient.setQueryData(['session'], null);
      if (error instanceof ApiError && (error.status === 404 || error.status === 409)) {
        await queryClient.invalidateQueries({ queryKey: ['pools'] });
      }
    },
  });

  const observedMillis = observedAt ? new Date(observedAt).getTime() : Number.NaN;
  const expiryMillis = expiresAt ? new Date(expiresAt).getTime() : Number.NaN;
  const validQuantity = /^(0|[1-9][0-9]{0,18})$/.test(remaining) && BigInt(remaining || '0') <= 9_223_372_036_854_775_807n;
  const validDates = Number.isFinite(observedMillis) && Number.isFinite(expiryMillis) && expiryMillis > observedMillis && expiryMillis > Date.now();
  const ready = sourceRef.trim().length > 0 && validQuantity && validDates && !mutation.isPending;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready) return;
    const evidence = {
      sourceRef: sourceRef.trim(),
      observedAtMillis: observedMillis,
      expiresAtMillis: expiryMillis,
      remaining,
      committedAtStart: resource.committed,
      adjustmentVersionAtStart: resource.adjustmentVersion,
    };
    const fingerprint = JSON.stringify([resource.windowKey, evidence]);
    if (pendingCommand.current?.fingerprint !== fingerprint) {
      pendingCommand.current = {
        fingerprint,
        body: { commandId: newCommandId(), evidence },
      };
    }
    mutation.mutate(pendingCommand.current.body);
  }

  function resetResult() {
    mutation.reset();
  }

  const saved = mutation.data?.status === 'stored';

  return <section className="config-state resource-observation" aria-labelledby="resource-observation-title">
    <header className="resource-observation-heading">
      <h2 id="resource-observation-title">{t('resources.observe.title')}</h2>
      <button className="button-secondary" type="button" onClick={onClose}>{t('resources.observe.close')}</button>
    </header>
    <div className="resource-observation-body">
      <p className="config-note">{t('resources.observe.help')}</p>
      <dl className="resource-observation-baseline">
        <div><dt>{t('resources.observe.window')}</dt><dd>{resource.windowKey}</dd></div>
        <div><dt>{t('resources.observe.committed')}</dt><dd>{resource.committed}</dd></div>
        <div><dt>{t('resources.observe.adjustmentVersion')}</dt><dd>{resource.adjustmentVersion}</dd></div>
      </dl>
      <form className="resource-observation-form" onSubmit={submit}>
        <label>{t('resources.observe.source')}
          <input value={sourceRef} onChange={(event) => { setSourceRef(event.target.value); resetResult(); }} required maxLength={256} disabled={saved} aria-invalid={sourceRef !== '' && !sourceRef.trim()} />
          {sourceRef !== '' && !sourceRef.trim() ? <small className="resource-observation-field-error">{t('resources.observe.invalidSource')}</small> : null}
        </label>
        <label>{t('resources.observe.remaining', { unit: resource.unit })}
          <input value={remaining} onChange={(event) => { setRemaining(event.target.value); resetResult(); }} inputMode="numeric" pattern="^(0|[1-9][0-9]{0,18})$" required disabled={saved} aria-invalid={remaining !== '' && !validQuantity} aria-describedby={`observation-quantity-${resource.resourceId}`} />
          <small id={`observation-quantity-${resource.resourceId}`}>{t('resources.observe.quantityHelp')}</small>
          {remaining !== '' && !validQuantity ? <small className="resource-observation-field-error">{t('resources.observe.invalidQuantity')}</small> : null}
        </label>
        <label>{t('resources.observe.observedAt')}
          <input type="datetime-local" value={observedAt} onChange={(event) => { setObservedAt(event.target.value); resetResult(); }} required disabled={saved} />
        </label>
        <label>{t('resources.observe.expiresAt')}
          <input type="datetime-local" value={expiresAt} onChange={(event) => { setExpiresAt(event.target.value); resetResult(); }} required disabled={saved} aria-invalid={expiresAt !== '' && !validDates} />
          {observedAt !== '' && expiresAt !== '' && !validDates ? <small className="resource-observation-field-error">{t('resources.observe.invalidDates')}</small> : null}
        </label>
        <p className="config-note">{t('resources.observe.baselineHelp')}</p>
        <button className="button-primary" type="submit" disabled={!ready || saved}>
          {mutation.isPending ? t('resources.observe.saving') : t('resources.observe.submit')}
        </button>
        {!ready && !mutation.isPending ? <p className="config-note">{t('resources.observe.disabledHelp')}</p> : null}
        {mutation.data?.status === 'stored' ? <p className="config-success" role="status" aria-live="polite">{t('resources.observe.saved')}</p> : null}
        {mutation.data?.status === 'rejectedStale' ? <p className="config-alert" role="alert">{t('resources.observe.stale')}</p> : null}
        {mutation.data?.status === 'sourceUnavailable' ? <p className="config-alert" role="alert">{t('resources.observe.sourceUnavailable')}</p> : null}
        {mutation.error ? <p className="config-alert" role="alert">{errorMessage(mutation.error)}</p> : null}
      </form>
    </div>
  </section>;
}
