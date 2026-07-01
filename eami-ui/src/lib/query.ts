import { QueryClient } from '@tanstack/react-query'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

/** Stale times for specific resources */
export const STALE_TIMES = {
  DEFAULT: 30_000,
  APPROVALS: 5_000,
  ACTIVE_SESSIONS: 5_000,
  ALERTS: 10_000,
} as const
