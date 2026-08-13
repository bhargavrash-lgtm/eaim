// WorkflowCanvasPage.tsx -- Gateway / Workflows / Canvas
// B-066 Brief 1: read-only rendering. B-067 Brief 2: real interactivity
// -- click a node to configure it (StepConfigPanel, B-065, reused
// verbatim), add/remove nodes, draw connections with real draw-time
// validation. B-068 Brief 3 (this revision): the mandatory second
// validation layer (the investigation's A.2 finding -- draw-time alone
// can't catch a deletion leaving a broken graph) plus real structural
// persistence, closing the epic's own two-layer requirement.
//
// ── The persistence split, a real finding from B-067, now resolved ─────
// A step's STATIC params have their own real, independent endpoint
// (PUT /workflow-steps/{id}/params, B-059) -- saved immediately from
// this page since B-067, touches nothing structural. A step's
// EXTRACTION config (input_mapping) has NO independent endpoint --
// B-063/064's backend only ever writes it as part of a full workflow
// PATCH that also carries `steps` (structure). B-067 left that half
// local-only because it couldn't call that PATCH yet. B-068 closes the
// gap: once the graph is validated and correctly ordered (validateGraph
// below), the EXISTING, unmodified validateAndConvertRows (WorkflowsPage
// .tsx) builds input_mapping from the reordered rows exactly as it
// always has -- extraction config now rides along in the same real
// PATCH as structure, with zero new logic for that half.
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
import { AlertTriangle, Settings2, Trash2, Plus, Save } from 'lucide-react'
import { PageHeader, EmptyState, LoadingSpinner } from '@/components/common'
import { apiFetch } from '@/api/client'
import { useWorkflow, useUpdateWorkflow, useSetWorkflowStepParams } from '@/hooks/useWorkflows'
import { useTools } from '@/hooks/useTools'
import type { ToolWithActions } from '@/hooks/useTools'
import { summarizeParams, revalidateExtractionRefs, validateAndConvertRows, saveStaticParams, StepConfigPanel } from './WorkflowsPage'
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
          <button type="button" onClick={data.onRemove} className="text-gray-400 hover:text-red-600 p-1" title="Remove step (click Save structure to persist)">
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

