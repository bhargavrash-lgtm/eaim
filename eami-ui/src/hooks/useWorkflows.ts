import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '@/api/client'

// Multi-Hop Workflows (Thread B), Brief 1 (B-058): schema + CRUD
// foundation only -- see BACKLOG.md's Thread B entry for the full epic
// (execution/mapping/approval integration are later briefs, not built
// here). None of these routes are in api/openapi.yaml yet
// (Architect-EAMI-owned, out of this brief's scope, matching B-038/B-045's
// precedent of shipping undocumented) -- every hook here uses apiFetch(),
// the documented escape hatch (client.ts), not the generated client, and
// every type is hand-declared rather than sourced from `components`.

export type WorkflowStatus = 'active' | 'draft' | 'disabled'

// One step in a workflow. tool_name is response-only (joined from
// gateway_tools.name). gateway_tool_id/tool_name both come back "" when
// the step's connector was deleted (ON DELETE SET NULL) -- see
// WorkflowsPage.tsx for how that's surfaced to the admin.
export type WorkflowStep = {
  gateway_tool_id: string
  tool_name?: string
  action: string
}

export type Workflow = {
  id: string
  name: string
  status: WorkflowStatus
  step_count?: number // list view only
  created_at: string
  updated_at?: string
  steps?: WorkflowStep[] // get/create/update responses only
}

export type WorkflowCreate = {
  name: string
  status?: WorkflowStatus
  steps: { gateway_tool_id: string; action: string }[]
}

// steps, if present (even []), fully replaces the existing step list --
// omitted leaves existing steps untouched. Mirrors ToolUpdate's
// omitted-vs-present-but-empty convention for action_paths.
export type WorkflowUpdate = {
  name?: string
  status?: WorkflowStatus
  steps?: { gateway_tool_id: string; action: string }[]
}

export function useWorkflows() {
  return useQuery({
    queryKey: ['workflows'],
    queryFn: () => apiFetch<{ data: Workflow[] }>('/v1/gateway/workflows'),
    staleTime: 30_000,
  })
}

export function useWorkflow(id: string | null) {
  return useQuery({
    queryKey: ['workflows', id],
    queryFn: () => apiFetch<Workflow>(`/v1/gateway/workflows/${id}`),
    enabled: id != null,
  })
}

export function useCreateWorkflow() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: WorkflowCreate) =>
      apiFetch<Workflow>('/v1/gateway/workflows', { method: 'POST', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workflows'] }),
  })
}

export function useUpdateWorkflow() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: WorkflowUpdate }) =>
      apiFetch<Workflow>(`/v1/gateway/workflows/${id}`, { method: 'PATCH', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workflows'] }),
  })
}

export function useDeleteWorkflow() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(`/v1/gateway/workflows/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workflows'] }),
  })
}
