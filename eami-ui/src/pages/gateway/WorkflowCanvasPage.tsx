// WorkflowCanvasPage.tsx -- Gateway / Workflows / Canvas (B-066 Brief 1)
//
// A read-only canvas rendering of one workflow's real steps, using
// @xyflow/react -- the first brief of a new, separate Workflow Canvas
// epic (distinct from Thread B's now-complete backend epic). Reached via
// its own route, not wired into WorkflowsPage.tsx's live-editing state
// at all (StepsEditor/StepConfigPanel/AddWorkflowPanel/EditWorkflowPanel
// are completely untouched) -- B-065's card UI remains the only editor
// until a later brief in this epic reaches editing parity.
//
// Deliberately, explicitly read-only per this brief's own scope: no
// drag-to-reposition persistence, no edge drawing/editing, no
// click-to-configure. `nodesDraggable`/`nodesConnectable`/
// `elementsSelectable` are all false on <ReactFlow> itself, and every
// Handle is `isConnectable={false}` -- there is no code path here that
// could mutate anything, not just an absent save button.
import { useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import {
  ReactFlow,
  Handle,
  Position,
  type Node,
  type Edge,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { AlertTriangle } from 'lucide-react'
import { PageHeader, EmptyState, LoadingSpinner } from '@/components/common'
import { apiFetch } from '@/api/client'
import { useWorkflow } from '@/hooks/useWorkflows'
import { useTools } from '@/hooks/useTools'
import type { ToolWithActions } from '@/hooks/useTools'
import { summarizeParams } from './WorkflowsPage'
import type { ParamRow, StepRow } from './WorkflowsPage'

type WorkflowNodeData = {
  stepNumber: number
  connectorName: string
  action: string
  toolDeleted: boolean
  summary: string
  invalid: boolean
}

// WorkflowStepNode mirrors B-065's card styling (same Tailwind classes,
// same red-border/warning treatment) so the canvas and card views read
// as the same product, not two disconnected UIs.
function WorkflowStepNode({ data }: NodeProps & { data: WorkflowNodeData }) {
  const flagged = data.toolDeleted || data.invalid
  return (
    <div className={`rounded-lg border bg-white shadow-sm p-3 w-64 ${flagged ? 'border-red-200' : 'border-gray-200'}`}>
      <Handle type="target" position={Position.Top} isConnectable={false} />
      <div className="flex items-start gap-2">
        <span className="flex items-center justify-center h-6 w-6 rounded-full bg-indigo-100 text-indigo-700 text-xs font-semibold shrink-0">
          {data.stepNumber}
        </span>
        <div className="min-w-0">
          <div className="flex items-center gap-1.5">
            {data.toolDeleted && <AlertTriangle className="h-3.5 w-3.5 text-red-500 shrink-0" />}
            <span className="text-sm font-medium text-gray-900 truncate">{data.connectorName}</span>
          </div>
          <div className="text-xs text-gray-500 font-mono truncate">{data.action || '(no action selected)'}</div>
          <div className={`text-xs mt-0.5 ${data.invalid ? 'text-red-600' : 'text-gray-400'}`}>{data.summary}</div>
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} isConnectable={false} />
    </div>
  )
}

const nodeTypes = { workflowStep: WorkflowStepNode }

export function WorkflowCanvasPage() {
  const { workflowId } = useParams<{ workflowId: string }>()
  const { data: workflow, isLoading, error } = useWorkflow(workflowId ?? null)
  const { data: toolsData } = useTools()
  const tools: ToolWithActions[] = (toolsData as any)?.data ?? []

  // Same per-step static-params fetch EditWorkflowPanel already
  // established (useWorkflows.ts's useWorkflowStepParams wraps the
  // identical endpoint) -- every step from a real GetWorkflow response
  // always has a real id (B-063), so this list is exactly workflow.steps.
  const stepIds = (workflow?.steps ?? []).map(s => s.id)
  const paramsQueries = useQueries({
    queries: stepIds.map(id => ({
      queryKey: ['workflow-step-params', id],
      queryFn: () => apiFetch<Record<string, unknown>>(`/v1/gateway/workflow-steps/${id}/params`),
    })),
  })

  if (isLoading) return <div className="p-6"><LoadingSpinner /></div>
  if (error || !workflow) return <div className="p-6 text-sm text-red-500">Failed to load workflow.</div>

  const steps = workflow.steps ?? []

  // Builds the SAME local row shape EditWorkflowPanel's own row-init
  // constructs, purely to call the imported summarizeParams -- never
  // rendered as an editable row, never mutated, no onChange anywhere.
  const rows: StepRow[] = steps.map((s, idx) => {
    const staticParams = (paramsQueries[idx]?.data ?? {}) as Record<string, unknown>
    const staticRows: ParamRow[] = Object.entries(staticParams).map(([key, value]) => ({
      key, mode: 'static', value: typeof value === 'string' ? value : JSON.stringify(value),
      fromStepLocalId: '', path: '',
    }))
    const extractRows: ParamRow[] = Object.entries(s.input_mapping ?? {}).map(([key, ref]) => ({
      key, mode: 'extract', value: '', fromStepLocalId: ref.from_step, path: ref.path,
    }))
    return {
      localId: s.id,
      id: s.id,
      gatewayToolId: s.gateway_tool_id,
      action: s.action,
      toolDeleted: !s.gateway_tool_id,
      params: [...staticRows, ...extractRows],
    }
  })

  const invalidByStep = rows.map(row => row.params.some(p => p.mode === 'extract' && p.invalid))

  const nodes: Node<WorkflowNodeData>[] = rows.map((row, i) => ({
    id: row.localId,
    type: 'workflowStep',
    // Simple deterministic vertical stack from step_order (workflow.
    // steps is already ORDER BY step_order ASC from the backend) -- no
    // position is persisted anywhere, per D.7's "presentation-only"
    // finding; recomputed fresh on every load.
    position: { x: 250, y: i * 140 },
    data: {
      stepNumber: i + 1,
      connectorName: row.toolDeleted ? 'Connector removed' : (tools.find(t => t.id === row.gatewayToolId)?.name ?? 'Unknown connector'),
      action: row.action,
      toolDeleted: row.toolDeleted ?? false,
      summary: summarizeParams(row.params, rows),
      invalid: invalidByStep[i],
    },
    draggable: false,
    connectable: false,
    selectable: false,
  }))

  // One straight, non-interactive edge per consecutive pair -- a visual
  // "this is the execution order" line, matching B-065's own
  // between-card chevron in spirit. Not a data-flow indicator (B-063/
  // 064's extraction references stay inside the summary text above,
  // per the canvas-UI investigation's own recommendation against
  // conflating execution-order and data-reference edges on one canvas).
  const edges: Edge[] = rows.slice(1).map((row, i) => ({
    id: `${rows[i].localId}-${row.localId}`,
    source: rows[i].localId,
    target: row.localId,
    deletable: false,
    selectable: false,
    focusable: false,
  }))

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={`${workflow.name} -- Canvas (preview)`}
        subtitle="Read-only rendering of this workflow's real steps, in order. Editing still happens on the Workflows page."
      />
      <div className="flex-1">
        {rows.length === 0 ? (
          <EmptyState
            title="No steps in this workflow"
            description="Add steps from the Workflows page -- this canvas view is read-only."
          />
        ) : (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            fitView
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={false}
            panOnDrag
            zoomOnScroll
          />
        )}
      </div>
    </div>
  )
}
