import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
import type { components } from '@/api/schema'

export type OrgUser = components['schemas']['OrgUser']
export type UserRole = 'admin' | 'operator' | 'approver' | 'viewer'

export function useUsers() {
  return useQuery({
    queryKey: ['users'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/users', {})
      if (error) throw error
      return data
    },
  })
}

export function useInviteUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: { email: string; role: UserRole }) => {
      const { data, error } = await api.POST('/v1/users/invite', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

export function useChangeUserRole() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ userId, role }: { userId: string; role: UserRole }) => {
      const { data, error } = await api.PUT('/v1/users/{userId}/role', {
        params: { path: { userId } },
        body: { role },
      })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

// useAdminGenerateResetLink -- B-212 security correction. POST
// /v1/users/{userId}/reset-link isn't in api/openapi.yaml yet
// (Architect-EAMI-owned), same disclosed apiFetch escape-hatch precedent
// as every other undocumented route in this app. This is the only real
// delivery mechanism for a password reset given no email infra exists:
// POST /v1/auth/request-reset (the self-service one, unauthenticated)
// deliberately never returns or logs a usable token -- an admin must
// generate the link explicitly here, same trust model as inviting a user.
export function useAdminGenerateResetLink() {
  return useMutation({
    mutationFn: async (userId: string) => {
      return apiFetch<{ reset_link: string; expires_at: string }>(
        `/v1/users/${userId}/reset-link`,
        { method: 'POST' },
      )
    },
  })
}

export function useRevokeUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (userId: string) => {
      const { error } = await api.DELETE('/v1/users/{userId}', {
        params: { path: { userId } },
      })
      if (error) throw error
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}
