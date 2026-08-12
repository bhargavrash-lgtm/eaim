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
import { Plus, Trash2, Pencil, ArrowUp, ArrowDown, AlertTriangle } from 'lucide-react'
import {
  PageHeader,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
} from '@/components/common'
import {
  useWorkflows,
  useWorkflow,
  useCreateWorkflow,
  useUpdateWorkflow,
  useDeleteWorkflow,
} from '@/hooks/useWorkflows'
import type { Workflow, WorkflowStatus } from '@/hooks/useWorkflows'
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

type StepRow = { gatewayToolId: string; action: string; toolDeleted?: boolean }

// validateAndConvertRows requires every row to be complete (a connector
// selected + an action) and returns an error naming the first incomplete
// row instead of silently dropping it. A step flagged toolDeleted (its
// connector was removed, see EditWorkflowPanel) is deliberately preserved
// with an empty gatewayToolId so an admin can see and fix it -- an
// earlier version of this editor filtered incomplete rows out silently on
// submit, which meant editing an unrelated field (renaming the workflow,
// reordering a different step) without touching the broken row would
// permanently delete it instead of surfacing it for repair, undoing the
// exact recovery flow AC4 exists for (code review finding).
function validateAndConvertRows(rows: StepRow[]): { steps: { gateway_tool_id: string; action: string }[]; error: string | null } {
  if (rows.length === 0) return { steps: [], error: 'At least one step is required' }
  const steps: { gateway_tool_id: string; action: string }[] = []
  for (let i = 0; i < rows.length; i++) {
    const gatewayToolId = rows[i].gatewayToolId.trim()
    const action = rows[i].action.trim()
    if (!gatewayToolId || !action) {
      return { steps: [], error: `Step ${i + 1} is incomplete -- select a connector and an action, or remove it` }
    }
    steps.push({ gateway_tool_id: gatewayToolId, action })
  }
  return { steps, error: null }
}

function StepsEditor({ rows, onChange }: { rows: StepRow[]; onChange: (rows: StepRow[]) => void }) {
  const { data: toolsData } = useTools()
  // Typed properly (mirrors ToolsPage.tsx's own identical pattern) so
  // action_paths -- B-046, already fetched here for free -- is usable
  // below without a second `any` cast.
  const tools: ToolWithActions[] = (toolsData as any)?.data ?? []

  function updateRow(i: number, patch: Partial<StepRow>) {
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function removeRow(i: number) {
    onChange(rows.filter((_, idx) => idx !== i))
  }
  function addRow() {
    onChange([...rows, { gatewayToolId: '', action: '' }])
  }
  function moveRow(i: number, dir: -1 | 1) {
    const j = i + dir
    if (j < 0 || j >= rows.length) return
    const next = [...rows]
    ;[next[i], next[j]] = [next[j], next[i]]
    onChange(next)
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
            <div key={i} className="flex items-center gap-2">
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
          ))}
        </div>
      )}
    </div>
  )
}

// ── Add Workflow panel ───────────────────────────────────────────────────────

function AddWorkflowPanel({ onClose }: { onClose: () => void }) {
  const create = useCreateWorkflow()
  const [toast, setToast] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [status, setStatus] = useState<WorkflowStatus>('draft')
  const [rows, setRows] = useState<StepRow[]>([])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const { steps, error } = validateAndConvertRows(rows)
    if (error) {
      setToast(error)
      return
    }
    try {
      await create.mutateAsync({ name, status, steps })
      setToast('Workflow created')
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
          toast.includes('Failed') || toast.includes('required') || toast.includes('incomplete') ? 'bg-red-50 border-red-200 text-red-700' : 'bg-green-50 border-green-200 text-green-700'
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
  const [toast, setToast] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [status, setStatus] = useState<WorkflowStatus>('draft')
  const [rows, setRows] = useState<StepRow[] | null>(null)

  // Initialize local form state once the workflow loads. A step whose
  // gateway_tool_id came back "" (deleted connector, see migration
  // 000006's ON DELETE SET NULL) is flagged toolDeleted so the dropdown
  // renders visibly broken rather than silently defaulting to blank.
  if (workflow && rows === null) {
    setName(workflow.name)
    setStatus(workflow.status)
    setRows((workflow.steps ?? []).map(s => ({
      gatewayToolId: s.gateway_tool_id,
      action: s.action,
      toolDeleted: !s.gateway_tool_id,
    })))
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const { steps, error } = validateAndConvertRows(rows ?? [])
    if (error) {
      setToast(error)
      return
    }
    try {
      await update.mutateAsync({ id: workflowId, body: { name, status, steps } })
      setToast('Workflow updated')
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
          toast.includes('Failed') || toast.includes('required') || toast.includes('incomplete') ? 'bg-red-50 border-red-200 text-red-700' : 'bg-green-50 border-green-200 text-green-700'
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
