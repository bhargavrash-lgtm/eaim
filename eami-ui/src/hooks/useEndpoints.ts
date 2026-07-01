import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { STALE_TIMES } from '@/lib/query'

interface EndpointParams {
  has_ai?: boolean
  has_local_model?: boolean
  search?: string
  page?: number
  per_page?: number
}

export function useEndpoints(params?: EndpointParams) {
  return useQuery({
    queryKey: ['endpoints', params],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/endpoints', {
        params: { query: params },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.DEFAULT,
  })
}

export function useEndpoint(endpointId: string) {
  return useQuery({
    queryKey: ['endpoints', endpointId],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/endpoints/{endpointId}', {
        params: { path: { endpointId } },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.DEFAULT,
    enabled: Boolean(endpointId),
  })
}
