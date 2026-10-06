import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
import type { components } from '@/api/schema'

export type Agent = components['schemas']['Agent']
export type AgentCreate = components['schemas']['AgentCreate']
export type AgentUpdate = components['schemas']['AgentUpdate']

// AgentWithWorkspace -- workspace_id/workspace_name (B-196 increment 1)
// aren't in api/openapi.yaml yet (Architect-EAMI-owned), same undocumented-
// field precedent as usePolicies.ts's PolicyWithWorkspace/useTools.ts's
// ToolWithActions. Nil/undefined on both means this agent has no real
// workspace assignment -- CMDB's list renders "Global floor" for that case.
export type AgentWithWorkspace = Agent & {
  workspace_id?: string | null
  workspace_name?: string | null
}

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

// Agent Lineage (Horizon 1 "Agent lineage"): GET /v1/gateway/agents/{id}/
// lineage, not yet in openapi.yaml -- the same documented apiFetch escape
// hatch as /connections above. Types mirror eami-api/internal/api/
// agent_lineage.go's AgentLineageResp exactly. A null cost means "no cost
// applies" (not an AI-provider connector, or no AI usage ever recorded)
// and renders as "—", never $0.
export type LineageWindow = '24h' | '7d' | '30d'

export type AgentLineageSummary = {
  risk_tier: string
  owner: string
  tools_ever_touched: number
  calls_in_window: number
  escalations_30d: number
  denials_30d: number
  first_seen: string | null
  last_seen: string | null
  cost_usd_window: number | null
  unpriced_calls_window: number
}
export type AgentLineageTool = {
  tool_id: string | null
  tool_name: string
  tool_type: string | null
  calls: number
  allowed: number
  escalated: number
  denied: number
  calls_total: number
  last_call_at: string | null
  cost_usd: number | null
  unpriced_calls: number
}
export type AgentLineagePolicy = { policy_id: string; name: string; action: string; hits: number; last_hit_at: string | null }
export type AgentLineageWorkflow = { workflow_id: string; name: string; runs: number; last_run_at: string }
export type AgentLineage = {
  agent_id: string
  window: LineageWindow
  window_start: string
  generated_at: string
  summary: AgentLineageSummary
  tools: AgentLineageTool[]
  policies: AgentLineagePolicy[]
  workflows: AgentLineageWorkflow[]
}

export function useAgentLineage(id: string | null, window: LineageWindow) {
  return useQuery({
    queryKey: ['agent-lineage', id, window],
    enabled: id != null,
    // Keep the previous window's data on screen while the new one loads,
    // so the window picker doesn't vanish into a spinner on every change --
    // but only for the SAME agent, never another agent's numbers.
    placeholderData: (prev, prevQuery) => (prevQuery?.queryKey[1] === id ? prev : undefined),
    queryFn: () => apiFetch<AgentLineage>(`/v1/gateway/agents/${id}/lineage?window=${window}`),
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
    // Refresh every agents query except the deleted agent's own detail query,
    // which is only marked stale: refetching it can only 404 (and retry),
    // delaying Agent Detail's post-delete redirect, while marking it stale
    // means a later visit to that URL refetches instead of showing a cached
    // ghost of the deleted agent (whichever page did the delete).
    onSuccess: (_data, id) =>
      Promise.all([
        qc.invalidateQueries({ queryKey: ['agents'], predicate: (q) => q.queryKey[1] !== id }),
        qc.invalidateQueries({ queryKey: ['agents', id], exact: true, refetchType: 'none' }),
      ]),
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
  // B-269 Slice 0 (S5): codes the stored paths would fail if added now
  // (path_root, path_profile_parent). Legacy paths stay accepted, flagged.
  path_warnings?: string[]
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
    // apiFetch injects the real session token. These hooks previously read a
    // localStorage 'access_token' key that nothing writes, so every config
    // request went out with an empty bearer and got a 401.
    queryFn: (): Promise<AgentConfig> => apiFetch<AgentConfig>(`/v1/gateway/agents/${agentId}/config`),
    staleTime: 30_000,
  })
}

export function useUpdateAgentConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: AgentConfigUpdate }): Promise<AgentConfig> =>
      apiFetch<AgentConfig>(`/v1/gateway/agents/${id}/config`, { method: 'PUT', body }),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ['agent-config', id] })
    },
  })
}