// validateGraph (B-068) is the mandatory save-time backstop the
// investigation's A.2 finding requires -- draw-time validation
// (isValidConnection, B-067) only gates NEW connection attempts; it
// can't catch a later edge/node deletion leaving a broken graph. This
// re-derives the graph's validity fresh from current `rows`/`edges`,
// trusting nothing from draw time, and (on success) the one valid
// linear order to save in -- exactly one connected chain from a single
// start to a single end, covering every node exactly once. Every
// rejection names the specific offending step(s), per the ease-of-use
// principle (CONTEXT.md): never just "invalid graph."
function validateGraph(rows: StepRow[], edges: Edge[]): { order: string[] | null; error: string | null } {
  if (rows.length === 0) return { order: null, error: 'Add at least one step before saving.' }
  if (rows.length === 1) return { order: [rows[0].localId], error: null }

  const label = (localId: string): string => {
    const idx = rows.findIndex(r => r.localId === localId)
    return idx === -1 ? 'an unknown step' : `Step ${idx + 1}`
  }

  const outgoingCount = new Map<string, number>()
  const incomingCount = new Map<string, number>()
  for (const row of rows) { outgoingCount.set(row.localId, 0); incomingCount.set(row.localId, 0) }
  for (const e of edges) {
    outgoingCount.set(e.source, (outgoingCount.get(e.source) ?? 0) + 1)
    incomingCount.set(e.target, (incomingCount.get(e.target) ?? 0) + 1)
  }

  // Defensive re-check of the same per-node degree limit draw-time
  // already enforces for NEW connections (B-067) -- re-verified here
  // rather than trusted, since this is the actual backstop.
  const overConnected = rows.filter(r => (outgoingCount.get(r.localId) ?? 0) > 1 || (incomingCount.get(r.localId) ?? 0) > 1)
  if (overConnected.length > 0) {
    return { order: null, error: `${overConnected.map(r => label(r.localId)).join(', ')} ${overConnected.length === 1 ? 'has' : 'have'} more than one connection in or out -- each step can only connect to one step before it and one step after it.` }
  }

  const starts = rows.filter(r => (incomingCount.get(r.localId) ?? 0) === 0)
  const ends = rows.filter(r => (outgoingCount.get(r.localId) ?? 0) === 0)
  if (starts.length === 0) {
    return { order: null, error: 'Every step has a connection leading into it, with no starting step -- this usually means a cycle. Remove a connection to break it.' }
  }
  if (starts.length > 1) {
    return { order: null, error: `${starts.map(r => label(r.localId)).join(', ')} have no connection leading into them -- there can only be one starting step. Connect them into the chain or remove the extra ones.` }
  }
  if (ends.length > 1) {
    return { order: null, error: `${ends.map(r => label(r.localId)).join(', ')} have no connection leading out of them -- there can only be one ending step. Connect them into the chain or remove the extra ones.` }
  }

  // Walk from the single start following outgoing edges. If this visits
  // every node exactly once, the graph is exactly one connected linear
  // chain -- the only shape the backend's flat, ordered `steps` array
  // can represent. The walk only ever advances onto a real row id
  // (rowIds.has(current)) -- this function claims to trust nothing from
  // draw time, so an edge whose target isn't a live row (which shouldn't
  // happen given today's UI, but isn't guaranteed by this function's own
  // types) must never be able to smuggle a phantom id into `order`.
  const rowIds = new Set(rows.map(r => r.localId))
  const edgeBySource = new Map<string, Edge>()
  for (const e of edges) edgeBySource.set(e.source, e)
  const order: string[] = []
  const visited = new Set<string>()
  let current: string | undefined = starts[0].localId
  while (current && rowIds.has(current) && !visited.has(current)) {
    visited.add(current)
    order.push(current)
    current = edgeBySource.get(current)?.target
  }

  if (order.length !== rows.length) {
    const unreached = rows.filter(r => !visited.has(r.localId))
    return { order: null, error: `${unreached.map(r => label(r.localId)).join(', ')} ${unreached.length === 1 ? "isn't" : "aren't"} connected to the main chain -- connect ${unreached.length === 1 ? 'it' : 'them'} in or remove ${unreached.length === 1 ? 'it' : 'them'}.` }
  }
  return { order, error: null }
}

