import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { AppProviders } from './providers';
import { routes } from './routes';

export interface AppProps {
  /** Injected so tests can mount the same tree with a memory router. */
  router: ReturnType<typeof createBrowserRouter>;
}

export function App({ router }: AppProps) {
  return (
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>
  );
}

export function createDefaultAppRouter(): ReturnType<typeof createBrowserRouter> {
  return createBrowserRouter(routes);
}
