// AgentDetailPage.tsx -- eami-ui
// B-200: DESIGN_SYSTEM.md §6/§7.1 (Layer 2 + Layer 3 of the design canvas)
// translated into real code -- a new full-page route (not a SlideOverPanel:
// the relationship graph needs ~1300px of width, physically wider than
// SlideOverPanel's fixed 480px drawer, the real constraint that settled
// this as a route rather than a panel) showing one agent's real Policy/
// Tool/Workflow/Endpoint connections (Focused Mode only, never an
// org-wide graph).
import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ShieldCheck } from 'lucide-react'
import { SlideOverPanel, LoadingSpinner, EmptyState, StatusPill } from '@/components/common'
import { AppTopBar } from '@/components/layout/AppTopBar'
import { useAgent, useAgentConnections } from '@/hooks/useAgents'
import { can, useOrgRole } from '@/lib/rbac'
import { usePolicy } from '@/hooks/usePolicies'
import { useWorkflow } from '@/hooks/useWorkflows'
import { useTools } from '@/hooks/useTools'
import { RelationshipGraph, type SelectedGraphNode } from './RelationshipGraph'
import { AgentActionsTab } from '@/components/agents/AgentActionsTab'
import { AgentLineageTab } from '@/components/agents/AgentLineageTab'
import { AgentClassificationTab } from '@/components/agents/AgentClassificationTab'

// ── Read-only detail panels ──────────────────────────────────────────────────
// Each wraps the existing SlideOverPanel shell (B-178) -- per this brief's
// own instruction not to invent a second detail-view pattern -- with new,
// small, read-only content. Neither PoliciesPage/WorkflowsPage has a pure
// "view" panel today (only edit forms), and ToolsPage has no single-tool
// fetch at all -- Tool detail deliberately looks up from the already-
// fetched useTools() list instead of adding a new backend endpoint,
// mirroring EditToolPanel's own established B-045 precedent exactly.

function PolicyDetailPanel({ policyId, onClose }: { policyId: string; onClose: () => void }) {
  const { data: policy, isLoading } = usePolicy(policyId)
  return (
    <SlideOverPanel onClose={onClose}>
      <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4">
        <h2 className="text-base font-semibold text-ink">{policy?.name ?? 'Policy'}</h2>
        <button onClick={onClose} className="rounded p-1 text-gray-400 hover:bg-gray-100">&times;</button>
      </div>
      <div className="flex-1 overflow-y-auto p-5">
        {isLoading ? (
          <LoadingSpinner />
        ) : !policy ? (
          <EmptyState title="Policy not found" />
        ) : (
          <div className="space-y-3 text-sm">
            <Field label="Action" value={policy.action} />
            <Field label="Status" value={policy.status} />
            <Field label="Priority" value={String(policy.priority)} />
            {policy.description && <Field label="Description" value={policy.description} />}
            <p className="pt-2 text-2xs text-ink-faint">
              This policy has actually applied to at least one real dispatch decision for this agent (per the audit log) -- not every policy whose conditions could theoretically match it.
            </p>
          </div>
        )}
      </div>
    </SlideOverPanel>
  )
}

