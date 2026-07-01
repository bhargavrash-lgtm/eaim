import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { STALE_TIMES } from '@/lib/query'

export interface AuditParams {
  agent_name?: string
  tool_name?: string
  decision?: 'allowed' | 'denied' | 'escalated'
  from?: string
  to?: string
  page?: number
  per_page?: number
}

export function useAudit(params?: AuditParams) {
  return useQuery({
    queryKey: ['audit', params],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/audit', {
        params: { query: params },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.DEFAULT,
  })
}
