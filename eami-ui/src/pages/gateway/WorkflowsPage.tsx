// WorkflowsPage.tsx -- Gateway / Workflows
// Multi-Hop Workflows (Thread B), Brief 1 (B-058): schema + CRUD
// foundation only. This page defines an ordered, strictly LINEAR list of
// steps -- no branching UI, matching the Thread B investigation's own
// finding that branching is unbuildable until connector output
// normalization exists. Nothing here executes a workflow.
//
// Structure deliberately mirrors ToolsPage.tsx: table + slide-out
// add/edit drawers + ConfirmDialog for delete, with the step-row editor
// reusing ToolsPage's ActionPathsEditor shape (add/remove rows), extended
// with up/down reorder controls since a workflow's step order is load-
// bearing (ActionPathsEditor's rows have no inherent order).
import { useState } from 'react'
import { useQueries } from '@tanstack/react-query'
import { Plus, Trash2, Pencil, ArrowUp, ArrowDown, AlertTriangle } from 'lucide-react'
import {
  PageHeader,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
} from '@/components/common'
import { apiFetch } from '@/api/client'
import {
  useWorkflows,
  useWorkflow,
  useCreateWorkflow,
  useUpdateWorkflow,
  useDeleteWorkflow,
  useSetWorkflowStepParams,
} from '@/hooks/useWorkflows'
import type { Workflow, WorkflowStatus, WorkflowStep, ExtractionRef } from '@/hooks/useWorkflows'
import { useTools } from '@/hooks/useTools'
import type { ToolWithActions } from '@/hooks/useTools'

const STATUS_OPTIONS: { value: WorkflowStatus; label: string }[] = [
  { value: 'draft', label: 'Draft' },
  { value: 'active', label: 'Active' },
  { value: 'disabled', label: 'Disabled' },
]

const STATUS_BADGE: Record<WorkflowStatus, string> = {
  draft: 'bg-gray-100 text-gray-700',
  active: 'bg-green-100 text-green-800',
  disabled: 'bg-amber-100 text-amber-800',
}

