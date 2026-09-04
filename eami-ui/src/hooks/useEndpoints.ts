import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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

// useLinkEndpointAgent (B-164/B-165) sets or clears an endpoint's linked
// governed gateway_agents identity -- the only write path for
// endpoints.gateway_agent_id. Pass gatewayAgentId: null to clear the link.
export function useLinkEndpointAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ endpointId, gatewayAgentId }: { endpointId: string; gatewayAgentId: string | null }) => {
      const { data, error } = await api.PATCH('/v1/endpoints/{endpointId}/link-agent', {
        params: { path: { endpointId } },
        body: { gateway_agent_id: gatewayAgentId },
      })
      if (error) throw error
      return data
    },
    onSuccess: (data, { endpointId }) => {
      // Merge the mutation's own real response straight into the cached
      // detail query instead of only invalidating -- avoids a visible
      // flicker back to the pre-mutation value while a refetch is still
      // in flight. `data` is the list-item shape (no latest_report);
      // spreading over the existing cached detail object leaves
      // latest_report (a key `data` doesn't have) untouched.
      qc.setQueryData(['endpoints', endpointId], (old: unknown) =>
        old && typeof old === 'object' ? { ...old, ...data } : old,
      )
      qc.invalidateQueries({ queryKey: ['endpoints', endpointId] })
      qc.invalidateQueries({ queryKey: ['endpoints'] })
    },
  })
}
