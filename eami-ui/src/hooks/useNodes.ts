import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type GatewayNode = components['schemas']['Node']

export function useNodes() {
  return useQuery({
    queryKey: ['nodes'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/gateway/nodes')
      if (error) throw error
      return data
    },
    staleTime: 15_000,
    refetchInterval: 15_000,
  })
}

export function useDeleteNode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const { error } = await api.DELETE('/v1/gateway/nodes/{nodeId}', {
        params: { path: { nodeId: id } },
      })
      if (error) throw error
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['nodes'] }),
  })
}
