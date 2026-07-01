import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { STALE_TIMES } from '@/lib/query'

export function useFinOpsSummary(from: string, to: string) {
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
    enabled: Boolean(from && to),
  })
}

export function useFinOpsTimeSeries(
  from: string,
  to: string,
  granularity: 'hour' | 'day' | 'week' = 'day',
) {
  return useQuery({
    queryKey: ['finops', 'timeseries', from, to, granularity],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/finops/timeseries', {
        params: { query: { from, to, granularity } },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.DEFAULT,
    enabled: Boolean(from && to),
  })
}
