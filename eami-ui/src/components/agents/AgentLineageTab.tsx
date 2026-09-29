// AgentLineageTab -- Agent Detail's Lineage tab (DESIGN_SYSTEM.md §7.7;
// roadmap Horizon 1 "Agent lineage"). What this agent has actually done,
// from data the gateway already records (audit_log, workflow_runs,
// token_usage) via GET /v1/gateway/agents/{id}/lineage. Connections stays
// the graph (shape); this tab is the numbers over the same relationships.
//
// Honest-data rules (§7.4): an agent with no history gets an explicit
// empty state below its (real, zero) summary counts; cost is "—" wherever
// no cost applies
// (not an AI-provider connector, or no AI usage ever recorded), never $0;
// unpriced usage is called out because it makes the total a floor. No data
// classification is shown: redaction records only a count, not what kind.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Activity } from 'lucide-react'
import { DataTable, EmptyState, LoadingSpinner, Button } from '@/components/common'
import type { Column } from '@/components/common'
import { ActionBadge } from '@/components/policies/PolicyBadges'
import { useAgentLineage } from '@/hooks/useAgents'
import type { AgentLineageTool, AgentLineagePolicy, AgentLineageWorkflow, LineageWindow } from '@/hooks/useAgents'

const WINDOWS: { value: LineageWindow; label: string }[] = [
  { value: '24h', label: 'Last 24 hours' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
]
const WINDOW_TEXT: Record<LineageWindow, string> = { '24h': 'last 24 hours', '7d': 'last 7 days', '30d': 'last 30 days' }

const CARD = 'rounded-[10px] border border-[rgba(228,231,240,0.55)] bg-white shadow-l1'

// FinOps' currency format, plus "<$0.01" so a real sub-cent cost never
// reads as a fabricated $0.00.
function formatUSD(v: number): string {
  if (v > 0 && v < 0.01) return '<$0.01'
  return `$${v.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

function formatTime(iso: string | null): string {
  return iso ? new Date(iso).toLocaleString() : '—'
}

// Timestamps are set in mono (DESIGN_SYSTEM.md §3).
function Time({ iso }: { iso: string | null }) {
  return <span className="font-mono text-xs text-gray-500">{formatTime(iso)}</span>
}

function Cell({ label, value, mono = false, title }: { label: string; value: string; mono?: boolean; title?: string }) {
  return (
    <div className="flex flex-col gap-[3px]">
      <span className="text-2xs font-semibold tracking-wider text-ink-faint">{label}</span>
      <span className={`${mono ? 'font-mono ' : ''}truncate text-sm font-semibold text-ink`} title={title ?? value}>{value}</span>
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-semibold text-ink">{title}</h3>
      <div className={`${CARD} overflow-hidden`}>{children}</div>
    </section>
  )
}

export function AgentLineageTab({ agentId }: { agentId: string }) {
  const [lineageWindow, setLineageWindow] = useState<LineageWindow>('7d')
  const { data, isLoading, isError, refetch, isFetching, isPlaceholderData } = useAgentLineage(agentId, lineageWindow)

  const picker = (
    <label className="flex items-center gap-2 text-sm text-gray-600">
      <span>Activity window</span>
      {isPlaceholderData && <LoadingSpinner size="sm" />}
      <select
        value={lineageWindow}
        onChange={(e) => setLineageWindow(e.target.value as LineageWindow)}
        className="border border-gray-300 rounded px-2 py-1 text-sm focus:outline-none focus:ring-1 focus:ring-brand-500"
      >
        {WINDOWS.map((w) => <option key={w.value} value={w.value}>{w.label}</option>)}
      </select>
    </label>
  )

  if (isLoading) {
    return <div className={`${CARD} flex h-[220px] items-center justify-center`}><LoadingSpinner /></div>
  }
  if (isError || !data) {
    return (
      <div className={`${CARD} flex h-[220px] flex-col items-center justify-center gap-3`}>
        <EmptyState title="Failed to load lineage" description="The activity record for this agent could not be loaded." />
        <Button variant="outline" size="sm" isLoading={isFetching} onClick={() => refetch()}>Retry</Button>
      </div>
    )
  }

  const s = data.summary
  // Empty only when nothing at all is recorded: no audit rows, no workflow
  // runs and no token usage (usage can arrive without audit rows).
  const neverActive = s.last_seen == null && data.workflows.length === 0 && s.cost_usd_window == null
  const windowText = WINDOW_TEXT[data.window]
  const unpriced = s.unpriced_calls_window

  const toolColumns: Column<AgentLineageTool>[] = [
    {
      key: 'tool_name', header: 'Tool',
      render: (t) => (
        <span className="font-medium text-gray-900">
          {t.tool_name || <span className="italic text-gray-400">unnamed</span>}
          {t.tool_id == null && <span className="ml-2 text-xs font-normal text-gray-400">connector no longer exists</span>}
        </span>
      ),
    },
    { key: 'calls', header: 'Calls', className: 'text-right', render: (t) => <span className="tabular-nums">{t.calls}</span> },
    { key: 'allowed', header: 'Allowed', className: 'text-right', render: (t) => <span className="tabular-nums">{t.allowed}</span> },
    { key: 'escalated', header: 'Escalated', className: 'text-right', render: (t) => <span className="tabular-nums">{t.escalated}</span> },
    { key: 'denied', header: 'Denied', className: 'text-right', render: (t) => <span className="tabular-nums">{t.denied}</span> },
    { key: 'last_call_at', header: 'Last call', render: (t) => <Time iso={t.last_call_at} /> },
    {
      key: 'cost_usd', header: 'Cost', className: 'text-right',
      render: (t) => t.cost_usd == null
        ? <span className="text-gray-400" title="No token cost is recorded for this tool type">—</span>
        : <span className="tabular-nums">{formatUSD(t.cost_usd)}{t.unpriced_calls > 0 && <span className="ml-1 text-xs text-amber-700" title="Usage with no configured model pricing is not included">+{t.unpriced_calls} unpriced</span>}</span>,
    },
  ]
  const policyColumns: Column<AgentLineagePolicy>[] = [
    { key: 'name', header: 'Policy', render: (p) => <span className="font-medium text-gray-900">{p.name}</span> },
    { key: 'action', header: 'Action', render: (p) => <ActionBadge action={p.action} /> },
    { key: 'hits', header: 'Decisions', className: 'text-right', render: (p) => <span className="tabular-nums">{p.hits}</span> },
    { key: 'last_hit_at', header: 'Last decision', render: (p) => <Time iso={p.last_hit_at} /> },
  ]
  const workflowColumns: Column<AgentLineageWorkflow>[] = [
    { key: 'name', header: 'Workflow', render: (w) => <span className="font-medium text-gray-900">{w.name}</span> },
    { key: 'runs', header: 'Runs', className: 'text-right', render: (w) => <span className="tabular-nums">{w.runs}</span> },
    { key: 'last_run_at', header: 'Last run', render: (w) => <Time iso={w.last_run_at} /> },
  ]

  return (
    // While a new window loads, the previous window's data stays visible but
    // dimmed and marked busy, so stale numbers never pass for current ones.
    <div className={`space-y-6 transition-opacity ${isPlaceholderData ? 'opacity-50' : ''}`} aria-busy={isPlaceholderData}>
      <div className="flex items-center justify-between">
        <p className="text-xs text-gray-500">From the gateway's audit log, workflow runs and token usage. Escalations and denials always cover the last 30 days.</p>
        {picker}
      </div>

      {/* Summary: §7.6a metadata grid, 3 columns x 3 rows of real fields. */}
      <div className={`grid grid-cols-3 gap-y-5 gap-x-7 px-[26px] py-[22px] ${CARD}`}>
        <Cell label="RISK" value={s.risk_tier} />
        <Cell label="OWNER" value={s.owner} mono />
        <Cell label="TOOLS EVER TOUCHED" value={String(s.tools_ever_touched)} />
        <Cell label={`CALLS (${data.window.toUpperCase()})`} value={String(s.calls_in_window)} />
        <Cell label="ESCALATIONS (30D)" value={String(s.escalations_30d)} />
        <Cell label="DENIALS (30D)" value={String(s.denials_30d)} />
        <Cell label="FIRST SEEN" value={formatTime(s.first_seen)} />
        <Cell label="LAST SEEN" value={formatTime(s.last_seen)} />
        <Cell
          label={`COST (${data.window.toUpperCase()})`}
          // A priced total alone would read $0.00 when every call is
          // unpriced -- say so beside the amount, not only in the notice.
          value={s.cost_usd_window == null ? '—' : `${formatUSD(s.cost_usd_window)}${unpriced > 0 ? ` + ${unpriced} unpriced` : ''}`}
          title={s.cost_usd_window == null ? 'No AI usage has been recorded for this agent' : 'All token usage for this agent in the window, as FinOps counts it'}
        />
      </div>

      {unpriced > 0 && (
        <div className="rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          <span className="font-semibold">{unpriced}</span>{' '}
          call{unpriced !== 1 ? 's' : ''} in the {windowText} used a model with no configured pricing and {unpriced !== 1 ? 'are' : 'is'} not
          included in the cost above, so it may be undercounted.{' '}
          <Link to="/settings?tab=model-pricing" className="font-medium underline">Add pricing</Link>
        </div>
      )}

      {neverActive ? (
        <div className={`${CARD} flex h-[220px] items-center justify-center`}>
          <EmptyState
            icon={<Activity className="h-10 w-10" />}
            title="No recorded activity"
            description="This agent has no audit or workflow history yet. Its activity appears here once it dispatches through the gateway."
          />
        </div>
      ) : (
        <>
          {s.calls_in_window === 0 && (
            <p className="text-sm text-gray-600">No calls in the {windowText}.{s.last_seen != null && <> Last seen {formatTime(s.last_seen)}.</>}</p>
          )}
          {data.tools.length > 0 && (
            <Section title="Tools">
              <DataTable columns={toolColumns} data={data.tools} pageSize={1000} getRowId={(t) => t.tool_id ?? `name:${t.tool_name}`} />
            </Section>
          )}
          {data.policies.length > 0 && (
            <Section title="Policies that decided its calls">
              <DataTable columns={policyColumns} data={data.policies} pageSize={1000} getRowId={(p) => p.policy_id} />
            </Section>
          )}
          {data.workflows.length > 0 && (
            <Section title="Workflows">
              <DataTable columns={workflowColumns} data={data.workflows} pageSize={1000} getRowId={(w) => w.workflow_id} />
            </Section>
          )}
          <p className="text-xs text-gray-400">
            Counts cover the {windowText}; "Last" times are the most recent ever. Each call counts once, by the gateway's first decision: an
            escalated call counts as an escalation, and its later approval or rejection isn't counted again. Policies with no specific rule
            match (default decisions) aren't listed. Cost is shown only for AI-provider tools that have recorded token usage.
          </p>
        </>
      )}
    </div>
  )
}
