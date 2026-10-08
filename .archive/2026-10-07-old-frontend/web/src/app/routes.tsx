import { lazy, Suspense } from 'react';
import { AppLayout } from '../components/AppLayout';
import { NotFoundPage } from '../components/NotFoundPage';
import { LoginPage } from '../features/login/LoginPage';
import { PoolsPage } from '../features/pools/PoolsPage';
import { ProvidersPage } from '../features/providers/ProvidersPage';
import { ResourcesPage } from '../features/resources/ResourcesPage';
import { InventoryAccountsPage } from '../features/resources/InventoryPage';
import { InventoryPoolsPage } from '../features/pools/InventoryPoolsPage';
import { Navigate, useSearchParams, type RouteObject } from 'react-router-dom';

const OperationsPage = lazy(() => import('../features/operations/OperationsPage').then((module) => ({ default: module.OperationsPage })));

/**
 * Route table (design 09 §1): exactly four primary sections plus login.
 * `/` redirects to the first step of the main path (providers).
 */
function ProviderEntry() { const [p]=useSearchParams(); return p.get('view')==='advanced'?<ProvidersPage />:<InventoryAccountsPage />; }
function PoolEntry() { const [p]=useSearchParams(); return p.get('view')==='active'?<PoolsPage />:<InventoryPoolsPage />; }

export const routes: RouteObject[] = [
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/providers" replace /> },
      { path: 'login', element: <LoginPage /> },
      { path: 'providers', element: <ProviderEntry /> },
      { path: 'resources', element: <ResourcesPage /> },
      { path: 'pools', element: <PoolEntry /> },
      { path: 'operations', element: <Suspense fallback={null}><OperationsPage /></Suspense> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
];
