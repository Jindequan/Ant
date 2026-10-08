import { useState } from 'react';
import { Link, NavLink, Outlet } from 'react-router-dom';
import { useSession } from '../auth/SessionContext';
import { t } from '../i18n';

const NAV_ITEMS = [
  { to: '/providers', labelKey: 'nav.providers' },
  { to: '/resources', labelKey: 'nav.resources' },
  { to: '/pools', labelKey: 'nav.pools' },
  { to: '/operations', labelKey: 'nav.operations' },
] as const;

/**
 * Single primary navigation (design 09 §1): providers / resources / pools /
 * operations, with identity and sign-out state read from the server session.
 */
export function AppLayout() {
  const { session, isLoading, error, refresh, logout, isLoggingOut } = useSession();
  const [logoutFailed, setLogoutFailed] = useState(false);
  async function signOut() {
    setLogoutFailed(false);
    try {
      await logout();
    } catch {
      setLogoutFailed(true);
    }
  }
  return (
    <div className="app-shell">
      <header className="app-header">
        <span className="app-title">
          <svg className="app-brand-mark" viewBox="0 0 32 32" aria-hidden="true">
            <path d="M8 8.5 16 16l8-7.5M16 16v8" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
            <circle cx="8" cy="8" r="3" fill="currentColor" />
            <circle cx="24" cy="8" r="3" fill="currentColor" />
            <circle cx="16" cy="16" r="3.5" fill="currentColor" />
            <circle cx="16" cy="24" r="3" fill="currentColor" />
          </svg>
          {t('app.title')}
        </span>
        <nav className="app-nav" aria-label={t('nav.ariaLabel')}>
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) => (isActive ? 'app-nav-link is-active' : 'app-nav-link')}
            >
              {t(item.labelKey)}
            </NavLink>
          ))}
        </nav>
        <div className="app-session">
          {isLoading ? <span role="status">{t('state.loading')}</span> : null}
          {!isLoading && error ? (
            <>
              <span role="alert">{t('session.status.error')}</span>
              <button type="button" className="app-nav-link" onClick={() => void refresh()}>{t('state.retry')}</button>
            </>
          ) : null}
          {!isLoading && !error && session ? (
            <>
              <span className="app-session-state">{session.namespace}</span>
              {logoutFailed ? <span role="alert">{t('session.logoutError')}</span> : null}
              <button type="button" className="app-nav-link" disabled={isLoggingOut} onClick={() => void signOut()}>
                {isLoggingOut ? t('session.signingOut') : t('session.signOut')}
              </button>
            </>
          ) : null}
          {!isLoading && !error && !session ? (
            <Link className="app-nav-link" to="/login">{t('nav.login')}</Link>
          ) : null}
        </div>
      </header>
      <main className="app-main">
        <Outlet />
      </main>
    </div>
  );
}
