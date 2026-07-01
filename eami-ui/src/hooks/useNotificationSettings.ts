import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type NotificationConfig = components['schemas']['NotificationConfig']
export type NotificationSettingsUpdate = components['schemas']['NotificationSettingsUpdate']

export function useNotificationSettings() {
  return useQuery({
    queryKey: ['settings', 'notifications'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/settings/notifications', {})
      if (error) throw error
      return data
    },
  })
}

export function useUpdateNotificationSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: NotificationSettingsUpdate) => {
      const { data, error } = await api.PUT('/v1/settings/notifications', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings', 'notifications'] }),
  })
}

export function useTestNotification() {
  return useMutation({
    mutationFn: async (channel: 'slack' | 'email') => {
      const { data, error } = await api.POST('/v1/settings/notifications/test', {
        body: { channel },
      })
      if (error) throw error
      return data
    },
  })
}
