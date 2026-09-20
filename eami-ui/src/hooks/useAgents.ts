import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
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

// useAgent (B-200): the existing GET /v1/gateway/agents/{agentId} single-
// fetch route (already real, already documented -- eami-api/internal/api/
// agents.go's GetAgent -- just never had a hook, since every page until
// now only ever needed the already-fetched list). Powers the new Agent
// Detail page's own load, independent of AgentsPage's list cache.
export function useAgent(id: string | null) {
  return useQuery({
    queryKey: ['agents', id],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/gateway/agents/{agentId}', {
        params: { path: { agentId: id! } },
      })
      if (error) throw error
      return data
    },
    enabled: id != null,
  })
}

// ── Agent connections (B-200) ────────────────────────────────────────────────
//
// The real, scoped relationship graph's data source (DESIGN_SYSTEM.md
// §7.1) -- GET /v1/gateway/agents/{agentId}/connections. Not in
// api/openapi.yaml yet (Architect-EAMI-owned, matching B-038/B-045's
// established precedent of shipping undocumented via apiFetch), so this
// uses the documented escape hatch, not the generated typed client, and
// every type here is hand-declared to match the real handler response
// shape exactly (eami-api/internal/api/agents.go's AgentConnectionsResp).

export type AgentToolConnection = {
  tool_id: string | null
  tool_name: string
  call_count_24h: number
  call_count_total: number
  last_dispatch_at: string
  is_active: boolean
}
export type AgentPolicyConnection = { policy_id: string; name: string; action: string }
export type AgentWorkflowConnection = { workflow_id: string; name: string }
export type AgentEndpointConnection = { endpoint_id: string; hostname: string }

export type AgentConnections = {
  tools: AgentToolConnection[]
  policies: AgentPolicyConnection[]
  workflows: AgentWorkflowConnection[]
  endpoint: AgentEndpointConnection | null
}

export function useAgentConnections(id: string | null) {
  return useQuery({
    queryKey: ['agent-connections', id],
    enabled: id != null,
    queryFn: () => apiFetch<AgentConnections>(`/v1/gateway/agents/${id}/connections`),
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
