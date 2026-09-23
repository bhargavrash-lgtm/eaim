import { useState } from 'react'
import { AppTopBar } from '@/components/layout/AppTopBar'
import { PageHeader } from '@/components/common/PageHeader'
import { DataTable } from '@/components/common/DataTable'
import type { Column } from '@/components/common/DataTable'
import { EmptyState } from '@/components/common/EmptyState'
import { StatusPill } from '@/components/common/StatusPill'
import { RiskPill } from '@/components/common/RiskPill'
import { AssetWorkspaceBadge } from '@/components/cmdb/AssetWorkspaceBadge'
import { AgentAssetPanel } from '@/components/cmdb/AgentAssetPanel'
import { ToolAssetPanel } from '@/components/cmdb/ToolAssetPanel'
import { EndpointDrawer } from '@/pages/discover/DiscoverPage'
import { useEndpoints } from '@/hooks/useEndpoints'
import type { EndpointWithWorkspace } from '@/hooks/useEndpoints'
import { useAgents } from '@/hooks/useAgents'
import type { AgentWithWorkspace } from '@/hooks/useAgents'
import { useTools } from '@/hooks/useTools'
import type { ToolWithActions } from '@/hooks/useTools'

// AssetsPage -- B-196 increment 1: a real CMDB list, built only on what
// this brief's own Part A/B/C investigation confirmed is genuinely real
// today (BACKLOG.md's B-196 entry) -- endpoints + gateway_agents +
// gateway_tools, static category tags (not the admin-configurable
// classification panel DESIGN_SYSTEM.md §7.2 describes -- that's real,
// separate, unbuilt future work per Part C), real workspace badges where
// the schema actually supports it (endpoints/agents), and an honest
// "Not workspace-scoped" label where it structurally doesn't
// (gateway_tools -- see AssetWorkspaceBadge's own doc comment for why
// that's a distinct state, not the same "Global floor" gray).
//
// Explicitly NOT here, disclosed not silently dropped (BACKLOG.md B-196):
// the AI Workload CI category (blocked on B-151/B-147, no real backend
// yet), true multi-hop CI relationships beyond what B-200 already proved
// (agent<->tool/policy/workflow/endpoint, reused directly below), and
// gateway_nodes (zero real rows in this deployment today, and -- like
// gateway_tools -- has no workspace_id column; out of this increment's
// named scope, B-218 logged for the schema gap).

type CIType = 'endpoint' | 'agent' | 'tool'

type AssetRow = {
  ciType: CIType
  id: string
  name: string
  category: string
  scoped: boolean
  workspaceName?: string | null
  statusValue: string
  riskTier?: string | null
  detail: string
  raw: EndpointWithWorkspace | AgentWithWorkspace | ToolWithActions
}

const CATEGORY_LABEL: Record<CIType, string> = {
  endpoint: 'End-user compute',
  agent: 'AI Agent',
  tool: 'Connector',
}

const TYPE_TABS: { key: CIType | 'all'; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'endpoint', label: 'Endpoints' },
  { key: 'agent', label: 'Agents' },
  { key: 'tool', label: 'Tools' },
]

