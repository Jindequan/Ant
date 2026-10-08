import type { ReactNode } from 'react';
import { t } from '../i18n';

/**
 * Shared four-state frame for loading, empty, error, and success views. Pages
 * can supply honest, domain-specific empty-state content as children.
 */
export type PagePhase = 'loading' | 'empty' | 'error' | 'success';

export interface PageStateFrameProps {
  phase: PagePhase;
  /** Extra diagnostic line for the error phase (no secrets). */
  errorDetail?: string;
  onRetry?: () => void;
  children?: ReactNode;
}

export function PageStateFrame({ phase, errorDetail, onRetry, children }: PageStateFrameProps) {
  switch (phase) {
    case 'loading':
      return (
        <p className="page-state page-state-loading" role="status">
          {t('state.loading')}
        </p>
      );
    case 'error':
      return (
        <div className="page-state page-state-error" role="alert">
          <p>{t('state.error')}</p>
          {errorDetail !== undefined && errorDetail.length > 0 ? (
            <p className="page-state-detail">{errorDetail}</p>
          ) : null}
          {onRetry !== undefined ? (
            <button type="button" onClick={onRetry}>
              {t('state.retry')}
            </button>
          ) : null}
        </div>
      );
    case 'empty':
      return children !== undefined ? (
        <div className="page-state page-state-empty">{children}</div>
      ) : (
        <p className="page-state page-state-empty" role="status">{t('state.empty.backendPending')}</p>
      );
    case 'success':
      return <div className="page-state page-state-success">{children}</div>;
  }
}
