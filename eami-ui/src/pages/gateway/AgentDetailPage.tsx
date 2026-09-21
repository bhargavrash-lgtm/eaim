// AgentDetailPage.tsx -- eami-ui
// B-200: DESIGN_SYSTEM.md §6/§7.1 (Layer 2 + Layer 3 of the design canvas)
// translated into real code -- a new full-page route (not a SlideOverPanel:
// the relationship graph needs ~1300px of width, physically wider than
// SlideOverPanel's fixed 480px drawer, the real constraint that settled
// this as a route rather than a panel) showing one agent's real Policy/
// Tool/Workflow/Endpoint connections (Focused Mode only, never an
// org-wide graph).
import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { MoreVertical, ShieldCheck, Wrench, Workflow as WorkflowIcon } from 'lucide-react'
import { SlideOverPanel, LoadingSpinner, EmptyState, StatusPill } from '@/components/common'
import { AppTopBar } from '@/components/layout/AppTopBar'
import { EndpointDrawer } from '@/pages/discover/DiscoverPage'
import { useAgent, useAgentConnections } from '@/hooks/useAgents'
import { usePolicy } from '@/hooks/usePolicies'
import { useWorkflow } from '@/hooks/useWorkflows'
import { useTools } from '@/hooks/useTools'
import { RelationshipGraph, type SelectedGraphNode } from './RelationshipGraph'

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

// ── Top bar ───────────────────────────────────────────────────────────────────
// B-201 Phase 2: now reuses the shared AppTopBar (components/layout/) --
// this file's own inline top bar (the original B-200 implementation) was
// extracted into that shared component verbatim, then deleted here, to
// avoid two copies of the identical code existing side by side. The
// disabled "More actions" chrome (code-review finding from B-200: no
// overflow-menu actions are in scope for this page) is preserved exactly
// as before, passed as AppTopBar's action prop -- same visual behavior,
// no silent change.
const agentDetailMoreActions = (
  <button
    className="cursor-not-allowed rounded-lg border border-gray-200 p-2 text-gray-300"
    title="More actions — not built yet"
    disabled
  >
    <MoreVertical className="h-4 w-4" />
  </button>
)

// ── Main page ─────────────────────────────────────────────────────────────────

export function AgentDetailPage() {
  const { id } = useParams<{ id: string }>()
  const { data: agent, isLoading: agentLoading, error: agentError } = useAgent(id ?? null)
  const { data: connections, isLoading: connectionsLoading, error: connectionsError } = useAgentConnections(id ?? null)
  const [selected, setSelected] = useState<SelectedGraphNode | null>(null)

  if (agentLoading) {
    return <div className="p-6 text-sm text-gray-400">Loading agent…</div>
  }
  if (agentError || !agent) {
    return <div className="p-6 text-sm text-red-500">Agent not found.</div>
  }

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <AppTopBar
        breadcrumb={[{ label: 'Agents', href: '/gateway/agents' }, { label: agent.name }]}
        action={agentDetailMoreActions}
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

        <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-ink">
          <WorkflowIcon className="h-3.5 w-3.5 text-ink-faint" />
          Connections
        </div>

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
            onSelect={setSelected}
          />
        )}

        <div className="mt-8 flex items-center gap-2 text-sm font-semibold text-ink">
          <Wrench className="h-3.5 w-3.5 text-ink-faint" />
          Agent Details
        </div>
        {/* B-204: compact grouped label-value pattern, not edge-justified
            justify-between -- this card spans the full ~1100px+ content
            width, so justify-between pushed "Owner"/"Scope" to the far
            left and their values to the far right, reading as extreme
            rather than a scannable pair. A fixed-width label column
            (w-20, enough for "Owner"/"Scope" without wrapping) + the
            value directly beside it keeps the card full-width (matching
            this page's own card language) while the actual content
            reads as one compact, left-aligned group. This is now
            DESIGN_SYSTEM.md's documented default label-value pattern. */}
        <div className="mt-3 flex flex-col gap-2">
          <div className="flex items-center gap-3 rounded-lg bg-white px-4.5 py-3.5 shadow-l1">
            <span className="w-20 flex-shrink-0 text-sm font-semibold text-ink">Owner</span>
            <span className="text-sm text-ink-faint">{agent.owner}</span>
          </div>
          <div className="flex items-center gap-3 rounded-lg bg-white px-4.5 py-3.5 shadow-l1">
            <span className="w-20 flex-shrink-0 text-sm font-semibold text-ink">Scope</span>
            <span className="max-w-md truncate text-sm text-ink-faint" title={agent.scope}>{agent.scope}</span>
          </div>
        </div>
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
      {selected?.kind === 'endpoint' && (
        <EndpointDrawer endpointId={selected.id} onClose={() => setSelected(null)} />
      )}
    </div>
  )
}