function WorkflowDetailPanel({ workflowId, onClose }: { workflowId: string; onClose: () => void }) {
  const { data: workflow, isLoading } = useWorkflow(workflowId)
  return (
    <SlideOverPanel onClose={onClose}>
      <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4">
        <h2 className="text-base font-semibold text-ink">{workflow?.name ?? 'Workflow'}</h2>
        <button onClick={onClose} className="rounded p-1 text-gray-400 hover:bg-gray-100">&times;</button>
      </div>
      <div className="flex-1 overflow-y-auto p-5">
        {isLoading ? (
          <LoadingSpinner />
        ) : !workflow ? (
          <EmptyState title="Workflow not found" />
        ) : (
          <div className="space-y-3 text-sm">
            <Field label="Status" value={workflow.status} />
            <Field label="Steps" value={String(workflow.steps?.length ?? workflow.step_count ?? 0)} />
            {(workflow.steps ?? []).map((step, i) => (
              <div key={step.id} className="rounded border border-gray-200 px-3 py-2 text-xs">
                <span className="font-mono text-ink-faint">#{i + 1}</span>{' '}
                <span className="font-semibold text-ink">{step.tool_name || 'connector removed'}</span>{' '}
                <span className="text-gray-500">{step.action}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </SlideOverPanel>
  )
}

function ToolDetailPanel({ toolId, onClose }: { toolId: string; onClose: () => void }) {
  const { data, isLoading } = useTools()
  const tool = ((data as any)?.data ?? []).find((t: any) => t.id === toolId)
  return (
    <SlideOverPanel onClose={onClose}>
      <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4">
        <h2 className="text-base font-semibold text-ink">{tool?.name ?? 'Tool'}</h2>
        <button onClick={onClose} className="rounded p-1 text-gray-400 hover:bg-gray-100">&times;</button>
      </div>
      <div className="flex-1 overflow-y-auto p-5">
        {/* Code-review finding: previously skipped isLoading entirely, so
            a cold useTools() cache (e.g. this agent's detail page opened
            as a deep link, before the list has ever been fetched this
            session) showed "Tool not found" for a real tool, briefly. */}
        {isLoading ? (
          <LoadingSpinner />
        ) : !tool ? (
          <EmptyState title="Tool not found" />
        ) : (
          <div className="space-y-3 text-sm">
            <Field label="Type" value={tool.type} />
            <Field label="Status" value={tool.status} />
            {tool.base_url && <Field label="Base URL" value={tool.base_url} />}
            {tool.last_used && <Field label="Last used" value={new Date(tool.last_used).toLocaleString()} />}
          </div>
        )}
      </div>
    </SlideOverPanel>
  )
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-2xs font-semibold uppercase tracking-wide text-ink-faint">{label}</div>
      <div className="mt-0.5 text-ink">{value}</div>
    </div>
  )
}

// ── Tabs (DESIGN_SYSTEM.md §7.7) ────────────────────────────────────────────
// Overview and Connections were sections of one page (B-200/B-204/B-206);
// Actions is the first real tab. The disabled "More actions -- not built
// yet" top-bar button was removed: this tab is the real home for those
// actions. The active tab lives in ?tab= (SettingsPage's pattern), so a tab
// is deep-linkable and survives reload. Each tab renders only for roles
// whose API reads it needs (B-252 C0): approvers get Overview only.
const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'connections', label: 'Connections' },
  { id: 'lineage', label: 'Lineage' },
  { id: 'classification', label: 'Classification' },
  { id: 'actions', label: 'Actions' },
] as const
type TabId = (typeof TABS)[number]['id']
const TAB_VISIBLE: Record<TabId, (role: string | undefined) => boolean> = {
  overview: () => true,
  connections: can.viewAgentConnections,
  lineage: can.viewAgentLineage,
  classification: can.viewCMDB,
  actions: can.viewAgentActionsTab,
}

// ── Main page ─────────────────────────────────────────────────────────────────

export function AgentDetailPage() {
  const { id } = useParams<{ id: string }>()
  const { data: agent, isLoading: agentLoading, error: agentError } = useAgent(id ?? null)
  const role = useOrgRole()
  const tabs = TABS.filter((t) => TAB_VISIBLE[t.id](role))
  // A null id disables the query: roles without the connections read
  // (approver) never request it.
  const { data: connections, isLoading: connectionsLoading, error: connectionsError } = useAgentConnections(can.viewAgentConnections(role) ? id ?? null : null)
  const [selected, setSelected] = useState<SelectedGraphNode | null>(null)
  const navigate = useNavigate()
  // B-252 C2: an endpoint node opens its Endpoint Detail page (the drawer is
  // retired); policy, workflow and tool nodes keep their panels for now.
  function selectNode(node: SelectedGraphNode) {
    if (node.kind === 'endpoint') navigate(`/assets/endpoints/${node.id}`)
    else setSelected(node)
  }
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const activeTab: TabId = tabs.some((t) => t.id === tabParam) ? (tabParam as TabId) : 'overview'
  const hiddenTabParam = tabParam != null && TABS.some((t) => t.id === tabParam) && !tabs.some((t) => t.id === tabParam)
  useEffect(() => {
    if (hiddenTabParam) setSearchParams({ tab: 'overview' }, { replace: true })
  }, [hiddenTabParam, setSearchParams])
  function setTab(id: TabId) {
    setSearchParams({ tab: id }, { replace: true })
  }
  // Left/Right arrow keys move between tabs (WAI-ARIA tabs pattern).
  function onTabKeyDown(e: React.KeyboardEvent) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
    const i = tabs.findIndex((t) => t.id === activeTab)
    const next = tabs[(i + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length]
    setTab(next.id)
    document.getElementById(`agent-tab-${next.id}`)?.focus()
  }

  if (agentLoading) {
    return <div className="p-6 text-sm text-gray-400">Loading agent…</div>
  }
  if (agentError || !agent) {
    return <div className="p-6 text-sm text-red-500">Agent not found.</div>
  }

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <AppTopBar
        // B-252 C1: Agent Detail's canonical home is /assets/agents/:id, so
        // the breadcrumb is Assets -- except for roles that can't read the
        // CMDB (approvers), who keep "Agents" until C9 retires that list.
        breadcrumb={[can.viewCMDB(role) ? { label: 'Assets', href: '/assets' } : { label: 'Agents', href: '/gateway/agents' }, { label: agent.name }]}
      />
      <div className="flex-1 overflow-y-auto px-10 py-8">
        <div className="mb-6 flex items-center justify-between">
          <div className="flex items-center gap-3.5">
            <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-[10px] bg-brand-50">
              <ShieldCheck className="h-5 w-5 text-brand-600" />
            </div>
            <div>
              {/* h2, not h1 (Phase 2 revision): AppTopBar's breadcrumb now
                  provides this page's real <h1> above -- this is a
                  subordinate section heading for the same name, correct
                  HTML outline (one h1, nested h2s), not a duplicate h1. */}
              <h2 className="text-xl font-bold text-ink">{agent.name}</h2>
              <div className="font-mono text-2xs text-ink-faint">{agent.model} · risk {agent.risk_tier}</div>
            </div>
          </div>
          {/* Code-review finding: was hardcoded to the success/green style
              regardless of real status -- StatusPill (already the shared,
              correct component) maps active/suspended/revoked to
              success/warning/danger on its own. */}
          <StatusPill status={agent.status} />
        </div>

        <div className="mb-6 border-b border-gray-200">
          <nav className="-mb-px flex gap-6" role="tablist" aria-label="Agent sections" onKeyDown={onTabKeyDown}>
            {tabs.map((tab) => (
              <button
                key={tab.id}
                type="button"
                role="tab"
                id={`agent-tab-${tab.id}`}
                aria-controls={`agent-panel-${tab.id}`}
                aria-selected={activeTab === tab.id}
                tabIndex={activeTab === tab.id ? 0 : -1}
                onClick={() => setTab(tab.id)}
                className={`border-b-2 py-3 text-sm font-medium transition-colors ${
                  activeTab === tab.id
                    ? 'border-brand-600 text-brand-700'
                    : 'border-transparent text-gray-500 hover:text-gray-700'
                }`}
              >
                {tab.label}
              </button>
            ))}
          </nav>
        </div>

        {activeTab === 'connections' && (<div role="tabpanel" id="agent-panel-connections" aria-labelledby="agent-tab-connections">

        {connectionsLoading ? (
          <div className="flex h-[220px] items-center justify-center rounded-xl bg-white shadow-l1">
            <LoadingSpinner />
          </div>
        ) : connectionsError || !connections ? (
          // Code-review finding: a fetch error previously looked identical
          // to "still loading" (isLoading is false but data stays
          // undefined) -- an infinite spinner with no error signal or way
          // to know the request failed.
          <div className="flex h-[220px] items-center justify-center rounded-xl bg-white shadow-l1">
            <EmptyState title="Failed to load connections" description="Try reloading the page." />
          </div>
        ) : (
          <RelationshipGraph
            agentName={agent.name}
            connections={connections}
            selected={selected}
            onSelect={selectNode}
          />
        )}
        </div>)}

        {activeTab === 'overview' && (<div role="tabpanel" id="agent-panel-overview" aria-labelledby="agent-tab-overview">
        {/* B-206: metadata grid pattern, replacing B-204's per-field
            card stack entirely -- built to the live canvas mockup
            (Layer6-MetadataGrid.dc.html) exactly: one dense card, real
            CSS grid, not several sparse justify-between cards. Solves
            alignment (a grid column locks every label to the same
            x-position automatically) and wasted space (one card, not N)
            in the same structural change, rather than the earlier
            per-field fix's narrower alignment-only scope.
            2 columns, not the mockup's illustrative repeat(3, 1fr): the
            mockup shows 6 fields to prove the pattern scales, but only
            Owner/Scope are real fields on this page today -- 3 columns
            with 2 real cells would leave one visually empty trailing
            slot, exactly the failure mode the mockup itself warns
            against. gap-y-5/gap-x-7 are exact standard Tailwind tokens
            for the mockup's 20px/28px gaps, not arbitrary values.
            rounded-[10px] matches the mockup's radius and this same
            file's own icon-chip precedent above. Label uses the
            existing text-2xs token (10px) rather than the mockup's
            literal 10.5px, per CLAUDE.md's hard micro-text-token rule;
            value uses the existing text-sm token (14px) rather than the
            mockup's literal 13px, a 1px difference judged visually
            negligible against reusing an existing scale step. */}
        <div className="grid grid-cols-2 gap-y-5 gap-x-7 rounded-[10px] border border-[rgba(228,231,240,0.55)] bg-white px-[26px] py-[22px] shadow-l1">
          <div className="flex flex-col gap-[3px]">
            <span className="text-2xs font-semibold tracking-wider text-ink-faint">OWNER</span>
            <span className="font-mono text-sm font-semibold text-ink">{agent.owner}</span>
          </div>
          <div className="flex flex-col gap-[3px]">
            <span className="text-2xs font-semibold tracking-wider text-ink-faint">SCOPE</span>
            <span className="truncate font-mono text-sm font-semibold text-ink" title={agent.scope}>{agent.scope}</span>
          </div>
        </div>
        </div>)}

        {/* Lineage (roadmap Horizon 1 "Agent lineage"): mounted only while
            open, so Agent Detail's own load makes no lineage request. */}
        {activeTab === 'lineage' && (
          <div role="tabpanel" id="agent-panel-lineage" aria-labelledby="agent-tab-lineage">
            <AgentLineageTab agentId={agent.id} />
          </div>
        )}

        {/* Classification (B-252 C1): mounted only while open. Leaving mid-save
            only loses the spinner: the mutation's cache invalidation and the
            toast still run (unlike Actions, whose inline error must survive). */}
        {activeTab === 'classification' && (
          <div role="tabpanel" id="agent-panel-classification" aria-labelledby="agent-tab-classification">
            <AgentClassificationTab agentId={agent.id} />
          </div>
        )}

        {/* Kept mounted (hidden) so an in-flight suspend/reactivate's result
            or error isn't lost if the user switches tabs mid-request. */}
        {TAB_VISIBLE.actions(role) && (
          <div role="tabpanel" id="agent-panel-actions" aria-labelledby="agent-tab-actions" hidden={activeTab !== 'actions'}>
            <AgentActionsTab agent={agent} />
          </div>
        )}
      </div>

      {selected?.kind === 'policy' && (
        <PolicyDetailPanel policyId={selected.id} onClose={() => setSelected(null)} />
      )}
      {selected?.kind === 'workflow' && (
        <WorkflowDetailPanel workflowId={selected.id} onClose={() => setSelected(null)} />
      )}
      {selected?.kind === 'tool' && (
        <ToolDetailPanel toolId={selected.id} onClose={() => setSelected(null)} />
      )}
    </div>
  )
}
