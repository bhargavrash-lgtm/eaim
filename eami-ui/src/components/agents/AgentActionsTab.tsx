// AgentActionsTab -- Agent Detail's Actions tab (DESIGN_SYSTEM.md §7.7).
// Hosts the same four lifecycle actions as the Agents list's row buttons,
// calling the exact same mutations (useUpdateAgent / useDeleteAgent /
// AgentConfigPanel's useUpdateAgentConfig) with the same toggle rule,
// confirmation and error handling. Differences from the list, all
// founder-approved: a successful delete returns to the Agents list (this
// page's agent no longer exists), and each action renders only for the
// roles the API accepts it from (B-252 C0, lib/rbac.ts) instead of a
// button that 403s. Step-up authentication for delete/reactivate is a
// separate tracked item (B-231), deliberately not added here.
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Ban, PlayCircle, Settings2, Trash2 } from 'lucide-react'
import { Button, ConfirmDialog, useToast } from '@/components/common'
import { useDeleteAgent, useUpdateAgent } from '@/hooks/useAgents'
import type { Agent } from '@/hooks/useAgents'
import { can, useOrgRole } from '@/lib/rbac'
import { AgentConfigPanel } from './AgentConfigPanel'

function errorText(err: unknown, fallback: string) {
  return (err as { message?: string } | null)?.message ?? fallback
}

export function AgentActionsTab({ agent }: { agent: Agent }) {
  const navigate = useNavigate()
  const role = useOrgRole()
  const { showToast } = useToast()
  const updateAgent = useUpdateAgent()
  const deleteAgent = useDeleteAgent()
  const [showConfig, setShowConfig] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const status = (agent as { status?: string }).status
  const suspended = status === 'suspended'

  // Same rule as AgentsPage's handleToggleSuspend: suspended -> active,
  // anything else -> suspended (see B-230 for the revoked-agent gap).
  function handleToggleSuspend() {
    setActionError(null)
    updateAgent.mutate(
      { id: agent.id, body: { status: suspended ? 'active' : 'suspended' } },
      {
        // A non-admin can't undo a suspend (reactivate is admin-only), so the
        // row disappears -- confirm it rather than leave it silent (B-252 C0).
        onSuccess: () => { if (!suspended && !can.reactivateAgent(role)) showToast('Agent suspended. Reactivating it requires an admin.', { type: 'success' }) },
        onError: (err) => setActionError(errorText(err, 'Failed to update agent status')),
      },
    )
  }

  function handleConfirmDelete() {
    setActionError(null)
    deleteAgent.mutate(agent.id, {
      // The agent no longer exists, so staying here would only refetch a 404.
      // useDeleteAgent leaves this agent's own detail query un-refetched
      // (stale only), so there is no 404 refetch to wait for before leaving.
      onSuccess: () => navigate('/gateway/agents', { replace: true }),
      // Kept open on error, as on the list: a 409 ("suspend it instead")
      // is information the admin needs to read.
      onError: (err) => setActionError(errorText(err, 'Failed to delete agent')),
    })
  }

  // B-253: suspend is containment (admin + operator); reactivate and delete
  // expand or destroy (admin only).
  const allRows: { title: string; show: boolean; description: string; action: React.ReactNode }[] = [
    {
      title: 'Configure',
      show: can.configureAgent(role),
      description: 'Endpoint scanner settings (scan interval, model paths, enabled scanners) served to an endpoint linked to this agent.',
      action: (
        <Button variant="outline" size="sm" onClick={() => setShowConfig(true)}>
          <Settings2 className="h-4 w-4" />Configure
        </Button>
      ),
    },
    {
      title: suspended ? 'Reactivate' : 'Suspend',
      show: suspended ? can.reactivateAgent(role) : can.suspendAgent(role),
      description: suspended
        ? 'Restore this agent so it can obtain tokens and dispatch through the gateway again.'
        : 'Immediately stop this agent from obtaining tokens or dispatching through the gateway. Reversible.',
      action: (
        <Button variant="outline" size="sm" isLoading={updateAgent.isPending} onClick={handleToggleSuspend}>
          {suspended ? <PlayCircle className="h-4 w-4" /> : <Ban className="h-4 w-4" />}
          {suspended ? 'Reactivate' : 'Suspend'}
        </Button>
      ),
    },
    {
      title: 'Delete',
      show: can.deleteAgent(role),
      description: 'Permanently remove this agent identity. Refused if it has episode, approval or workflow-run history; suspend it instead.',
      action: (
        <Button variant="destructive" size="sm" disabled={updateAgent.isPending} onClick={() => { setActionError(null); setConfirmDelete(true) }}>
          <Trash2 className="h-4 w-4" />Delete
        </Button>
      ),
    },
  ]
  const rows = allRows.filter((row) => row.show)

  if (rows.length === 0) {
    return (
      <div className="rounded-[10px] border border-[rgba(228,231,240,0.55)] bg-white px-[26px] py-[22px] text-sm text-gray-500 shadow-l1">
        Your role has read-only access to this agent. Configuring and suspending agents require the admin or operator role; reactivating and deleting require admin.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {actionError && !confirmDelete && (
        <div className="flex items-start gap-3 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
          <span className="flex-1">{actionError}</span>
          <button onClick={() => setActionError(null)} className="shrink-0 text-base leading-none text-red-500 hover:text-red-700" aria-label="Dismiss">×</button>
        </div>
      )}
      <div className="divide-y divide-gray-100 rounded-[10px] border border-[rgba(228,231,240,0.55)] bg-white shadow-l1">
        {rows.map((row) => (
          <div key={row.title} className="flex items-center justify-between gap-6 px-[26px] py-[18px]">
            <div>
              <div className="text-sm font-semibold text-ink">{row.title}</div>
              <div className="mt-0.5 text-xs text-gray-500">{row.description}</div>
            </div>
            <div className="shrink-0">{row.action}</div>
          </div>
        ))}
      </div>

      {showConfig && <AgentConfigPanel agent={agent} onClose={() => setShowConfig(false)} />}

      {confirmDelete && (
        <ConfirmDialog
          open
          title={`Delete "${agent.name}"?`}
          description="This permanently removes the agent identity. If it has real episode, approval, or workflow-run history, deletion will be refused -- suspend it instead."
          confirmLabel="Delete"
          destructive
          isLoading={deleteAgent.isPending}
          onConfirm={handleConfirmDelete}
          onCancel={() => { setConfirmDelete(false); setActionError(null) }}
        >
          {actionError && (
            <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800">{actionError}</div>
          )}
        </ConfirmDialog>
      )}
    </div>
  )
}
