// PoliciesPage.tsx -- Gateway / Policies CRUD
// Owned by FE-Gateway
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Plus, ChevronUp, ChevronDown, Pencil, Trash2 } from 'lucide-react'
import {
  PageHeader,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
  DataTable,
} from '@/components/common'
import { AppTopBar } from '@/components/layout/AppTopBar'
import type { Column } from '@/components/common'
import {
  usePolicies,
  useCreatePolicy,
  useUpdatePolicy,
  useDeletePolicy,
  useReorderPolicies,
} from '@/hooks/usePolicies'
import type { Policy, PolicyWithWorkspace } from '@/hooks/usePolicies'
// ActionBadge/StatusBadge/ScopeBadge/ConditionSummary and PolicyPanel
// (B-210): extracted to components/policies/ so the new workspace-scoped
// policy view can reuse them exactly rather than reinventing them -- see
// those files' own doc comments for the full reasoning (unchanged from
// this page's original comment: StatusPill's shape doesn't fit).
import { ActionBadge, StatusBadge, ScopeBadge, ConditionSummary } from '@/components/policies/PolicyBadges'
import { PolicyPanel, type PanelMode } from '@/components/policies/PolicyPanel'

// Main page

export function PoliciesPage() {
  const { data, isLoading, error } = usePolicies()
  const deletePolicy = useDeletePolicy()
  const reorder = useReorderPolicies()
  const createPolicy = useCreatePolicy()
  const updatePolicy = useUpdatePolicy()
  // Deep-linking/highlighting by ID (B-092): ?highlight=<policy id> lands
  // on and highlights that row via DataTable's getRowId/highlightRowId.
  const [searchParams] = useSearchParams()
  const highlightId = searchParams.get('highlight')

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
    {
      key: 'scope',
      header: 'Scope',
      render: (policy) => <ScopeBadge workspaceName={(policy as PolicyWithWorkspace).workspace_name} />,
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
      <AppTopBar
        breadcrumb={[{ label: 'Policies' }]}
        action={
          <button
            onClick={() => setPanel({ mode: 'create' })}
            className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700"
          >
            <Plus className="h-4 w-4" />
            New policy
          </button>
        }
      />
      <PageHeader subtitle="Ordered rule set evaluated per gateway call -- first match wins" />

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
            action={
              <button
                onClick={() => setPanel({ mode: 'create' })}
                className="mt-4 px-4 py-2 rounded-md bg-indigo-600 text-sm font-medium text-white hover:bg-indigo-700 transition-colors"
              >
                New policy
              </button>
            }
          />
        ) : (
          <DataTable
            columns={policyColumns}
            data={policies}
            onRowClick={(policy) => setPanel({ mode: 'edit', policy })}
            pageSize={1000}
            getRowId={(policy) => policy.id}
            highlightRowId={highlightId}
          />
        )}
      </div>

      {panel && (
        <PolicyPanel
          mode={panel.mode}
          policy={panel.policy}
          onClose={() => setPanel(null)}
          createMutation={createPolicy}
          updateMutation={updatePolicy}
        />
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
