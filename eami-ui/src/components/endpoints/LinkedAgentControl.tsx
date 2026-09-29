import { useState } from 'react'
import { useLinkEndpointAgent } from '@/hooks/useEndpoints'
import { useAgents } from '@/hooks/useAgents'
import { can, useOrgRole } from '@/lib/rbac'
import type { components } from '@/api/schema'
import { Link2, Link2Off } from 'lucide-react'

type Endpoint = components['schemas']['Endpoint']

// ── Linked governed agent (B-164/B-165) ─────────────────────────────────────
//
// endpoints.gateway_agent_id has no automatic derivation -- eami-agent's
// own free-text discovery identity (agent_id/hostname) shares no
// relationship with a real gateway_agents.id. This control is the only
// place that link is ever set or cleared: an explicit admin decision, not
// an inferred one. Setting or clearing it is admin-only (B-253), so other
// roles see the current link as read-only text (B-252 C0).
export function LinkedAgentControl({ endpoint }: { endpoint: Endpoint }) {
  const { data: agentsData } = useAgents()
  const linkMutation = useLinkEndpointAgent()
  const canLink = can.linkEndpointAgent(useOrgRole())
  const [pendingSelection, setPendingSelection] = useState<string | null>(null)

  const agents = agentsData?.data ?? []
  const linkedId = endpoint.gateway_agent_id ?? ''

  function handleChange(value: string) {
    setPendingSelection(value)
    linkMutation.mutate(
      { endpointId: endpoint.id, gatewayAgentId: value === '' ? null : value },
      { onSettled: () => setPendingSelection(null) },
    )
  }

  return (
    <div className="rounded-lg border border-gray-200 px-4 py-3">
      <div className="mb-1.5 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
        {linkedId ? <Link2 className="h-3.5 w-3.5" /> : <Link2Off className="h-3.5 w-3.5" />}
        Linked governed agent
      </div>
      {canLink ? (
        <>
          <select
            value={pendingSelection ?? linkedId}
            onChange={(e) => handleChange(e.target.value)}
            disabled={linkMutation.isPending}
            className="w-full rounded border border-gray-300 px-2 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-brand-500 disabled:opacity-50"
          >
            <option value="">Not linked</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
          {linkMutation.isError && (
            <p className="mt-1 text-xs text-red-600">Failed to update link. Try again.</p>
          )}
        </>
      ) : (
        <p className="text-sm text-gray-700">
          {linkedId ? (agents.find((a) => a.id === linkedId)?.name ?? linkedId) : 'Not linked'}
        </p>
      )}
      <p className="mt-1.5 text-2xs text-gray-400">
        No automatic match exists between this endpoint's discovery identity and a governed agent — {canLink ? 'link it manually if you know who operates it.' : 'an admin can link it manually.'}
      </p>
    </div>
  )
}
