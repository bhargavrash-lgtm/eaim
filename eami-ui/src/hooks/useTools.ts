import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
import type { components } from '@/api/schema'

export type Tool = components['schemas']['Tool']
export type ToolCreate = components['schemas']['ToolCreate']

// ToolUpdate (B-045) -- PATCH /v1/gateway/tools/{toolId} isn't in
// api/openapi.yaml yet (Architect-EAMI-owned, out of this task's scope),
// so there's no generated type for it; defined locally to mirror
// ToolCreate's shape minus the required-field constraints (every field is
// optional -- a partial update). credentials omitted entirely means
// "leave the stored value unchanged," not "clear it" -- see useUpdateTool.
export type ToolUpdate = {
  name?: string
  mcp_command?: string
  mcp_args?: string[]
  base_url?: string
  credentials?: {
    api_key?: string
    oauth_client_id?: string
    oauth_client_secret?: string
    connection_string?: string
  }
}

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

export function useUpdateTool() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, body }: { id: string; body: ToolUpdate }) => {
      // apiFetch, not api.PATCH: the generated client has no typed call
      // for this route until openapi.yaml documents it (B-045, see
      // ToolUpdate's comment) -- this is the documented escape hatch
      // (client.ts), not a raw fetch in a component.
      return apiFetch<Tool>(`/v1/gateway/tools/${id}`, { method: 'PATCH', body })
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
