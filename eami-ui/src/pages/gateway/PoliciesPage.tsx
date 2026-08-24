// PoliciesPage.tsx -- Gateway / Policies CRUD
// Owned by FE-Gateway
import { useState } from 'react'
import { Plus, ChevronUp, ChevronDown, Pencil, Trash2 } from 'lucide-react'
import {
  PageHeader,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
  DataTable,
} from '@/components/common'
import type { Column } from '@/components/common'
import {
  usePolicies,
  useCreatePolicy,
  useUpdatePolicy,
  useDeletePolicy,
  useReorderPolicies,
} from '@/hooks/usePolicies'
import type { Policy, PolicyCreate, PolicyUpdate } from '@/hooks/usePolicies'

// Badges

const ACTION_STYLES: Record<string, string> = {
  allow:    'bg-green-100 text-green-800',
  deny:     'bg-red-100 text-red-800',
  escalate: 'bg-amber-100 text-amber-800',
}

const STATUS_STYLES: Record<string, string> = {
  active:   'bg-green-100 text-green-800',
  draft:    'bg-gray-100 text-gray-600',
  disabled: 'bg-red-100 text-red-700',
}

function ActionBadge({ action }: { action: string }) {
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-semibold capitalize ${ACTION_STYLES[action] ?? 'bg-gray-100 text-gray-600'}`}>
      {action}
    </span>
  )
}

function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize ${STATUS_STYLES[status] ?? 'bg-gray-100 text-gray-600'}`}>
      {status}
    </span>
  )
}

function ConditionSummary({ conditions }: { conditions: Policy['conditions'] }) {
  const parts: string[] = []
  if (conditions.agent_name_pattern) parts.push('agent: ' + conditions.agent_name_pattern)
  if (conditions.tool_names?.length) parts.push('tools: ' + conditions.tool_names.join(', '))
  if (conditions.action_types?.length) parts.push('actions: ' + conditions.action_types.join(', '))
  if (conditions.environments?.length) parts.push('env: ' + conditions.environments.join(', '))
  if (conditions.record_count_gt != null) parts.push('records > ' + conditions.record_count_gt)
  if (conditions.semantic_rule) parts.push('semantic rule set')
  if (conditions.scope_drift) parts.push('scope drift')
  if (parts.length === 0) return <span className="text-gray-400 italic text-xs">any request</span>
  return <span className="text-xs text-gray-600 truncate max-w-xs" title={parts.join(' / ')}>{parts.join(' / ')}</span>
}

// Slide-out panel

type PanelMode = 'create' | 'edit'

interface PanelProps {
  mode: PanelMode
  policy?: Policy
  onClose: () => void
}

const ENVIRONMENTS = ['any', 'production', 'staging', 'development'] as const

