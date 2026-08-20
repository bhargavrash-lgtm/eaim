import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
import type { components } from '@/api/schema'

export type Tool = components['schemas']['Tool']
export type ToolCreate = components['schemas']['ToolCreate']

// ActionPathMapping (B-046) -- one named action's routing entry for a
// rest_api tool, mirroring eami-api's ActionPathMapping/eami-gateway's
// toolrouter.ActionPathEntry JSON shape exactly. method is always sent
// explicit from the UI (defaulted to POST when a row is added) even
// though the backend itself defaults an omitted method to POST too.
//
// input_schema (B-075) is optional and purely descriptive -- populated
// only for an action generated from a parsed OpenAPI spec; a manually-typed
// mapping simply omits it, exactly as before this field existed.
export type ActionPathMapping = { path: string; method: string; input_schema?: Record<string, unknown> }

// OpenAPIDiscoverAction (B-075) is one candidate action returned by
// POST /v1/gateway/openapi/discover -- mirrors eami-api's
// openapidiscovery.Action JSON shape.
export type OpenAPIDiscoverAction = {
  path: string
  method: string
  summary?: string
  input_schema?: Record<string, unknown>
  has_path_params: boolean
}

export type OpenAPIDiscoverResult = {
  actions: Record<string, OpenAPIDiscoverAction>
  warnings?: string[]
}

// action_paths isn't in api/openapi.yaml yet either (same undocumented-
// route situation as ToolUpdate below) -- Tool/ToolCreate are frozen
// generated types, so the field is carried via these local intersection
// types rather than editing the generated schema.
//
// provider/audit_mode (AI Provider Connector, Thread A Model 1) follow the
// identical undocumented-field convention. "ai_provider" additionally
// isn't a member of the generated type's literal `type` union (openapi.yaml
// still only documents mcp/rest_api/database) -- Omit+redeclare widens
// just that one field rather than editing the generated schema.
export type ToolProvider = 'claude'
export type ToolAuditMode = 'full' | 'structural_metadata_only'
export type ToolType = 'mcp' | 'rest_api' | 'database' | 'ai_provider'

export type ToolWithActions = Omit<Tool, 'type'> & {
  type: ToolType
  action_paths?: Record<string, ActionPathMapping>
  provider?: ToolProvider
  audit_mode?: ToolAuditMode
}
export type ToolCreateWithActions = Omit<ToolCreate, 'type'> & {
  type: ToolType
  action_paths?: Record<string, ActionPathMapping>
  provider?: ToolProvider
  audit_mode?: ToolAuditMode
}

// ToolUpdate (B-045) -- PATCH /v1/gateway/tools/{toolId} isn't in
// api/openapi.yaml yet (Architect-EAMI-owned, out of this task's scope),
// so there's no generated type for it; defined locally to mirror
// ToolCreate's shape minus the required-field constraints (every field is
// optional -- a partial update). credentials omitted entirely means
// "leave the stored value unchanged," not "clear it" -- see useUpdateTool.
// action_paths (B-046) follows the same omitted-vs-present convention:
// omit to leave existing mappings unchanged, {} to explicitly clear them.
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
  action_paths?: Record<string, ActionPathMapping>
  provider?: ToolProvider
  audit_mode?: ToolAuditMode
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
    mutationFn: async (body: ToolCreateWithActions) => {
      // action_paths/provider/audit_mode aren't in the generated ToolCreate
      // type (see ToolCreateWithActions' comment) -- extra fields are
      // structurally fine as-is and still serialized and sent at runtime.
      // `type` needed an explicit cast though: ToolCreateWithActions widens
      // it to include "ai_provider", which the generated (openapi.yaml)
      // type doesn't know about -- a real type mismatch, not just an extra
      // field, since `type` exists on both sides. The cast is type-only;
      // the actual runtime request body is unchanged.
      const { data, error } = await api.POST('/v1/gateway/tools', { body: body as unknown as ToolCreate })
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

// useDiscoverOpenAPI (B-075) parses an admin-supplied OpenAPI spec (upload/
// paste or URL) into candidate action_paths entries for review -- purely a
// preview, writes nothing. Not a query-invalidating mutation: nothing in
// the tools list changes until the admin separately reviews/edits the
// result and saves it through useUpdateTool, exactly like any other
// action_paths edit.
export function useDiscoverOpenAPI() {
  return useMutation({
    mutationFn: async (body: { spec_url?: string; spec_content?: string }) => {
      // apiFetch, not api.POST: undocumented route (same B-045 precedent
      // as useUpdateTool above) -- api/openapi.yaml doesn't know about it.
      return apiFetch<OpenAPIDiscoverResult>('/v1/gateway/openapi/discover', { method: 'POST', body })
    },
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
