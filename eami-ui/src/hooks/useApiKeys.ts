import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type ApiKey = components['schemas']['ApiKey']

export function useApiKeys() {
  return useQuery({
    queryKey: ['api-keys'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/auth/api-keys', {})
      if (error) throw error
      return data
    },
  })
}

export function useCreateApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: { name: string; scopes?: string[]; expires_at?: string }) => {
      const { data, error } = await api.POST('/v1/auth/api-keys', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-keys'] }),
  })
}

export function useRevokeApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (keyId: string) => {
      const { error } = await api.DELETE('/v1/auth/api-keys/{keyId}', {
        params: { path: { keyId } },
      })
      if (error) throw error
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-keys'] }),
  })
}
