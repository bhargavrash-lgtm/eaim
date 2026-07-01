import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type Policy = components['schemas']['Policy']
export type PolicyCreate = components['schemas']['PolicyCreate']
export type PolicyUpdate = components['schemas']['PolicyUpdate']

export function usePolicies() {
  return useQuery({
    queryKey: ['policies'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/gateway/policies')
      if (error) throw error
      return data
    },
    staleTime: 30_000,
  })
}

export function useCreatePolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: PolicyCreate) => {
      const { data, error } = await api.POST('/v1/gateway/policies', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })
}

export function useUpdatePolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, body }: { id: string; body: PolicyUpdate }) => {
      const { data, error } = await api.PATCH('/v1/gateway/policies/{policyId}', {
        params: { path: { policyId: id } },
        body,
      })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })
}

export function useDeletePolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const { error } = await api.DELETE('/v1/gateway/policies/{policyId}', {
        params: { path: { policyId: id } },
      })
      if (error) throw error
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })
}

export function useReorderPolicies() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (orderedIds: string[]) => {
      const { data, error } = await api.POST('/v1/gateway/policies/reorder', {
        body: { order: orderedIds },
      })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })
}