export function AssetsPage() {
  const [typeFilter, setTypeFilter] = useState<CIType | 'all'>('all')
  const [selected, setSelected] = useState<AssetRow | null>(null)

  const { data: endpointsData, isLoading: endpointsLoading, error: endpointsError } = useEndpoints({ per_page: 200 })
  const { data: agentsData, isLoading: agentsLoading, error: agentsError } = useAgents()
  const { data: toolsData, isLoading: toolsLoading, error: toolsError } = useTools()

  const isLoading = endpointsLoading || agentsLoading || toolsLoading
  // Endpoints require the Discovery module license (router.go) --
  // distinct from a real network/server error, and the one real reason
  // this specific fetch can fail while the other two succeed. Reported
  // honestly rather than folded into one generic "failed to load" banner.
  const endpointsUnlicensed =
    !!endpointsError && (endpointsError as { code?: string })?.code === 'module_not_licensed'
  const hasOtherError = !!agentsError || !!toolsError || (!!endpointsError && !endpointsUnlicensed)

  // Code-review finding: per_page: 200 below is a real, hardcoded cap, not
  // full pagination -- this page has no "load more"/page-through affordance
  // the way DiscoverPage.tsx does. Silently dropping rows past 200 on a
  // page whose whole premise is being an honest, complete inventory would
  // be exactly the kind of "incomplete data presented as complete" this
  // session's own DESIGN_SYSTEM.md §7.4 rule exists to prevent -- so the
  // real total (endpointsData.meta.total, already returned by the existing
  // API) is compared against what actually rendered and surfaced instead
  // of just quietly dropped.
  const endpointsTotal = endpointsData?.meta?.total ?? 0
  const endpointsShown = endpointsData?.data?.length ?? 0
  const endpointsTruncated = endpointsTotal > endpointsShown

  const rows: AssetRow[] = [
    ...((endpointsData?.data ?? []) as EndpointWithWorkspace[]).map(
      (e): AssetRow => ({
        ciType: 'endpoint',
        id: e.id,
        name: e.hostname,
        category: CATEGORY_LABEL.endpoint,
        scoped: true,
        workspaceName: e.workspace_name,
        statusValue: e.os ?? '—',
        detail: e.agent_version ?? '—',
        raw: e,
      }),
    ),
    ...((agentsData?.data ?? []) as AgentWithWorkspace[]).map(
      (a): AssetRow => ({
        ciType: 'agent',
        id: a.id,
        name: a.name,
        category: CATEGORY_LABEL.agent,
        scoped: true,
        workspaceName: a.workspace_name,
        statusValue: a.status,
        riskTier: a.risk_tier,
        detail: a.model,
        raw: a,
      }),
    ),
    ...((toolsData?.data ?? []) as ToolWithActions[]).map(
      (t): AssetRow => ({
        ciType: 'tool',
        id: t.id,
        name: t.name,
        category: CATEGORY_LABEL.tool,
        scoped: false,
        statusValue: t.status,
        detail: t.type,
        raw: t,
      }),
    ),
  ]

  const filteredRows = typeFilter === 'all' ? rows : rows.filter((r) => r.ciType === typeFilter)

  const columns: Column<AssetRow>[] = [
    { key: 'name', header: 'Name', sortable: true, render: (r) => <span className="font-medium text-gray-900">{r.name}</span> },
    {
      key: 'category',
      header: 'Type',
      sortable: true,
      render: (r) => (
        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-600">
          {r.category}
        </span>
      ),
    },
    {
      key: 'statusValue',
      header: 'Status',
      render: (r) =>
        r.ciType === 'endpoint' ? (
          <span className="text-gray-500 capitalize">{r.statusValue}</span>
        ) : r.ciType === 'agent' ? (
          <div className="flex items-center gap-1.5">
            <StatusPill status={r.statusValue as 'active' | 'suspended' | 'revoked'} />
            {r.riskTier && <RiskPill tier={r.riskTier as 'low' | 'medium' | 'high' | 'critical'} />}
          </div>
        ) : (
          <StatusPill status={r.statusValue as 'connected' | 'degraded' | 'disconnected'} />
        ),
    },
    { key: 'detail', header: 'Detail', render: (r) => <span className="text-xs text-gray-500">{r.detail}</span> },
    {
      key: 'workspace',
      header: 'Workspace',
      render: (r) => <AssetWorkspaceBadge scoped={r.scoped} workspaceName={r.workspaceName} />,
    },
  ]

  return (
    <div className="flex flex-col h-full">
      <AppTopBar breadcrumb={[{ label: 'Assets' }]} />
      <PageHeader subtitle="Endpoints, agents, and connectors normalized into one real asset list" />

      <div className="flex-1 overflow-auto p-6 space-y-4">
        <div className="flex items-center gap-1 border-b border-gray-200">
          {TYPE_TABS.map((tab) => (
            <button
              key={tab.key}
              onClick={() => setTypeFilter(tab.key)}
              className={`px-3 py-2 text-sm font-medium border-b-2 -mb-px ${
                typeFilter === tab.key
                  ? 'border-brand-600 text-brand-700'
                  : 'border-transparent text-gray-500 hover:text-gray-700'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {endpointsUnlicensed && (
          <p className="text-xs text-amber-700 bg-amber-50 border border-amber-200 rounded px-3 py-2">
            Endpoints aren't shown -- this organization isn't licensed for the Discovery module. Agents and tools below are unaffected.
          </p>
        )}
        {endpointsTruncated && (
          <p className="text-xs text-amber-700 bg-amber-50 border border-amber-200 rounded px-3 py-2">
            Showing {endpointsShown} of {endpointsTotal} real endpoints -- this list doesn't yet page through the rest. Agents and tools below are complete.
          </p>
        )}
        {hasOtherError && (
          <p className="text-sm text-red-600">Failed to load some real asset data. Try again.</p>
        )}

        <DataTable
          columns={columns}
          data={filteredRows}
          loading={isLoading}
          pageSize={50}
          getRowId={(r) => `${r.ciType}-${r.id}`}
          onRowClick={(r) => setSelected(r)}
          renderEmpty={() => (
            <EmptyState
              title="No assets found"
              description="Endpoints, agents, and tools discovered or configured for this org will appear here."
            />
          )}
        />
      </div>

      {selected?.ciType === 'endpoint' && (
        <EndpointDrawer endpointId={selected.id} onClose={() => setSelected(null)} />
      )}
      {selected?.ciType === 'agent' && (
        <AgentAssetPanel agent={selected.raw as AgentWithWorkspace} onClose={() => setSelected(null)} />
      )}
      {selected?.ciType === 'tool' && (
        <ToolAssetPanel tool={selected.raw as ToolWithActions} onClose={() => setSelected(null)} />
      )}
    </div>
  )
}
