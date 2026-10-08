import { Link } from 'react-router-dom';
import { t } from '../i18n';

export function NotFoundPage() {
  return (
    <section className="page">
      <h1>{t('notFound.title')}</h1>
      <Link to="/providers">{t('notFound.back')}</Link>
    </section>
  );
}
