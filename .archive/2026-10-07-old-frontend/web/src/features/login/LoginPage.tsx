import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError } from '../../api/client';
import { useSession } from '../../auth/SessionContext';
import { PageStateFrame } from '../../components/PageState';
import { t } from '../../i18n';

export function LoginPage() {
  const navigate = useNavigate();
  const { session, isLoading, error: sessionError, refresh, login } = useSession();
  const [token, setToken] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [errorKey, setErrorKey] = useState('');

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setErrorKey('');
    setIsSubmitting(true);
    try {
      await login(token);
      setToken('');
      navigate('/providers', { replace: true });
    } catch (error) {
      setErrorKey(error instanceof ApiError && error.status === 401 ? 'page.login.invalidToken' : 'page.login.failed');
    } finally {
      setIsSubmitting(false);
    }
  }

  if (isLoading) {
    return <section className="page"><h1>{t('page.login.title')}</h1><PageStateFrame phase="loading" /></section>;
  }
  if (sessionError) {
    return (
      <section className="page">
        <h1>{t('page.login.title')}</h1>
        <PageStateFrame phase="error" errorDetail={t('session.loadError')} onRetry={() => void refresh()} />
      </section>
    );
  }
  if (session) {
    return (
      <section className="page">
        <h1>{t('page.login.title')}</h1>
        <PageStateFrame phase="success">
          <p role="status">{t('page.login.alreadySignedIn', { namespace: session.namespace })}</p>
          <button type="button" className="button-primary" onClick={() => navigate('/providers', { replace: true })}>
            {t('page.login.continue')}
          </button>
        </PageStateFrame>
      </section>
    );
  }

  return (
    <section className="page">
      <h1>{t('page.login.title')}</h1>
      <form className="login-form" onSubmit={(event) => void submit(event)}>
        <label className="login-label" htmlFor="login-token">{t('page.login.tokenLabel')}</label>
        <input
          id="login-token"
          name="token"
          type="password"
          autoComplete="current-password"
          value={token}
          onChange={(event) => setToken(event.currentTarget.value)}
          required
          minLength={16}
          disabled={isSubmitting}
          aria-describedby={errorKey || undefined}
        />
        {errorKey ? <p id={errorKey} className="login-error" role="alert">{t(errorKey)}</p> : null}
        <button type="submit" className="button-primary" disabled={isSubmitting || token.length < 16}>
          {isSubmitting ? t('page.login.submitting') : t('page.login.submit')}
        </button>
        <p className="login-help">{t('page.login.help')}</p>
      </form>
    </section>
  );
}
