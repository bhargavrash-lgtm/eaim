import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { STALE_TIMES } from '@/lib/query'

/** Active sessions — polls every 5s */
export function useActiveSessions() {
  return useQuery({
    queryKey: ['gateway', 'agents', 'active'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/gateway/agents', {
        params: { query: { status: 'active' } },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.ACTIVE_SESSIONS,
    refetchInterval: STALE_TIMES.ACTIVE_SESSIONS,
  })
}

/** Pending approvals count — polls every 5s */
export function usePendingApprovals() {
  return useQuery({
    queryKey: ['approvals', 'pending'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/approvals', {
        params: { query: { status: 'pending', per_page: 1 } },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.APPROVALS,
    refetchInterval: STALE_TIMES.APPROVALS,
  })
}

/** Current month FinOps summary */
export function useMonthlySpend() {
  const now = new Date()
  const from = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-01`
  const to = now.toISOString().split('T')[0]

  return useQuery({
    queryKey: ['finops', 'summary', from, to],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/finops/summary', {
        params: { query: { from, to } },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.DEFAULT,
  })
}
