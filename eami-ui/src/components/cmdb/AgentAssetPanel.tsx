import { X } from 'lucide-react'
import { SlideOverPanel } from '@/components/common/SlideOverPanel'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { StatusPill } from '@/components/common/StatusPill'
import { RiskPill } from '@/components/common/RiskPill'
import { AssetWorkspaceBadge } from './AssetWorkspaceBadge'
import { useAgentConnections } from '@/hooks/useAgents'
import type { AgentWithWorkspace } from '@/hooks/useAgents'

// AgentAssetPanel -- B-196 increment 1's real agent detail view.
//
// Deliberately NOT the relationship-graph canvas (RelationshipGraph.tsx,
// B-200) AgentDetailPage.tsx renders -- the task brief is explicit: "no
// new relationship-graph work" for this increment. This reuses the exact
// same real data source (useAgentConnections -> GET /v1/gateway/agents/
// {agentId}/connections, B-200's own proven endpoint) and renders it as a
// plain, real list instead -- the data is real, only the visualization is
// simpler, on purpose.
export function AgentAssetPanel({ agent, onClose }: { agent: AgentWithWorkspace; onClose: () => void }) {
  const { data: connections, isLoading, error } = useAgentConnections(agent.id)

  return (
    <SlideOverPanel onClose={onClose}>
      <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4">
        <h2 className="text-base font-semibold text-gray-900">{agent.name}</h2>
        <button onClick={onClose} className="rounded p-1 text-gray-400 hover:bg-gray-100">
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-5 space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <StatusPill status={agent.status as 'active' | 'suspended' | 'revoked'} />
          <RiskPill tier={agent.risk_tier as 'low' | 'medium' | 'high' | 'critical'} />
          <AssetWorkspaceBadge scoped workspaceName={agent.workspace_name} />
        </div>

        <div className="grid grid-cols-2 gap-2 text-xs">
          {(
            [
              ['Model', agent.model],
              ['Owner', agent.owner],
              ['Scope', agent.scope],
            ] as [string, string][]
          ).map(([k, v]) => (
            <div key={k} className="rounded bg-gray-50 px-3 py-2">
              <div className="text-gray-400 uppercase tracking-wide text-2xs font-semibold">{k}</div>
              <div className="mt-0.5 font-medium text-gray-800 break-all">{v}</div>
            </div>
          ))}
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-10">
            <LoadingSpinner />
          </div>
        ) : error ? (
          <p className="text-sm text-red-600">Failed to load this agent's real connections. Try again.</p>
        ) : (
          <>
            <ConnectionSection title="Endpoint">
              {connections?.endpoint ? (
                <p className="text-sm text-gray-700">{connections.endpoint.hostname}</p>
              ) : (
                <p className="text-sm text-gray-400">No endpoint linked</p>
              )}
            </ConnectionSection>

            <ConnectionSection title={`Tools (${connections?.tools.length ?? 0})`}>
              {(connections?.tools ?? []).length === 0 ? (
                <p className="text-sm text-gray-400">None</p>
              ) : (
                <ul className="space-y-1">
                  {connections!.tools.map((t) => (
                    <li key={t.tool_name} className="flex items-center justify-between text-sm">
                      <span className="font-medium text-gray-800">{t.tool_name}</span>
                      <span className="text-xs text-gray-400">{t.call_count_24h} calls / 24h</span>
                    </li>
                  ))}
                </ul>
              )}
            </ConnectionSection>

            <ConnectionSection title={`Policies (${connections?.policies.length ?? 0})`}>
              {(connections?.policies ?? []).length === 0 ? (
                <p className="text-sm text-gray-400">None</p>
              ) : (
                <ul className="space-y-1">
                  {connections!.policies.map((p) => (
                    <li key={p.policy_id} className="flex items-center justify-between text-sm">
                      <span className="font-medium text-gray-800">{p.name}</span>
                      <span className="text-xs text-gray-400 capitalize">{p.action}</span>
                    </li>
                  ))}
                </ul>
              )}
            </ConnectionSection>

            <ConnectionSection title={`Workflows (${connections?.workflows.length ?? 0})`}>
              {(connections?.workflows ?? []).length === 0 ? (
                <p className="text-sm text-gray-400">None</p>
              ) : (
                <ul className="space-y-1">
                  {connections!.workflows.map((w) => (
                    <li key={w.workflow_id} className="text-sm font-medium text-gray-800">
                      {w.name}
                    </li>
                  ))}
                </ul>
              )}
            </ConnectionSection>
          </>
        )}
      </div>
    </SlideOverPanel>
  )
}

function ConnectionSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-gray-200 rounded-lg overflow-hidden">
      <div className="px-4 py-2 bg-gray-50 text-sm font-medium text-gray-700">{title}</div>
      <div className="px-4 py-3 bg-white">{children}</div>
    </div>
  )
}
