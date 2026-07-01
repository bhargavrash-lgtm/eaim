import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type AlertRule = components['schemas']['AlertRule']
export type AlertRuleCreate = components['schemas']['AlertRuleCreate']
export type AlertRuleUpdate = components['schemas']['AlertRuleUpdate']

export function useAlertRules() {
  return useQuery({
    queryKey: ['alert-rules'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/alerts/rules')
      if (error) throw error
      return data
    },
    staleTime: 30_000,
  })
}

export function useCreateAlertRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: AlertRuleCreate) => {
      const { data, error } = await api.POST('/v1/alerts/rules', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
}

export function useUpdateAlertRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, body }: { id: string; body: AlertRuleUpdate }) => {
      const { data, error } = await api.PUT('/v1/alerts/rules/{id}', {
        params: { path: { id } },
        body,
      })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
}

export function useDeleteAlertRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const { error } = await api.DELETE('/v1/alerts/rules/{id}', {
        params: { path: { id } },
      })
      if (error) throw error
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
}
