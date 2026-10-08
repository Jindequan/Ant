import { createContext, useContext, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiJson } from '../api/client';
import type { SessionView } from '../api/types';

interface SessionContextValue {
  session: SessionView | null;
  isLoading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
  login: (token: string) => Promise<void>;
  logout: () => Promise<void>;
  isLoggingOut: boolean;
}

const SessionContext = createContext<SessionContextValue | null>(null);
const sessionQueryKey = ['session'] as const;

async function readSession(): Promise<SessionView | null> {
  try {
    return await apiJson<SessionView>('/v1/session');
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) return null;
    throw error;
  }
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: sessionQueryKey, queryFn: readSession });
  const loginMutation = useMutation({
    mutationFn: (token: string) => apiJson<SessionView>('/v1/sessions', { method: 'POST', body: { token } }),
    onSuccess: (session) => queryClient.setQueryData(sessionQueryKey, session),
  });
  const logoutMutation = useMutation({
    mutationFn: () => apiJson<void>('/v1/session', {
      method: 'DELETE',
      csrfToken: queryClient.getQueryData<SessionView | null>(sessionQueryKey)?.csrfToken,
    }),
    onSuccess: () => queryClient.setQueryData(sessionQueryKey, null),
  });

  const value: SessionContextValue = {
    session: query.data ?? null,
    isLoading: query.isPending,
    error: query.error,
    refresh: async () => { await query.refetch(); },
    login: async (token) => { await loginMutation.mutateAsync(token); },
    logout: async () => { await logoutMutation.mutateAsync(); },
    isLoggingOut: logoutMutation.isPending,
  };

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (value === null) throw new Error('useSession must be used inside SessionProvider');
  return value;
}