function PolicyPanel({ mode, policy, onClose }: PanelProps) {
  const create = useCreatePolicy()
  const update = useUpdatePolicy()
  const [toast, setToast] = useState<string | null>(null)

  const [name, setName] = useState(policy?.name ?? '')
  const [description, setDescription] = useState(policy?.description ?? '')
  const [priority, setPriority] = useState(policy?.priority ?? 10)
  const [action, setAction] = useState<'allow' | 'deny' | 'escalate'>(
    (policy?.action as 'allow' | 'deny' | 'escalate') ?? 'deny'
  )
  const [status, setStatus] = useState<'active' | 'draft' | 'disabled'>(
    (policy?.status as 'active' | 'draft' | 'disabled') ?? 'draft'
  )
  const [alert, setAlert] = useState(policy?.alert ?? false)

  const [agentPattern, setAgentPattern] = useState(policy?.conditions.agent_name_pattern ?? '')
  const [toolNames, setToolNames]       = useState(policy?.conditions.tool_names?.join(', ') ?? '')
  const [actionTypes, setActionTypes]   = useState(policy?.conditions.action_types?.join(', ') ?? '')
  const [environments, setEnvironments] = useState<string[]>(policy?.conditions.environments ?? [])
  const [recordCountGt, setRecordCountGt] = useState(policy?.conditions.record_count_gt?.toString() ?? '')
  const [semanticRule, setSemanticRule] = useState(policy?.conditions.semantic_rule ?? '')
  const [scopeDrift, setScopeDrift]     = useState(policy?.conditions.scope_drift ?? false)

  const isPending = create.isPending || update.isPending

  function buildConditions() {
    return {
      agent_name_pattern: agentPattern.trim() || undefined,
      tool_names: toolNames.trim() ? toolNames.split(',').map(s => s.trim()).filter(Boolean) : undefined,
      action_types: actionTypes.trim() ? actionTypes.split(',').map(s => s.trim()).filter(Boolean) : undefined,
      environments: environments.length ? (environments as ('production' | 'staging' | 'development' | 'any')[]) : undefined,
      record_count_gt: recordCountGt ? parseInt(recordCountGt, 10) : undefined,
      semantic_rule: semanticRule.trim() || undefined,
      scope_drift: scopeDrift || undefined,
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    try {
      if (mode === 'create') {
        const body: PolicyCreate = {
          name, description: description || undefined, priority, action, alert,
          status: status === 'disabled' ? 'draft' : status,
          conditions: buildConditions(),
        }
        await create.mutateAsync(body)
      } else {
        const body: PolicyUpdate = {
          name, description: description || undefined, priority, action, alert, status,
          conditions: buildConditions(),
        }
        await update.mutateAsync({ id: policy!.id, body })
      }
      setToast(mode === 'create' ? 'Policy created' : 'Policy saved')
      setTimeout(() => { setToast(null); onClose() }, 1000)
    } catch {
      setToast('Save failed')
    }
  }

  function toggleEnv(env: string) {
    setEnvironments(prev => prev.includes(env) ? prev.filter(e => e !== env) : [...prev, env])
  }

  return (
    <div className="fixed inset-y-0 right-0 w-[480px] bg-white shadow-xl flex flex-col z-50 border-l border-gray-200">
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <div>
          <h2 className="font-semibold text-gray-900">
            {mode === 'create' ? 'New Policy' : 'Edit Policy'}
          </h2>
          {policy && <p className="text-xs text-gray-500 truncate">{policy.name}</p>}
        </div>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">x</button>
      </div>

      {toast && (
        <div className={`mx-6 mt-4 px-4 py-2 rounded text-sm border ${
          toast.includes('failed') ? 'bg-red-50 border-red-200 text-red-700' : 'bg-green-50 border-green-200 text-green-700'
        }`}>
          {toast}
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-6 py-4">
        <form id="policy-form" onSubmit={handleSubmit} className="space-y-6">

          <div className="space-y-4">
            <p className="text-xs font-semibold text-gray-500 uppercase tracking-wide">Basic</p>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Name</label>
              <input required value={name} onChange={e => setName(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="Block production deletes" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Description</label>
              <textarea value={description} onChange={e => setDescription(e.target.value)} rows={2}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Priority (lower = first)</label>
                <input type="number" min={1} required value={priority}
                  onChange={e => setPriority(parseInt(e.target.value, 10) || 1)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Status</label>
                <select value={status} onChange={e => setStatus(e.target.value as typeof status)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  <option value="active">Active</option>
                  <option value="draft">Draft</option>
                  {mode === 'edit' && <option value="disabled">Disabled</option>}
                </select>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Action</label>
                <select value={action} onChange={e => setAction(e.target.value as typeof action)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  <option value="allow">Allow</option>
                  <option value="deny">Deny</option>
                  <option value="escalate">Escalate (require approval)</option>
                </select>
              </div>
              <div className="flex items-end pb-2">
                <label className="flex items-center gap-2 text-sm text-gray-700 cursor-pointer">
                  <input type="checkbox" checked={alert} onChange={e => setAlert(e.target.checked)}
                    className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500" />
                  Send alert on match
                </label>
              </div>
            </div>
          </div>

          <div className="space-y-4 pt-4 border-t">
            <p className="text-xs font-semibold text-gray-500 uppercase tracking-wide">Conditions (all specified must match)</p>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Agent name pattern (glob)</label>
              <input value={agentPattern} onChange={e => setAgentPattern(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="claude-support-* or leave blank for any" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Tool names (comma-separated)</label>
              <input value={toolNames} onChange={e => setToolNames(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="delete_file, drop_table" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Action verbs (comma-separated)</label>
              <input value={actionTypes} onChange={e => setActionTypes(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="delete, drop, truncate" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">Environments</label>
              <div className="flex flex-wrap gap-3">
                {ENVIRONMENTS.map(env => (
                  <label key={env} className="flex items-center gap-1.5 text-sm text-gray-700 cursor-pointer">
                    <input type="checkbox" checked={environments.includes(env)} onChange={() => toggleEnv(env)}
                      className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500" />
                    <span className="font-mono text-xs">{env}</span>
                  </label>
                ))}
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Estimated records greater than</label>
              <input type="number" min={0} value={recordCountGt} onChange={e => setRecordCountGt(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="e.g. 1000 -- leave blank to skip" />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Semantic rule (LLM-evaluated)</label>
              <textarea value={semanticRule} onChange={e => setSemanticRule(e.target.value)} rows={2}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="Agent must not exfiltrate PII" />
            </div>

            <label className="flex items-center gap-2 text-sm text-gray-700 cursor-pointer">
              <input type="checkbox" checked={scopeDrift} onChange={e => setScopeDrift(e.target.checked)}
                className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500" />
              Match on scope drift (agent acts outside declared task)
            </label>
          </div>
        </form>
      </div>

      <div className="px-6 py-4 border-t flex gap-3">
        <button type="submit" form="policy-form" disabled={isPending}
          className="flex-1 bg-indigo-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50">
          {isPending ? 'Saving...' : mode === 'create' ? 'Create policy' : 'Save changes'}
        </button>
        <button onClick={onClose} className="px-4 py-2 text-sm text-gray-600 hover:text-gray-900">Cancel</button>
      </div>
    </div>
  )
}

// Main page

export function PoliciesPage() {
  const { data, isLoading, error } = usePolicies()
  const deletePolicy = useDeletePolicy()
  const reorder = useReorderPolicies()

  const [panel, setPanel] = useState<{ mode: PanelMode; policy?: Policy } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Policy | null>(null)

  // Already returned in priority order (ORDER BY priority ASC) -- row
  // position IS evaluation rank, so array index doubles as display rank.
  const policies: Policy[] = (data as any)?.data ?? []

  // The reorder endpoint renumbers priority sequentially from array
  // position for every id it's given, so a move must always submit every
  // policy's id in its new full order -- submitting only the two swapped
  // ids would leave the rest with stale, potentially colliding priorities.
  function movePolicy(index: number, direction: -1 | 1) {
    const target = index + direction
    if (target < 0 || target >= policies.length || reorder.isPending) return
    const orderedIds = policies.map(p => p.id)
    ;[orderedIds[index], orderedIds[target]] = [orderedIds[target], orderedIds[index]]
    reorder.mutate(orderedIds)
  }

  if (isLoading) return <div className="p-6"><LoadingSpinner /></div>
  if (error)    return <div className="p-6 text-sm text-red-500">Failed to load policies.</div>

  // B-104: shared DataTable (closes the row-click bug class B-081/082/083
  // found). policies.indexOf(policy) recovers each row's true position in
  // the priority-ordered array from inside a Column.render callback (which
  // only receives the row, not an index) -- correct regardless of
  // DataTable's own pagination, and safe specifically because no column
  // here is marked sortable, so DataTable never reorders `policies` itself.
  // pageSize is set high enough to never engage DataTable's own pager,
  // matching this page's existing "show every policy" behavior.
  const policyColumns: Column<Policy>[] = [
    {
      key: 'reorder',
      header: '',
      className: 'w-8',
      render: (policy) => {
        const idx = policies.indexOf(policy)
        return (
          <div className="flex flex-col">
            <button
              onClick={(e) => { e.stopPropagation(); movePolicy(idx, -1) }}
              disabled={idx === 0 || reorder.isPending}
              title="Move up (higher priority)"
              className="text-gray-400 hover:text-indigo-600 disabled:opacity-25 disabled:hover:text-gray-400"
            >
              <ChevronUp className="h-3.5 w-3.5" />
            </button>
            <button
              onClick={(e) => { e.stopPropagation(); movePolicy(idx, 1) }}
              disabled={idx === policies.length - 1 || reorder.isPending}
              title="Move down (lower priority)"
              className="text-gray-400 hover:text-indigo-600 disabled:opacity-25 disabled:hover:text-gray-400"
            >
              <ChevronDown className="h-3.5 w-3.5" />
            </button>
          </div>
        )
      },
    },
    { key: 'priority', header: 'Pri', render: (policy) => <span className="text-gray-500 font-mono text-xs">{policy.priority}</span> },
    {
      key: 'name',
      header: 'Name',
      render: (policy) => (
        <>
          <div className="font-medium text-gray-900">{policy.name}</div>
          {policy.description && (
            <div className="text-xs text-gray-400 truncate max-w-xs">{policy.description}</div>
          )}
        </>
      ),
    },
    { key: 'conditions', header: 'Conditions', render: (policy) => <ConditionSummary conditions={policy.conditions} /> },
    {
      key: 'action',
      header: 'Action',
      render: (policy) => (
        <>
          <ActionBadge action={policy.action} />
          {policy.alert && <span className="ml-1.5 text-xs text-amber-600">+ alert</span>}
        </>
      ),
    },
    { key: 'status', header: 'Status', render: (policy) => <StatusBadge status={policy.status} /> },
    {
      key: 'actions',
      header: '',
      className: 'text-right',
      render: (policy) => (
        <div className="flex items-center justify-end gap-3">
          <button onClick={(e) => { e.stopPropagation(); setPanel({ mode: 'edit', policy }) }}
            className="text-gray-400 hover:text-indigo-600" title="Edit">
            <Pencil className="h-4 w-4" />
          </button>
          <button onClick={(e) => { e.stopPropagation(); setDeleteTarget(policy) }}
            className="text-gray-400 hover:text-red-600" title="Delete">
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      ),
    },
  ]

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="Policies"
        subtitle="Ordered rule set evaluated per gateway call -- first match wins"
        actions={
          <button
            onClick={() => setPanel({ mode: 'create' })}
            className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700"
          >
            <Plus className="h-4 w-4" />
            New policy
          </button>
        }
      />

      <div className="flex-1 overflow-auto p-6">
        {reorder.isError && (
          <div className="mb-4 px-4 py-2 rounded text-sm border bg-red-50 border-red-200 text-red-700">
            Failed to save new policy order -- reload and try again.
          </div>
        )}
        {policies.length === 0 ? (
          <EmptyState
            title="No policies yet"
            description="Create your first policy to start governing gateway traffic."
          />
        ) : (
          <DataTable
            columns={policyColumns}
            data={policies}
            onRowClick={(policy) => setPanel({ mode: 'edit', policy })}
            pageSize={1000}
          />
        )}
      </div>

      {panel && (
        <>
          <div className="fixed inset-0 bg-black/20 z-40" onClick={() => setPanel(null)} />
          <PolicyPanel mode={panel.mode} policy={panel.policy} onClose={() => setPanel(null)} />
        </>
      )}

      {deleteTarget && (
        <ConfirmDialog
          open
          title={'Delete "' + deleteTarget.name + '"?'}
          description="This policy will be permanently removed."
          confirmLabel="Delete"
          destructive
          isLoading={deletePolicy.isPending}
          onConfirm={() => {
            deletePolicy.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) })
          }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
