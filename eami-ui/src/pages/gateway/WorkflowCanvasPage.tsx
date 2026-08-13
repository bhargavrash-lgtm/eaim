// WorkflowCanvasPage.tsx -- Gateway / Workflows / Canvas
// B-066 Brief 1: read-only rendering. B-067 Brief 2 (this revision):
// real interactivity -- click a node to configure it (StepConfigPanel,
// B-065, reused verbatim), add/remove nodes, draw connections with real
// draw-time validation. B-065's card UI remains the ONLY way to persist
// a workflow's structure or its extraction (input_mapping) config;
// nothing in this file calls a backend mutation for structure -- see
// the persistence-split note below for exactly what DOES persist here
// and why.
//
// ── The persistence split, a real finding not an oversight ─────────────
// A step's STATIC params have their own real, independent endpoint
// (PUT /workflow-steps/{id}/params, B-059) -- safe to save immediately
// from this page, touches nothing structural. A step's EXTRACTION
// config (input_mapping) has NO independent endpoint at all -- B-063/
// 064's backend only ever writes it as part of a full workflow PATCH
// that also carries `steps` (structure), which this brief is explicitly
// forbidden from calling. So: static param edits persist for real,
// immediately, when the config panel closes; everything else (add/
// remove a node, draw/delete a connection, an extraction-mode param
// edit) stays local-only, surfaced through one unambiguous "unsaved
// changes" banner -- never a silent, unpersisted "looks saved" state.
import { useState, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import {
  ReactFlow,
  Handle,
  Position,
  getOutgoers,
  type Node,
  type Edge,
  type NodeProps,
  type Connection,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { AlertTriangle, Settings2, Trash2, Plus } from 'lucide-react'
import { PageHeader, EmptyState, LoadingSpinner } from '@/components/common'
import { apiFetch } from '@/api/client'
import { useWorkflow, useSetWorkflowStepParams } from '@/hooks/useWorkflows'
import { useTools } from '@/hooks/useTools'
import type { ToolWithActions } from '@/hooks/useTools'
import { summarizeParams, revalidateExtractionRefs, StepConfigPanel } from './WorkflowsPage'
import type { ParamRow, StepRow } from './WorkflowsPage'

type WorkflowNodeData = {
  stepNumber: number
  connectorName: string
  action: string
  toolDeleted: boolean
  summary: string
  invalid: boolean
  onConfigure: () => void
  onRemove: () => void
}

// WorkflowStepNode mirrors B-065's card styling and icon affordances
// (Settings2/Trash2) so the canvas and card views read as one product.
// Handles are connectable this brief (B-066 had isConnectable={false});
// validity of any given connection attempt is enforced centrally by
// <ReactFlow>'s isValidConnection below, not per-handle.
function WorkflowStepNode({ data }: NodeProps & { data: WorkflowNodeData }) {
  const flagged = data.toolDeleted || data.invalid
  return (
    <div className={`rounded-lg border bg-white shadow-sm p-3 w-64 ${flagged ? 'border-red-200' : 'border-gray-200'}`}>
      <Handle type="target" position={Position.Top} />
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-start gap-2 min-w-0">
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
        <div className="flex items-center gap-0.5 shrink-0">
          <button type="button" onClick={data.onConfigure} className="text-gray-400 hover:text-indigo-600 p-1" title="Configure step">
            <Settings2 className="h-4 w-4" />
          </button>
          <button type="button" onClick={data.onRemove} className="text-gray-400 hover:text-red-600 p-1" title="Remove step (visual only, not saved)">
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  )
}

const nodeTypes = { workflowStep: WorkflowStepNode }

// staticParamsKey builds a comparable snapshot of just a step's static
// (mode==='static') params, used to detect whether the STATIC half of a
// step's params actually changed when its config panel closes -- so
// closing without touching statics never fires a redundant PUT.
function staticParamsKey(params: ParamRow[]): string {
  const obj: Record<string, string> = {}
  for (const p of params) {
    const key = p.key.trim()
    if (p.mode === 'static' && key) obj[key] = p.value
  }
  return JSON.stringify(obj)
}

export function WorkflowCanvasPage() {
  const { workflowId } = useParams<{ workflowId: string }>()
  const { data: workflow, isLoading, error } = useWorkflow(workflowId ?? null)
  const { data: toolsData } = useTools()
  const tools: ToolWithActions[] = (toolsData as any)?.data ?? []
  const setStepParams = useSetWorkflowStepParams()

  const stepIds = (workflow?.steps ?? []).map(s => s.id)
  const paramsQueries = useQueries({
    queries: stepIds.map(id => ({
      queryKey: ['workflow-step-params', id],
      queryFn: () => apiFetch<Record<string, unknown>>(`/v1/gateway/workflow-steps/${id}/params`),
    })),
  })
  const paramsReady = stepIds.length === 0 || paramsQueries.every(q => q.isSuccess || q.isError)

  const [rows, setRows] = useState<StepRow[] | null>(null)
  const [edges, setEdges] = useState<Edge[]>([])
  const [configuringLocalId, setConfiguringLocalId] = useState<string | null>(null)
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false)
  const [toast, setToast] = useState<string | null>(null)
  // The static-params snapshot as of the last successful save (or
  // initial load) per step -- compared on config-panel close so only a
  // genuine static-param change triggers a real PUT (AC1), never a
  // no-op write on every open/close.
  const [savedStaticKeys, setSavedStaticKeys] = useState<Record<string, string>>({})

  // Initialize local editable state once, guarded exactly like
  // EditWorkflowPanel's own row-init (never re-runs on a background
  // refetch of the same data).
  if (workflow && paramsReady && rows === null) {
    const initRows: StepRow[] = (workflow.steps ?? []).map((s, idx) => {
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
    setRows(initRows)
    setEdges(initRows.slice(1).map((row, i) => ({
      id: `${initRows[i].localId}-${row.localId}`,
      source: initRows[i].localId,
      target: row.localId,
    })))
    const snapshot: Record<string, string> = {}
    initRows.forEach(r => { snapshot[r.localId] = staticParamsKey(r.params) })
    setSavedStaticKeys(snapshot)
  }

  // commit mirrors StepsEditor's own commit() exactly -- always
  // re-validates extraction references before applying a change, reused
  // (not reimplemented) via the export B-067 added to WorkflowsPage.tsx.
  const commit = useCallback((newRows: StepRow[]) => {
    setRows(revalidateExtractionRefs(newRows))
  }, [])

  function updateParam(stepIdx: number, paramIdx: number, patch: Partial<ParamRow>) {
    if (!rows) return
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: r.params.map((p, pi) => (pi !== paramIdx ? p : { ...p, ...patch })) })))
  }
  function addParam(stepIdx: number) {
    if (!rows) return
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: [...r.params, { key: '', mode: 'static' as const, value: '', fromStepLocalId: '', path: '' }] })))
  }
  function removeParam(stepIdx: number, paramIdx: number) {
    if (!rows) return
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: r.params.filter((_, pi) => pi !== paramIdx) })))
  }
  function updateRow(i: number, patch: Partial<StepRow>) {
    if (!rows) return
    commit(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
    setHasUnsavedChanges(true) // a connector/action change is structural-adjacent -- not independently saveable here
  }

  // closeConfigPanel is where the actual persistence split happens
  // (point 2 of this brief's plan): a real, immediate PUT for the
  // static half if it changed; everything else just marks unsaved.
  async function closeConfigPanel(stepLocalId: string) {
    setConfiguringLocalId(null)
    if (!rows) return
    const row = rows.find(r => r.localId === stepLocalId)
    if (!row || !row.id) return
    const hasExtractEdits = row.params.some(p => p.mode === 'extract')
    const currentStaticKey = staticParamsKey(row.params)
    const staticChanged = currentStaticKey !== savedStaticKeys[stepLocalId]
    if (staticChanged) {
      const staticParams: Record<string, string> = {}
      row.params.forEach(p => { const k = p.key.trim(); if (p.mode === 'static' && k) staticParams[k] = p.value })
      try {
        await setStepParams.mutateAsync({ stepId: row.id, params: staticParams })
        setSavedStaticKeys(prev => ({ ...prev, [stepLocalId]: currentStaticKey }))
        setToast(hasExtractEdits ? 'Static parameters saved. Extraction changes here are visual only -- see the note above.' : 'Static parameters saved')
        setTimeout(() => setToast(null), 2500)
      } catch {
        setToast('Failed to save static parameters')
      }
    }
    if (hasExtractEdits) setHasUnsavedChanges(true)
  }

  function addNode() {
    if (!rows) return
    const newRow: StepRow = { localId: crypto.randomUUID(), gatewayToolId: '', action: '', params: [] }
    const newRows = [...rows, newRow]
    commit(newRows)
    if (newRows.length > 1) {
      const prev = newRows[newRows.length - 2]
      setEdges(e => [...e, { id: `${prev.localId}-${newRow.localId}`, source: prev.localId, target: newRow.localId }])
    }
    setHasUnsavedChanges(true)
  }

  function removeNode(localId: string) {
    if (!rows) return
    commit(rows.filter(r => r.localId !== localId))
    setEdges(e => e.filter(edge => edge.source !== localId && edge.target !== localId))
    setHasUnsavedChanges(true)
  }

  // isValidConnection (AC2/AC3): React Flow's own documented reference
  // shape (one-in/one-out via existing-edge counts, cycle rejection via
  // getOutgoers) -- not a placeholder. A rejection surfaces a specific
  // toast instead of a silent no-op.
  const isValidConnection = useCallback((connection: Edge | Connection): boolean => {
    if (!rows) return false
    if (connection.source === connection.target) {
      setToast('A step cannot connect to itself')
      setTimeout(() => setToast(null), 2500)
      return false
    }
    if (edges.some(e => e.source === connection.source)) {
      setToast('This step already has an outgoing connection -- remove it first')
      setTimeout(() => setToast(null), 2500)
      return false
    }
    if (edges.some(e => e.target === connection.target)) {
      setToast('That step already has an incoming connection -- remove it first')
      setTimeout(() => setToast(null), 2500)
      return false
    }
    // Cycle check: would following outgoers from the proposed target
    // ever lead back to the proposed source? Mirrors React Flow's own
    // documented "Preventing Cycles" example exactly.
    const nodesForCheck: Node[] = rows.map(r => ({ id: r.localId, position: { x: 0, y: 0 }, data: {} }))
    const targetNode = nodesForCheck.find(n => n.id === connection.target)
    if (targetNode) {
      const visited = new Set<string>()
      const hasCycle = (node: Node): boolean => {
        if (visited.has(node.id)) return false
        visited.add(node.id)
        for (const outgoer of getOutgoers(node, nodesForCheck, edges)) {
          if (outgoer.id === connection.source) return true
          if (hasCycle(outgoer)) return true
        }
        return false
      }
      if (hasCycle(targetNode)) {
        setToast('That connection would create a cycle')
        setTimeout(() => setToast(null), 2500)
        return false
      }
    }
    return true
  }, [rows, edges])

  function onConnect(connection: Connection) {
    if (!connection.source || !connection.target) return
    setEdges(e => [...e, { id: `${connection.source}-${connection.target}`, source: connection.source!, target: connection.target! }])
    setHasUnsavedChanges(true)
  }

  if (isLoading) return <div className="p-6"><LoadingSpinner /></div>
  if (error || !workflow) return <div className="p-6 text-sm text-red-500">Failed to load workflow.</div>
  if (rows === null) return <div className="p-6"><LoadingSpinner /></div>

  const invalidByStep = rows.map(row => row.params.some(p => p.mode === 'extract' && p.invalid))

  const nodes: Node<WorkflowNodeData>[] = rows.map((row, i) => ({
    id: row.localId,
    type: 'workflowStep',
    // Still a simple deterministic vertical stack from array order --
    // no free dragging this brief (out of scope), no position persisted
    // anywhere (D.7's finding, unchanged from B-066).
    position: { x: 250, y: i * 140 },
    data: {
      stepNumber: i + 1,
      connectorName: row.toolDeleted ? 'Connector removed' : (tools.find(t => t.id === row.gatewayToolId)?.name ?? (row.gatewayToolId ? 'Unknown connector' : 'Select a connector...')),
      action: row.action,
      toolDeleted: row.toolDeleted ?? false,
      summary: summarizeParams(row.params, rows),
      invalid: invalidByStep[i],
      onConfigure: () => setConfiguringLocalId(row.localId),
      onRemove: () => removeNode(row.localId),
    },
    draggable: false,
  }))

  const configuringIndex = configuringLocalId ? rows.findIndex(r => r.localId === configuringLocalId) : -1

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={`${workflow.name} -- Canvas (preview)`}
        subtitle="Click a step to configure it. Static parameter edits save for real; everything else here is visual only -- see the banner below."
        actions={
          <button onClick={addNode}
            className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700">
            <Plus className="h-4 w-4" />
            Add step (visual only)
          </button>
        }
      />

      {hasUnsavedChanges && (
        <div className="mx-4 mt-3 px-4 py-2 rounded text-sm border bg-amber-50 border-amber-200 text-amber-800">
          You have unsaved structural or extraction changes on this canvas. These are visual only in this preview and are not saved -- use the Workflows page to make and save real structural changes.
        </div>
      )}
      {toast && (
        <div className="mx-4 mt-3 px-4 py-2 rounded text-sm border bg-red-50 border-red-200 text-red-700">
          {toast}
        </div>
      )}

      <div className="flex-1">
        {rows.length === 0 ? (
          <EmptyState
            title="No steps in this workflow"
            description="Add a step above -- remember, structural changes here are visual only and are not saved."
          />
        ) : (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            fitView
            nodesDraggable={false}
            nodesConnectable
            elementsSelectable
            isValidConnection={isValidConnection}
            onConnect={onConnect}
            panOnDrag
            zoomOnScroll
          />
        )}
      </div>

      {configuringIndex >= 0 && rows[configuringIndex] && (
        <StepConfigPanel
          row={rows[configuringIndex]} index={configuringIndex} rows={rows} tools={tools}
          onClose={() => closeConfigPanel(rows[configuringIndex].localId)}
          updateRow={updateRow} updateParam={updateParam} addParam={addParam} removeParam={removeParam}
        />
      )}
    </div>
  )
}
