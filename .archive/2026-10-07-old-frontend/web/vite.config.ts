import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';
import { loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'ANT_');
  const apiTarget = env['ANT_API_PROXY_TARGET'];
  return {
    plugins: [react()],
    ...(apiTarget
      ? {
          server: {
            proxy: {
              '/v1': { target: apiTarget },
              '/healthz': { target: apiTarget },
              '/readyz': { target: apiTarget },
            },
          },
        }
      : {}),
    test: {
      environment: 'jsdom',
      include: ['tests/**/*.test.ts', 'tests/**/*.test.tsx'],
      setupFiles: ['./tests/setup.ts'],
    },
  };
});
