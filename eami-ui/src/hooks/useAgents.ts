import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type Agent = components['schemas']['Agent']
export type AgentCreate = components['schemas']['AgentCreate']
export type AgentUpdate = components['schemas']['AgentUpdate']

export function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/gateway/agents')
      if (error) throw error
      return data
    },
    staleTime: 30_000,
  })
}

export function useCreateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: AgentCreate) => {
      const { data, error } = await api.POST('/v1/gateway/agents', { body })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] }),
  })
}

export function useUpdateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, body }: { id: string; body: AgentUpdate }) => {
      const { data, error } = await api.PATCH('/v1/gateway/agents/{agentId}', {
        params: { path: { agentId: id } },
        body,
      })
      if (error) throw error
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] }),
  })
}

export function useDeleteAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const { error } = await api.DELETE('/v1/gateway/agents/{agentId}', {
        params: { path: { agentId: id } },
      })
      if (error) throw error
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] }),
  })
}

// ── Agent config hooks ────────────────────────────────────────────────────────

export interface AgentConfig {
  agent_id: string
  scan_interval_seconds: number
  model_scan_paths: string[]
  max_report_size_bytes: number
  enabled_scanners: string[]
  updated_at: string
}

export interface AgentConfigUpdate {
  scan_interval_seconds?: number
  model_scan_paths?: string[]
  max_report_size_bytes?: number
  enabled_scanners?: string[]
}

export function useAgentConfig(agentId: string | null) {
  return useQuery({
    queryKey: ['agent-config', agentId],
    enabled: !!agentId,
    queryFn: async (): Promise<AgentConfig> => {
      const res = await fetch(`/v1/gateway/agents/${agentId}/config`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('access_token') ?? ''}` },
      })
      if (!res.ok) throw new Error(`GET /config: ${res.status}`)
      return res.json()
    },
    staleTime: 30_000,
  })
}

export function useUpdateAgentConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, body }: { id: string; body: AgentConfigUpdate }): Promise<AgentConfig> => {
      const res = await fetch(`/v1/gateway/agents/${id}/config`, {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('access_token') ?? ''}`,
        },
        body: JSON.stringify(body),
      })
      if (!res.ok) throw new Error(`PUT /config: ${res.status}`)
      return res.json()
    },
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ['agent-config', id] })
    },
  })
}
