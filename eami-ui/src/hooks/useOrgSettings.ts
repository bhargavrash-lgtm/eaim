import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type OrgSettings = components['schemas']['OrgSettings']
export type OrgSettingsUpdate = components['schemas']['OrgSettingsUpdate']

export function useOrgSettings() {
  return useQuery({
    queryKey: ['settings', 'org'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/settings/org', {})
      if (error) throw error
      return data
    },
  })
}

export function useUpdateOrgSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: OrgSettingsUpdate) => {
      const { data, error } = await api.PUT('/v1/settings/org', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings', 'org'] }),
  })
}
