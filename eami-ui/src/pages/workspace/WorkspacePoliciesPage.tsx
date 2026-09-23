// WorkspacePoliciesPage.tsx -- B-210: "Our Policies," real create/edit/
// delete for workspace_admins against the real B-209 API (previously only
// reachable via curl). Reuses B-214's ScopeBadge/ConditionSummary/
// ActionBadge/StatusBadge and the shared PolicyPanel form directly --
// per this brief's own explicit instruction not to reinvent them.
//
// Role gating mirrors the real backend enforcement exactly (defense in
// depth, not the actual boundary -- requireWorkspaceRole already 403s
// server-side regardless): a workspace_member sees every row read-only;
// a workspace_admin (or org-level admin, via useMyWorkspaces' role) gets
// create/edit/delete, but ONLY on rows that actually belong to this
// workspace -- the org-wide floor rows (workspace_id null) are shown for
// context (DESIGN_SYSTEM.md §7.3) but are never editable from here, since
// UpdateWorkspacePolicy/DeleteWorkspacePolicy would 404 on them anyway
// (workspace_policies.go filters by workspace_id, and a floor row's is
// NULL, never equal to this route's real workspaceId).
import { useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { Plus, Pencil, Trash2 } from 'lucide-react'
import { WorkspaceTopBar } from '@/components/layout/WorkspaceTopBar'
import { LoadingSpinner, EmptyState, DataTable, ConfirmDialog } from '@/components/common'
import { useToast } from '@/components/common/Toast'
import type { Column } from '@/components/common'
import { ActionBadge, StatusBadge, ScopeBadge, ConditionSummary } from '@/components/policies/PolicyBadges'
import { PolicyPanel, type PanelMode } from '@/components/policies/PolicyPanel'
import {
  useMyWorkspaces,
  useWorkspacePolicies,
  useCreateWorkspacePolicy,
  useUpdateWorkspacePolicy,
  useDeleteWorkspacePolicy,
} from '@/hooks/useWorkspaces'
import type { PolicyWithWorkspace } from '@/hooks/usePolicies'

export function WorkspacePoliciesPage() {
  const { workspaceId } = useOutletContext<{ workspaceId: string }>()
  const { showToast } = useToast()
  const { data: myWorkspaces } = useMyWorkspaces()
  const myRole = myWorkspaces?.data.find((m) => m.workspace_id === workspaceId)?.role
  const isAdmin = myRole === 'workspace_admin'

  const { data, isLoading, error } = useWorkspacePolicies(workspaceId)
  const createPolicy = useCreateWorkspacePolicy(workspaceId)
  const updatePolicy = useUpdateWorkspacePolicy(workspaceId)
  const deletePolicy = useDeleteWorkspacePolicy(workspaceId)

  const [panel, setPanel] = useState<{ mode: PanelMode; policy?: PolicyWithWorkspace } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<PolicyWithWorkspace | null>(null)

  const policies: PolicyWithWorkspace[] = data?.data ?? []

  if (isLoading) return (
    <div><WorkspaceTopBar title="Our Policies" /><div className="p-10"><LoadingSpinner /></div></div>
  )
  if (error) return (
    <div><WorkspaceTopBar title="Our Policies" /><div className="p-10 text-sm text-red-500">Failed to load policies.</div></div>
  )

  const columns: Column<PolicyWithWorkspace>[] = [
    { key: 'priority', header: 'Pri', render: (p) => <span className="text-gray-500 font-mono text-xs">{p.priority}</span> },
    {
      key: 'name',
      header: 'Name',
      render: (p) => (
        <>
          <div className="font-medium text-gray-900">{p.name}</div>
          {p.description && <div className="text-xs text-gray-400 truncate max-w-xs">{p.description}</div>}
        </>
      ),
    },
    { key: 'scope', header: 'Scope', render: (p) => <ScopeBadge workspaceName={p.workspace_name} /> },
    { key: 'conditions', header: 'Conditions', render: (p) => <ConditionSummary conditions={p.conditions} /> },
    {
      key: 'action',
      header: 'Action',
      render: (p) => (
        <>
          <ActionBadge action={p.action} />
          {p.alert && <span className="ml-1.5 text-xs text-amber-600">+ alert</span>}
        </>
      ),
    },
    { key: 'status', header: 'Status', render: (p) => <StatusBadge status={p.status} /> },
    ...(isAdmin
      ? [{
          key: 'actions',
          header: '',
          className: 'text-right',
          render: (p: PolicyWithWorkspace) => {
            const ownedByThisWorkspace = p.workspace_id === workspaceId
            if (!ownedByThisWorkspace) {
              return <span className="text-xs text-gray-400 italic">org floor</span>
            }
            return (
              <div className="flex items-center justify-end gap-3">
                <button onClick={(e) => { e.stopPropagation(); setPanel({ mode: 'edit', policy: p }) }}
                  className="text-gray-400 hover:text-indigo-600" title="Edit">
                  <Pencil className="h-4 w-4" />
                </button>
                <button onClick={(e) => { e.stopPropagation(); setDeleteTarget(p) }}
                  className="text-gray-400 hover:text-red-600" title="Delete">
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            )
          },
        } as Column<PolicyWithWorkspace>]
      : []),
  ]

  return (
    <div>
      <WorkspaceTopBar
        title="Our Policies"
        action={isAdmin ? (
          <button
            onClick={() => setPanel({ mode: 'create' })}
            className="flex items-center gap-1.5 bg-brand-500 text-white rounded-lg px-3 py-1.5 text-sm font-medium hover:bg-brand-600"
          >
            <Plus className="h-4 w-4" />
            New policy
          </button>
        ) : undefined}
      />
      <div className="p-10">
        {policies.length === 0 ? (
          <EmptyState
            title="No policies yet"
            description={isAdmin ? 'Create your first policy to add a restriction on top of the org floor.' : 'No policies apply to this workspace yet.'}
          />
        ) : (
          <DataTable
            columns={columns}
            data={policies}
            onRowClick={isAdmin ? (p) => { if (p.workspace_id === workspaceId) setPanel({ mode: 'edit', policy: p }) } : undefined}
            pageSize={1000}
            getRowId={(p) => p.id}
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
            deletePolicy.mutate(deleteTarget.id, {
              onSuccess: () => setDeleteTarget(null),
              onError: () => showToast('Delete failed', { type: 'error' }),
            })
          }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
