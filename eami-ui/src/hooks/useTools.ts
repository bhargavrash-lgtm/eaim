import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type Tool = components['schemas']['Tool']
export type ToolCreate = components['schemas']['ToolCreate']

export function useTools() {
  return useQuery({
    queryKey: ['tools'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/gateway/tools')
      if (error) throw error
      return data
    },
    staleTime: 30_000,
  })
}

export function useCreateTool() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: ToolCreate) => {
      const { data, error } = await api.POST('/v1/gateway/tools', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tools'] }),
  })
}

export function useDeleteTool() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const { error } = await api.DELETE('/v1/gateway/tools/{toolId}', {
        params: { path: { toolId: id } },
      })
      if (error) throw error
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tools'] }),
  })
}

export function useTestTool() {
  return useMutation({
    mutationFn: async (id: string) => {
      const { data, error } = await api.POST('/v1/gateway/tools/{toolId}/test', {
        params: { path: { toolId: id } },
      })
      if (error) throw error
      return data
    },
  })
}