function StatusBadge({ status }: { status: string }) {
  const cls = STATUS_BADGE[status as WorkflowStatus] ?? 'bg-gray-100 text-gray-600'
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize ${cls}`}>
      {status}
    </span>
  )
}

// ── Step editor ──────────────────────────────────────────────────────────────

// ParamRow (B-064) is one of a step's parameters, either a fixed value
// (static, B-059) or extracted from an earlier step's real recorded
// result (extract, B-063). fromStepLocalId references another StepRow
// by its LOCAL identity (localId below), never by array position or by
// the backend's real id directly -- a brand-new, not-yet-saved source
// step has no real id yet, so local identity is the only thing stable
// enough to reference *during* an edit session; it's resolved to a real
// backend id only at submit time (validateAndConvertRows), exactly
// mirroring why B-063's backend itself references steps by id, not
// position. invalid is set by revalidateExtractionRefs after any
// reorder/removal that leaves this reference pointing at a step that's
// no longer earlier (or no longer exists, or still has no real id) --
// surfaced visibly, never silently cleared (AC3).
type ParamRow = {
  key: string
  mode: 'static' | 'extract'
  value: string
  fromStepLocalId: string
  path: string
  invalid?: boolean
}

// StepRow.localId is a client-generated stable identity (crypto.
// randomUUID(), or the real backend id when loaded from a saved step --
// see EditWorkflowPanel) independent of array position, so extraction
// references survive a reorder within the same edit session. id is the
// real workflow_steps.id once this step has been persisted at least
// once -- undefined for a step just added in the current session, which
// is exactly what makes it ineligible as an extraction SOURCE (no real
// id for a later step to reference yet) while still being free to
// itself extract FROM an already-real-id'd earlier step.
type StepRow = {
  localId: string
  id?: string
  gatewayToolId: string
  action: string
  toolDeleted?: boolean
  params: ParamRow[]
}

function newStepRow(): StepRow {
  return { localId: crypto.randomUUID(), gatewayToolId: '', action: '', params: [] }
}

function newParamRow(): ParamRow {
  return { key: '', mode: 'static', value: '', fromStepLocalId: '', path: '' }
}

// revalidateExtractionRefs re-checks every extract-mode param against
// the CURRENT row order/identity and flags (never silently clears) any
// reference that's no longer valid -- source step removed, or no longer
// earlier than the referencing step (B-064 AC3). Run after every
// row-order/row-set mutation (see StepsEditor's commit()).
function revalidateExtractionRefs(rows: StepRow[]): StepRow[] {
  return rows.map((row, i) => ({
    ...row,
    params: row.params.map(p => {
      if (p.mode !== 'extract') return p
      if (p.fromStepLocalId === '') return { ...p, invalid: false } // not yet configured -- caught at submit time as incomplete, not flagged as broken
      const sourceIdx = rows.findIndex(r => r.localId === p.fromStepLocalId)
      const valid = sourceIdx !== -1 && sourceIdx < i && !!rows[sourceIdx].id
      return { ...p, invalid: !valid }
    }),
  }))
}

// validateAndConvertRows requires every step row to be complete (a
// connector selected + an action) and every configured param to be
// complete and valid, returning an error naming the first problem
// instead of silently dropping or saving broken state (B-058's own
// established precedent, extended here to param rows -- AC3/CONTRACTS).
// A step flagged toolDeleted (its connector was removed) is deliberately
// preserved with an empty gatewayToolId so an admin can see and fix it.
// Returns each step's static params separately (keyed by array index,
// not yet by real step id -- see AddWorkflowPanel/EditWorkflowPanel's
// submit handlers for why: a new step's real id is only known once the
// main Create/Update response comes back).
function validateAndConvertRows(rows: StepRow[]): {
  steps: { id?: string; gateway_tool_id: string; action: string; input_mapping?: Record<string, ExtractionRef> }[]
  staticParamsByIndex: (Record<string, string> | null)[]
  error: string | null
} {
  if (rows.length === 0) return { steps: [], staticParamsByIndex: [], error: 'At least one step is required' }
  const steps: { id?: string; gateway_tool_id: string; action: string; input_mapping?: Record<string, ExtractionRef> }[] = []
  const staticParamsByIndex: (Record<string, string> | null)[] = []
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i]
    const gatewayToolId = row.gatewayToolId.trim()
    const action = row.action.trim()
    if (!gatewayToolId || !action) {
      return { steps: [], staticParamsByIndex: [], error: `Step ${i + 1} is incomplete -- select a connector and an action, or remove it` }
    }
    const inputMapping: Record<string, ExtractionRef> = {}
    const staticParams: Record<string, string> = {}
    for (const p of row.params) {
      const key = p.key.trim()
      if (!key) continue // an in-progress blank param row is skipped, not an error -- matches ActionPathsEditor's own rowsToActionPaths precedent
      if (p.mode === 'extract') {
        if (p.invalid || !p.fromStepLocalId || !p.path.trim()) {
          return { steps: [], staticParamsByIndex: [], error: `Step ${i + 1}, parameter "${key}": pick a valid source step and a field path, or remove this parameter` }
        }
        const source = rows.find(r => r.localId === p.fromStepLocalId)
        if (!source?.id) {
          return { steps: [], staticParamsByIndex: [], error: `Step ${i + 1}, parameter "${key}": source step is not yet saved -- save the workflow once with that step present first` }
        }
        inputMapping[key] = { from_step: source.id, path: p.path.trim() }
      } else {
        staticParams[key] = p.value
      }
    }
    steps.push({
      ...(row.id ? { id: row.id } : {}),
      gateway_tool_id: gatewayToolId,
      action,
      ...(Object.keys(inputMapping).length > 0 ? { input_mapping: inputMapping } : {}),
    })
    staticParamsByIndex.push(Object.keys(staticParams).length > 0 ? staticParams : null)
  }
  return { steps, staticParamsByIndex, error: null }
}

// saveStaticParams (B-064) fires the real B-059 PUT endpoint for every
// step that has static params configured, using the REAL step ids the
// main Create/Update response just returned (in the same order steps
// were submitted, since step_order is assigned from array position) --
// this is what lets a brand-new step's OWN static params be set in the
// very same submit that creates it, no "save once, reopen" friction.
// Runs all step param saves in parallel; a failure surfaces a distinct
// count rather than being silently swallowed (the workflow itself did
// save successfully either way).
async function saveStaticParams(
  responseSteps: WorkflowStep[],
  staticParamsByIndex: (Record<string, string> | null)[],
  setStepParams: ReturnType<typeof useSetWorkflowStepParams>,
): Promise<{ failed: number }> {
  const jobs = staticParamsByIndex.map((params, i) => {
    const stepId = responseSteps[i]?.id
    if (!params || !stepId) return null
    return setStepParams.mutateAsync({ stepId, params }).then(() => true, () => false)
  })
  const results = await Promise.all(jobs)
  return { failed: results.filter(r => r === false).length }
}

function StepsEditor({ rows, onChange }: { rows: StepRow[]; onChange: (rows: StepRow[]) => void }) {
  const { data: toolsData } = useTools()
  // Typed properly (mirrors ToolsPage.tsx's own identical pattern) so
  // action_paths -- B-046, already fetched here for free -- is usable
  // below without a second `any` cast.
  const tools: ToolWithActions[] = (toolsData as any)?.data ?? []
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  // commit is the single path every row/param mutation below goes
  // through -- always re-runs revalidateExtractionRefs first (B-064
  // AC3), so there's no call site that could forget to and leave a
  // now-invalid extraction reference looking valid.
  function commit(newRows: StepRow[]) {
    onChange(revalidateExtractionRefs(newRows))
  }

  function updateRow(i: number, patch: Partial<StepRow>) {
    commit(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function removeRow(i: number) {
    commit(rows.filter((_, idx) => idx !== i))
  }
  function addRow() {
    commit([...rows, newStepRow()])
  }
  function moveRow(i: number, dir: -1 | 1) {
    const j = i + dir
    if (j < 0 || j >= rows.length) return
    const next = [...rows]
    ;[next[i], next[j]] = [next[j], next[i]]
    commit(next)
  }
  function updateParam(stepIdx: number, paramIdx: number, patch: Partial<ParamRow>) {
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: r.params.map((p, pi) => (pi !== paramIdx ? p : { ...p, ...patch })) })))
  }
  function addParam(stepIdx: number) {
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: [...r.params, newParamRow()] })))
  }
  function removeParam(stepIdx: number, paramIdx: number) {
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: r.params.filter((_, pi) => pi !== paramIdx) })))
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <label className="block text-sm font-medium text-gray-700">Steps (in order)</label>
        <button type="button" onClick={addRow} className="text-xs text-indigo-600 hover:text-indigo-800 font-medium">
          + Add step
        </button>
      </div>
      <p className="text-xs text-gray-400 mb-2">
        Each step calls one connector. This is a linear chain, definition only -- steps don't execute yet.
      </p>
      {rows.length === 0 ? (
        <p className="text-xs text-gray-400 italic">No steps yet -- add at least one.</p>
      ) : (
        <div className="space-y-2">
          {rows.map((row, i) => (
            <div key={row.localId}>
            <div className="flex items-center gap-2">
              <span className="text-xs text-gray-400 w-4 text-right shrink-0">{i + 1}</span>
              <div className="flex flex-col shrink-0">
                <button type="button" onClick={() => moveRow(i, -1)} disabled={i === 0}
                  className="text-gray-400 hover:text-indigo-600 disabled:opacity-30" title="Move up">
                  <ArrowUp className="h-3 w-3" />
                </button>
                <button type="button" onClick={() => moveRow(i, 1)} disabled={i === rows.length - 1}
                  className="text-gray-400 hover:text-indigo-600 disabled:opacity-30" title="Move down">
                  <ArrowDown className="h-3 w-3" />
                </button>
              </div>
              {row.toolDeleted && (
                <span title="This step's connector was deleted -- pick a new one" className="shrink-0">
                  <AlertTriangle className="h-4 w-4 text-red-500" />
                </span>
              )}
              <select value={row.gatewayToolId}
                // action is always reset on a connector change (B-060) --
                // simplest rule that can never leave a stale action value
                // behind, whether the old or new connector uses the
                // dropdown or free-text path.
                onChange={e => updateRow(i, { gatewayToolId: e.target.value, action: '', toolDeleted: false })}
                className={`flex-1 border rounded px-2 py-1.5 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500 ${row.toolDeleted ? 'border-red-300 text-red-600' : ''}`}>
                <option value="">{row.toolDeleted ? 'connector removed -- select a new one' : 'Select a connector...'}</option>
                {tools.map(t => (
                  <option key={t.id} value={t.id}>{t.name}</option>
                ))}
              </select>
              {(() => {
                // Real action picker (B-060): a rest_api connector with
                // action_paths (B-046) defined gets a dropdown of its real,
                // known actions instead of free text -- action_paths is
                // already fetched by useTools() above, no new endpoint
                // needed. A connector with none (ai_provider/mcp/database,
                // or a rest_api tool that hasn't defined per-action
                // mappings yet) falls back to free text unchanged, exactly
                // matching action_paths' own existing "unmapped falls back
                // to base_url" convention on the dispatch side.
                const selectedTool = tools.find(t => t.id === row.gatewayToolId)
                const actionKeys = selectedTool?.action_paths ? Object.keys(selectedTool.action_paths) : []
                if (actionKeys.length === 0) {
                  return (
                    <input value={row.action} onChange={e => updateRow(i, { action: e.target.value })}
                      placeholder="action"
                      className="w-1/3 border rounded px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                  )
                }
                // A saved-but-now-unrecognized action (its mapping was
                // removed from the connector after this workflow was
                // saved) is appended as an extra option rather than
                // dropped -- loading a workflow must never silently mutate
                // its saved data, only an actual user edit should.
                const options = row.action && !actionKeys.includes(row.action) ? [...actionKeys, row.action] : actionKeys
                return (
                  <select value={row.action} onChange={e => updateRow(i, { action: e.target.value })}
                    className="w-1/3 border rounded px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500">
                    <option value="">Select an action...</option>
                    {options.map(a => <option key={a} value={a}>{a}</option>)}
                  </select>
                )
              })()}
              <button type="button" onClick={() => removeRow(i)} className="text-gray-400 hover:text-red-600 shrink-0" title="Remove step">
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </div>

            {/* Parameters (B-064): collapsible, per step -- static values
                (B-059) and extractions from an earlier step's real result
                (B-063), side by side per param key. No live preview of a
                prior step's actual result is offered (no backend path
                exists for it today, see BACKLOG.md's B-064 entry) --
                honest text-input path expression instead. */}
            <div className="ml-6 mt-1">
              <button type="button" onClick={() => setExpanded(e => ({ ...e, [row.localId]: !e[row.localId] }))}
                className="text-xs text-gray-500 hover:text-indigo-600">
                {expanded[row.localId] ? '▾' : '▸'} Parameters{row.params.length > 0 ? ` (${row.params.length})` : ''}
              </button>
              {expanded[row.localId] && (
                <div className="mt-1 pl-3 border-l-2 border-gray-100 space-y-1.5">
                  {row.params.length === 0 && (
                    <p className="text-xs text-gray-400 italic">No parameters -- add one below.</p>
                  )}
                  {row.params.map((p, pi) => (
                    <div key={pi} className="flex items-center gap-1.5">
                      <input value={p.key} onChange={e => updateParam(i, pi, { key: e.target.value })}
                        placeholder="param name"
                        className="w-28 border rounded px-1.5 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                      <select value={p.mode}
                        onChange={e => updateParam(i, pi, { mode: e.target.value as ParamRow['mode'] })}
                        className="border rounded px-1 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500">
                        <option value="static">Static</option>
                        <option value="extract">Extract</option>
                      </select>
                      {p.mode === 'static' ? (
                        <input value={p.value} onChange={e => updateParam(i, pi, { value: e.target.value })}
                          placeholder="value"
                          className="flex-1 border rounded px-1.5 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                      ) : (
                        <>
                          <select value={p.fromStepLocalId}
                            onChange={e => updateParam(i, pi, { fromStepLocalId: e.target.value })}
                            className={`flex-1 border rounded px-1.5 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500 ${p.invalid ? 'border-red-300 text-red-600' : ''}`}>
                            <option value="">{p.invalid ? 'source no longer valid -- pick again' : 'Select a source step...'}</option>
                            {rows.slice(0, i).map((r, ri) => r.id && (
                              <option key={r.localId} value={r.localId}>
                                Step {ri + 1}: {tools.find(t => t.id === r.gatewayToolId)?.name ?? '(connector)'} / {r.action || '(action)'}
                              </option>
                            ))}
                          </select>
                          <input value={p.path} onChange={e => updateParam(i, pi, { path: e.target.value })}
                            placeholder="e.g. contact.id"
                            className="w-32 border rounded px-1.5 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                        </>
                      )}
                      <button type="button" onClick={() => removeParam(i, pi)} className="text-gray-400 hover:text-red-600 shrink-0" title="Remove parameter">
                        <Trash2 className="h-3 w-3" />
                      </button>
                    </div>
                  ))}
                  {row.params.some(p => p.mode === 'extract' && p.invalid) && (
                    <p className="text-xs text-red-600">
                      One or more extraction sources are no longer valid (moved later, removed, or not yet saved) -- pick a new source or remove the parameter.
                    </p>
                  )}
                  <button type="button" onClick={() => addParam(i)} className="text-xs text-indigo-600 hover:text-indigo-800">
                    + Add parameter
                  </button>
                </div>
              )}
            </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ── Add Workflow panel ───────────────────────────────────────────────────────

function AddWorkflowPanel({ onClose }: { onClose: () => void }) {
  const create = useCreateWorkflow()
  const setStepParams = useSetWorkflowStepParams()
  const [toast, setToast] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [status, setStatus] = useState<WorkflowStatus>('draft')
  const [rows, setRows] = useState<StepRow[]>([])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const { steps, staticParamsByIndex, error } = validateAndConvertRows(rows)
    if (error) {
      setToast(error)
      return
    }
    try {
      const created = await create.mutateAsync({ name, status, steps })
      // Real step ids only exist now, in this response -- fires the
      // static-params PUTs for any step that has them, including a
      // brand-new step created in this exact submit (B-064).
      const { failed } = await saveStaticParams(created.steps ?? [], staticParamsByIndex, setStepParams)
      setToast(failed > 0 ? 'Workflow created, but some parameters failed to save' : 'Workflow created')
      setTimeout(() => { setToast(null); onClose() }, 1000)
    } catch {
      setToast('Failed to create workflow')
    }
  }

  return (
    <div className="fixed inset-y-0 right-0 w-[480px] bg-white shadow-xl flex flex-col z-50 border-l border-gray-200">
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <h2 className="font-semibold text-gray-900">Add Workflow</h2>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">x</button>
      </div>

      {toast && (
        <div className={`mx-6 mt-4 px-4 py-2 rounded text-sm border ${
          toast.includes('Failed') || toast.includes('required') || toast.includes('incomplete')
            ? 'bg-red-50 border-red-200 text-red-700'
            : toast.includes('parameters failed')
              ? 'bg-amber-50 border-amber-200 text-amber-800'
              : 'bg-green-50 border-green-200 text-green-700'
        }`}>
          {toast}
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-6 py-4">
        <form id="workflow-form" onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Workflow name</label>
            <input required value={name} onChange={e => setName(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              placeholder="Lead enrichment chain..." />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Status</label>
            <select value={status} onChange={e => setStatus(e.target.value as WorkflowStatus)}
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
              {STATUS_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          </div>
          <StepsEditor rows={rows} onChange={setRows} />
        </form>
      </div>

      <div className="px-6 py-4 border-t flex gap-3">
        <button type="submit" form="workflow-form" disabled={create.isPending}
          className="flex-1 bg-indigo-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50">
          {create.isPending ? 'Creating...' : 'Create workflow'}
        </button>
        <button onClick={onClose} className="px-4 py-2 text-sm text-gray-600 hover:text-gray-900">Cancel</button>
      </div>
    </div>
  )
}

// ── Edit Workflow panel ──────────────────────────────────────────────────────

function EditWorkflowPanel({ workflowId, onClose }: { workflowId: string; onClose: () => void }) {
  const { data: workflow, isLoading } = useWorkflow(workflowId)
  const update = useUpdateWorkflow()
  const setStepParams = useSetWorkflowStepParams()
  const [toast, setToast] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [status, setStatus] = useState<WorkflowStatus>('draft')
  const [rows, setRows] = useState<StepRow[] | null>(null)

  // B-064: fetch every already-persisted step's real static params
  // (B-059's own endpoint, never previously consumed by any UI) so the
  // editor can seed static param rows correctly. useQueries (not one
  // query per row via a subcomponent) keeps ownership of the fetched
  // data in this component, alongside the workflow fetch it's combined
  // with below -- every step from a real GetWorkflow response always has
  // a real id (B-063), so this list is exactly workflow.steps itself.
  const stepIds = (workflow?.steps ?? []).map(s => s.id)
  const paramsQueries = useQueries({
    queries: stepIds.map(id => ({
      queryKey: ['workflow-step-params', id],
      queryFn: () => apiFetch<Record<string, unknown>>(`/v1/gateway/workflow-steps/${id}/params`),
    })),
  })
  const paramsReady = stepIds.length === 0 || paramsQueries.every(q => q.isSuccess || q.isError)

  // Initialize local form state once the workflow AND every step's
  // static params have loaded. A step whose gateway_tool_id came back ""
  // (deleted connector, migration 000006's ON DELETE SET NULL) is
  // flagged toolDeleted so the dropdown renders visibly broken rather
  // than silently defaulting to blank. localId is set to the step's own
  // real id for every already-persisted step -- correct and necessary,
  // since a sibling step's input_mapping.from_step already refers to
  // this exact id, and fromStepLocalId must resolve against it.
  if (workflow && paramsReady && rows === null) {
    setName(workflow.name)
    setStatus(workflow.status)
    setRows((workflow.steps ?? []).map((s, idx) => {
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
    }))
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const { steps, staticParamsByIndex, error } = validateAndConvertRows(rows ?? [])
    if (error) {
      setToast(error)
      return
    }
    try {
      const updated = await update.mutateAsync({ id: workflowId, body: { name, status, steps } })
      const { failed } = await saveStaticParams(updated.steps ?? [], staticParamsByIndex, setStepParams)
      setToast(failed > 0 ? 'Workflow updated, but some parameters failed to save' : 'Workflow updated')
      setTimeout(() => { setToast(null); onClose() }, 1000)
    } catch {
      setToast('Failed to update workflow')
    }
  }

  return (
    <div className="fixed inset-y-0 right-0 w-[480px] bg-white shadow-xl flex flex-col z-50 border-l border-gray-200">
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <h2 className="font-semibold text-gray-900">Edit Workflow</h2>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">x</button>
      </div>

      {toast && (
        <div className={`mx-6 mt-4 px-4 py-2 rounded text-sm border ${
          toast.includes('Failed') || toast.includes('required') || toast.includes('incomplete')
            ? 'bg-red-50 border-red-200 text-red-700'
            : toast.includes('parameters failed')
              ? 'bg-amber-50 border-amber-200 text-amber-800'
              : 'bg-green-50 border-green-200 text-green-700'
        }`}>
          {toast}
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-6 py-4">
        {isLoading || rows === null ? (
          <LoadingSpinner />
        ) : (
          <form id="workflow-edit-form" onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Workflow name</label>
              <input required value={name} onChange={e => setName(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Status</label>
              <select value={status} onChange={e => setStatus(e.target.value as WorkflowStatus)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                {STATUS_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </div>
            <StepsEditor rows={rows} onChange={setRows} />
          </form>
        )}
      </div>

      <div className="px-6 py-4 border-t flex gap-3">
        <button type="submit" form="workflow-edit-form" disabled={update.isPending || rows === null}
          className="flex-1 bg-indigo-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50">
          {update.isPending ? 'Saving...' : 'Save changes'}
        </button>
        <button onClick={onClose} className="px-4 py-2 text-sm text-gray-600 hover:text-gray-900">Cancel</button>
      </div>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

function formatDate(iso?: string): string {
  if (!iso) return '--'
  return new Date(iso).toLocaleDateString()
}

export function WorkflowsPage() {
  const { data, isLoading, error } = useWorkflows()
  const deleteWorkflow = useDeleteWorkflow()

  const [showAdd, setShowAdd] = useState(false)
  const [editTargetId, setEditTargetId] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Workflow | null>(null)

  const workflows: Workflow[] = data?.data ?? []

  if (isLoading) return <div className="p-6"><LoadingSpinner /></div>
  if (error) return <div className="p-6 text-sm text-red-500">Failed to load workflows.</div>

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="Workflows"
        subtitle="Ordered chains of connectors a call can route through (definition only -- not yet executed)"
        actions={
          <button onClick={() => setShowAdd(true)}
            className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700">
            <Plus className="h-4 w-4" />
            Add workflow
          </button>
        }
      />

      <div className="flex-1 overflow-auto p-6">
        {workflows.length === 0 ? (
          <EmptyState
            title="No workflows defined"
            description="Chain connectors together into an ordered sequence of steps."
          />
        ) : (
          <div className="rounded-lg border border-gray-200 overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-left">Steps</th>
                  <th className="px-4 py-3 text-left">Created</th>
                  <th className="px-4 py-3 text-right"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {workflows.map(wf => (
                  <tr key={wf.id} className="hover:bg-gray-50">
                    <td className="px-4 py-3 font-medium text-gray-900">{wf.name}</td>
                    <td className="px-4 py-3"><StatusBadge status={wf.status} /></td>
                    <td className="px-4 py-3 text-xs text-gray-500">{wf.step_count ?? 0}</td>
                    <td className="px-4 py-3 text-xs text-gray-400">{formatDate(wf.created_at)}</td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-3">
                        <button onClick={() => setEditTargetId(wf.id)}
                          className="text-gray-400 hover:text-indigo-600" title="Edit">
                          <Pencil className="h-4 w-4" />
                        </button>
                        <button onClick={() => setDeleteTarget(wf)}
                          className="text-gray-400 hover:text-red-600" title="Delete">
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {showAdd && (
        <>
          <div className="fixed inset-0 bg-black/20 z-40" onClick={() => setShowAdd(false)} />
          <AddWorkflowPanel onClose={() => setShowAdd(false)} />
        </>
      )}

      {editTargetId && (
        <>
          <div className="fixed inset-0 bg-black/20 z-40" onClick={() => setEditTargetId(null)} />
          <EditWorkflowPanel workflowId={editTargetId} onClose={() => setEditTargetId(null)} />
        </>
      )}

      {deleteTarget && (
        <ConfirmDialog
          open
          title={'Delete "' + deleteTarget.name + '"?'}
          description="This workflow and its steps will be removed. This cannot be undone."
          confirmLabel="Delete"
          destructive
          isLoading={deleteWorkflow.isPending}
          onConfirm={() => { deleteWorkflow.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) }) }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
