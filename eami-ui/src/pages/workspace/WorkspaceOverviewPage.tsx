// WorkspaceOverviewPage.tsx -- B-210. Real, minimal Overview: workspace
// name/description, real membership list with real roles. No fabricated
// metrics -- Part A confirmed AI Usage Health/Spend/Team Adoption have
// zero real backend support today (audit_log/token_usage have no
// workspace_id at all, confirmed directly in migration
// 000021_groups_workspaces.up.sql's own comment), so none of the Layer4
// canvas mockup's illustrative dashboard cards are built here.
import { useOutletContext } from 'react-router-dom'
import { WorkspaceTopBar } from '@/components/layout/WorkspaceTopBar'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { useWorkspace, useWorkspaceMembers } from '@/hooks/useWorkspaces'

export function WorkspaceOverviewPage() {
  const { workspaceId } = useOutletContext<{ workspaceId: string }>()
  const { data: workspace, isLoading: wsLoading, error: wsError } = useWorkspace(workspaceId)
  const { data: membersData, isLoading: membersLoading, error: membersError } = useWorkspaceMembers(workspaceId)
  const members = membersData?.data ?? []

  return (
    <div>
      <WorkspaceTopBar title="Overview" />
      <div className="max-w-3xl p-10 space-y-8">
        {wsLoading ? (
          <LoadingSpinner />
        ) : wsError ? (
          <p className="text-sm text-red-600">Failed to load this workspace. Reload and try again.</p>
        ) : (
          <div>
            <h1 className="text-2xl font-bold text-ink">{workspace?.name}</h1>
            <p className="mt-1.5 text-sm text-ink-muted">
              {workspace?.description ?? 'No description set for this workspace yet.'}
            </p>
          </div>
        )}

        <div className="bg-white rounded-xl shadow-l1 border border-[rgba(228,231,240,0.55)] p-6">
          <h2 className="text-sm font-semibold text-ink mb-4">
            Members{members.length > 0 ? ` (${members.length})` : ''}
          </h2>
          {membersLoading ? (
            <LoadingSpinner />
          ) : membersError ? (
            <p className="text-sm text-red-600">Failed to load members. Reload and try again.</p>
          ) : members.length === 0 ? (
            <p className="text-sm text-ink-faint italic">No members yet.</p>
          ) : (
            <ul className="divide-y divide-gray-100">
              {members.map((m) => (
                <li key={m.user_id} className="flex items-center justify-between py-2.5">
                  <span className="text-sm text-ink">{m.email}</span>
                  <span className="text-xs font-medium text-ink-muted capitalize">
                    {m.role.replace('workspace_', '')}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}
