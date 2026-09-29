// AgentLink (B-252 C1) -- an agent name on an activity page (Audit, FinOps,
// Memory) that links to Agent Detail by agent_id. An id alone doesn't prove
// the agent still exists (audit rows outlive deleted agents), so the id is
// checked against the org's agents list first: a real link when it exists,
// plain text marked "agent no longer exists" when it doesn't, and plain text
// while the list loads or when the row carries no agent_id.
import { useEffect, useMemo, useRef } from 'react'
import { Link } from 'react-router-dom'
import { useAgents } from '@/hooks/useAgents'

export function AgentLink({ agentId, name, className = '', stacked = false }: {
  agentId?: string | null
  name: string
  className?: string
  // Put the "no longer exists" marker on its own line, for narrow truncated
  // cells where a trailing suffix would be cut off (Audit's agent column).
  stacked?: boolean
}) {
  const { data, isSuccess, isFetching, dataUpdatedAt, refetch } = useAgents()
  const ids = useMemo(() => new Set((data?.data ?? []).map((a) => a.id)), [data])
  const exists = !!agentId && ids.has(agentId)
  // A miss may only mean the cached list predates a newly created agent
  // (30 s staleTime). Refresh once before declaring it deleted; joining any
  // in-flight fetch (cancelRefetch: false) so many rows cause one request.
  const refreshed = useRef(false)
  const staleMiss = !!agentId && isSuccess && !exists && Date.now() - dataUpdatedAt > 2000
  useEffect(() => {
    if (staleMiss && !refreshed.current) {
      refreshed.current = true
      refetch({ cancelRefetch: false })
    }
  }, [staleMiss, refetch])

  if (!agentId || !isSuccess || (!exists && (isFetching || staleMiss))) return <span className={className}>{name}</span>
  if (!exists) {
    return stacked ? (
      <span className={className} title={`${name} — this agent no longer exists`}>
        <span className="block truncate">{name}</span>
        <span className="block text-xs font-normal text-gray-400">agent no longer exists</span>
      </span>
    ) : (
      <span className={className} title="This agent no longer exists">
        {name} <span className="text-xs font-normal text-gray-400">· agent no longer exists</span>
      </span>
    )
  }
  return (
    <Link
      to={`/assets/agents/${agentId}`}
      // The link sits inside clickable rows (Audit opens a detail panel):
      // follow the link without also triggering the row.
      onClick={(e) => e.stopPropagation()}
      className={`${className} text-brand-700 hover:underline`}
    >
      {name}
    </Link>
  )
}
