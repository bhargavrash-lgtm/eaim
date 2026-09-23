// useWorkspaces.ts -- B-210: real Workspace-mode data hooks.
// None of these routes are in api/openapi.yaml (Architect-EAMI-owned) --
// apiFetch throughout, same established precedent as every other
// undocumented route in this app.
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '@/api/client'
import type { PolicyCreate, PolicyUpdate } from '@/hooks/usePolicies'
import type { PolicyWithWorkspace } from '@/hooks/usePolicies'

export interface MyWorkspaceMembership {
  workspace_id: string
  workspace_name: string
  role: 'workspace_admin' | 'workspace_member'
}

export interface Workspace {
  id: string
  org_id: string
  group_id: string
  name: string
  created_at: string
  updated_at: string
  description?: string | null
}

export interface WorkspaceMember {
  user_id: string
  email: string
  role: 'workspace_admin' | 'workspace_member'
  created_at: string
}

// useMyWorkspaces -- GET /v1/workspaces/mine. The real, only-trustworthy
// signal for "which workspace(s) does the current user actually belong
// to" -- ListWorkspaces/GetWorkspace are NOT membership-gated (any
// org admin/operator/viewer can call them, confirmed via router.go's own
// comment), so this is the one query both the route guard and the nav
// context indicator must use.
export function useMyWorkspaces() {
  return useQuery({
    queryKey: ['workspaces', 'mine'],
    queryFn: () => apiFetch<{ data: MyWorkspaceMembership[] }>('/v1/workspaces/mine'),
    staleTime: 30_000,
  })
}

export function useWorkspace(id: string | undefined) {
  return useQuery({
    queryKey: ['workspaces', id],
    queryFn: () => apiFetch<Workspace>(`/v1/workspaces/${id}`),
    enabled: !!id,
  })
}

export function useWorkspaceMembers(id: string | undefined) {
  return useQuery({
    queryKey: ['workspaces', id, 'members'],
    queryFn: () => apiFetch<{ data: WorkspaceMember[] }>(`/v1/workspaces/${id}/members`),
    enabled: !!id,
  })
}

// useWorkspacePolicies -- GET /v1/workspaces/{id}/policies. Real union of
// this workspace's own policies AND the org-wide floor (workspace_id
// null), same B-214 shape PoliciesPage.tsx already consumes.
export function useWorkspacePolicies(id: string | undefined) {
  return useQuery({
    queryKey: ['workspaces', id, 'policies'],
    queryFn: () => apiFetch<{ data: PolicyWithWorkspace[] }>(`/v1/workspaces/${id}/policies`),
    enabled: !!id,
    staleTime: 30_000,
  })
}

export function useCreateWorkspacePolicy(workspaceId: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: PolicyCreate) =>
      apiFetch(`/v1/workspaces/${workspaceId}/policies`, { method: 'POST', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workspaces', workspaceId, 'policies'] }),
  })
}

export function useUpdateWorkspacePolicy(workspaceId: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: PolicyUpdate }) =>
      apiFetch(`/v1/workspaces/${workspaceId}/policies/${id}`, { method: 'PATCH', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workspaces', workspaceId, 'policies'] }),
  })
}

export function useDeleteWorkspacePolicy(workspaceId: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/v1/workspaces/${workspaceId}/policies/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workspaces', workspaceId, 'policies'] }),
  })
}
