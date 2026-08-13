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

// ExtractionRef (B-063) -- one parameter's extraction source: a prior
// step's own stable id (never a step_order position) plus a gjson path
// into that step's real recorded execution result.
export type ExtractionRef = { from_step: string; path: string }

// One step in a workflow. tool_name is response-only (joined from
// gateway_tools.name). gateway_tool_id/tool_name both come back "" when
// the step's connector was deleted (ON DELETE SET NULL) -- see
// WorkflowsPage.tsx for how that's surfaced to the admin. id (B-063) is
// always the real workflow_steps.id in a response.
export type WorkflowStep = {
  id: string
  gateway_tool_id: string
  tool_name?: string
  action: string
  input_mapping?: Record<string, ExtractionRef>
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

// id (B-063), if echoed back matching a real existing step of the SAME
// workflow, lets that step's database identity (and thus any later
// step's input_mapping.from_step reference to it) survive a save --
// omitted, or echoing an id that isn't real for this workflow, is always
// treated as "this is a new step," exactly today's behavior for a
// caller that's never sent it.
type WorkflowStepInput = {
  id?: string
  gateway_tool_id: string
  action: string
  input_mapping?: Record<string, ExtractionRef>
}

export type WorkflowCreate = {
  name: string
  status?: WorkflowStatus
  steps: WorkflowStepInput[]
}

// steps, if present (even []), fully replaces the existing step list --
// omitted leaves existing steps untouched. Mirrors ToolUpdate's
// omitted-vs-present-but-empty convention for action_paths.
export type WorkflowUpdate = {
  name?: string
  status?: WorkflowStatus
  steps?: WorkflowStepInput[]
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

// Static per-step parameters (B-059) -- a separate resource from the
// workflow/steps body above (its own table, workflow_step_params, one
// row per real step id). B-064 is this endpoint's first UI consumer;
// PUT replaces the whole params object, matching action_paths' own
// "always sent, replace whole" convention already used elsewhere.
export function useWorkflowStepParams(stepId: string | null) {
  return useQuery({
    queryKey: ['workflow-step-params', stepId],
    queryFn: () => apiFetch<Record<string, unknown>>(`/v1/gateway/workflow-steps/${stepId}/params`),
    enabled: stepId != null,
  })
}

export function useSetWorkflowStepParams() {
  return useMutation({
    mutationFn: ({ stepId, params }: { stepId: string; params: Record<string, unknown> }) =>
      apiFetch<Record<string, unknown>>(`/v1/gateway/workflow-steps/${stepId}/params`, { method: 'PUT', body: params }),
  })
}