export function WorkflowCanvasPage() {
  const { workflowId } = useParams<{ workflowId: string }>()
  const { data: workflow, isLoading, error } = useWorkflow(workflowId ?? null)
  const { data: toolsData } = useTools()
  const tools: ToolWithActions[] = (toolsData as any)?.data ?? []
  const setStepParams = useSetWorkflowStepParams()
  const update = useUpdateWorkflow()

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
  // isSaving/saveError (B-068) are distinct from the transient
  // connection-attempt `toast` above -- a save failure is persistent
  // (survives until the next save attempt or a fix), never auto-clears,
  // since it names a real structural problem the admin must act on.
  const [isSaving, setIsSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

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

  // handleSave (B-068) is the real save path this whole brief exists
  // for: validateGraph first (the mandatory save-time backstop), then
  // reorder rows to the one valid chain order, THEN revalidateExtractionRefs
  // on that reordered array (order can change what counts as "earlier"),
  // then the existing, unmodified validateAndConvertRows builds the exact
  // same { steps, staticParamsByIndex } shape the card editor's own save
  // path already builds -- this calls the SAME useUpdateWorkflow mutation
  // EditWorkflowPanel uses, with the same body shape, nothing new on the
  // backend side. Any rejection at any stage is a persistent, specific
  // saveError -- never a silent no-op or a generic "save failed."
  async function handleSave() {
    if (!rows || !workflow || !workflowId) return
    setSaveError(null)

    const { order, error: graphError } = validateGraph(rows, edges)
    if (graphError || !order) {
      setSaveError(graphError ?? 'This workflow graph is not valid.')
      return
    }

    const orderedRows = order.map(localId => rows.find(r => r.localId === localId)!)
    const revalidated = revalidateExtractionRefs(orderedRows)
    const { steps, staticParamsByIndex, error: convertError } = validateAndConvertRows(revalidated)
    if (convertError) {
      setSaveError(convertError)
      return
    }

    setIsSaving(true)
    try {
      const updated = await update.mutateAsync({ id: workflowId, body: { name: workflow.name, status: workflow.status, steps } })
      const responseSteps = updated.steps ?? []
      const { failed } = await saveStaticParams(responseSteps, staticParamsByIndex, setStepParams)

      // Rebuild local rows/edges/savedStaticKeys from the confirmed
      // persisted response -- same construction as initial load -- so
      // the canvas visually reflects the real saved order/ids
      // immediately (AC4), not just the locally-guessed order.
      const newRows: StepRow[] = responseSteps.map((s, idx) => {
        const staticParams = staticParamsByIndex[idx] ?? {}
        const staticRowsNew: ParamRow[] = Object.entries(staticParams).map(([key, value]) => ({
          key, mode: 'static', value: typeof value === 'string' ? value : JSON.stringify(value),
          fromStepLocalId: '', path: '',
        }))
        const extractRowsNew: ParamRow[] = Object.entries(s.input_mapping ?? {}).map(([key, ref]) => ({
          key, mode: 'extract', value: '', fromStepLocalId: ref.from_step, path: ref.path,
        }))
        return {
          localId: s.id,
          id: s.id,
          gatewayToolId: s.gateway_tool_id,
          action: s.action,
          toolDeleted: !s.gateway_tool_id,
          params: [...staticRowsNew, ...extractRowsNew],
        }
      })
      setRows(newRows)
      setEdges(newRows.slice(1).map((row, i) => ({
        id: `${newRows[i].localId}-${row.localId}`,
        source: newRows[i].localId,
        target: row.localId,
      })))
      const snapshot: Record<string, string> = {}
      newRows.forEach(r => { snapshot[r.localId] = staticParamsKey(r.params) })
      setSavedStaticKeys(snapshot)
      setHasUnsavedChanges(false)

      if (failed > 0) {
        setSaveError(`Workflow structure saved, but ${failed} step${failed === 1 ? "'s" : "s'"} static parameters failed to save -- reopen the affected step and try again.`)
      } else {
        setToast('Workflow saved')
        setTimeout(() => setToast(null), 2500)
      }
    } catch {
      setSaveError('Failed to save the workflow -- check your connection and try again.')
    } finally {
      setIsSaving(false)
    }
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
        title={`${workflow.name} -- Canvas`}
        subtitle="Click a step to configure it. Draw or delete connections to change step order, then Save structure to persist the graph for real."
        actions={
          <div className="flex items-center gap-2">
            <button onClick={addNode}
              className="flex items-center gap-1.5 bg-white border border-gray-300 text-gray-700 rounded px-3 py-1.5 text-sm font-medium hover:bg-gray-50">
              <Plus className="h-4 w-4" />
              Add step
            </button>
            <button onClick={handleSave} disabled={isSaving}
              className={`flex items-center gap-1.5 rounded px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60 ${
                hasUnsavedChanges ? 'bg-indigo-600 hover:bg-indigo-700' : 'bg-gray-400 hover:bg-gray-500'
              }`}>
              <Save className="h-4 w-4" />
              {isSaving ? 'Saving...' : 'Save structure'}
            </button>
          </div>
        }
      />

      {hasUnsavedChanges && (
        <div className="mx-4 mt-3 px-4 py-2 rounded text-sm border bg-amber-50 border-amber-200 text-amber-800">
          You have unsaved structural or extraction changes on this canvas. Click "Save structure" to persist them.
        </div>
      )}
      {saveError && (
        <div className="mx-4 mt-3 px-4 py-2 rounded text-sm border bg-red-50 border-red-200 text-red-700">
          {saveError}
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
            description="Add a step above, then click Save structure to persist it."
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
