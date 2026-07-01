import { useNavigate } from 'react-router-dom'
import { Topbar } from '@/components/layout/Topbar'
import { MetricCard } from '@/components/common/MetricCard'
import { RiskPill } from '@/components/common/RiskPill'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { EmptyState } from '@/components/common/EmptyState'
import { useActiveSessions, usePendingApprovals, useMonthlySpend } from '@/hooks/useDashboard'
import { useAlerts } from '@/hooks/useAlerts'
import { useAudit } from '@/hooks/useAudit'
import { useEndpoints } from '@/hooks/useEndpoints'
import type { components } from '@/api/schema'

type Agent = components['schemas']['Agent']
type Alert = components['schemas']['Alert']
type AuditEntry = components['schemas']['AuditEntry']

function formatDuration(isoDate: string | null | undefined): string {
  if (!isoDate) return '—'
  const secs = Math.floor((Date.now() - new Date(isoDate).getTime()) / 1000)
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`
}

function formatTs(isoDate: string): string {
  return new Date(isoDate).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

const SEVERITY_STYLES: Record<string, string> = {
  info: 'bg-blue-100 text-blue-800',
  warning: 'bg-amber-100 text-amber-800',
  high: 'bg-orange-100 text-orange-800',
  critical: 'bg-red-100 text-red-800',
}

const DECISION_STYLES: Record<string, string> = {
  allowed: 'text-green-700',
  denied: 'text-red-600',
  escalated: 'text-amber-600',
}

export function DashboardPage() {
  const navigate = useNavigate()
  const today = new Date().toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric' })

  const { data: sessionsData, isLoading: sessionsLoading } = useActiveSessions()
  const { data: approvalsData, isLoading: approvalsLoading } = usePendingApprovals()
  const { data: spendData, isLoading: spendLoading } = useMonthlySpend()
  const { data: endpointsData } = useEndpoints({ per_page: 1 })
  const { data: alertsData, isLoading: alertsLoading } = useAlerts()
  const { data: auditData, isLoading: auditLoading } = useAudit({ per_page: 10 })

  const sessions: Agent[] = sessionsData?.data ?? []
  const pendingCount = approvalsData?.meta?.total ?? 0
  const totalSpend = spendData?.total_cost_usd
  const endpointCount = endpointsData?.meta?.total
  const alerts: Alert[] = (alertsData?.data ?? []).slice(0, 5)
  const auditEntries: AuditEntry[] = auditData?.data ?? []

  return (
    <div>
      <Topbar title="Dashboard" subtitle={today} />
      <div className="p-6 space-y-6">

        {/* KPI row */}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <MetricCard
            label="Active Sessions"
            value={sessionsLoading ? '…' : sessions.length}
            trend="polling every 5s"
            trendDir="neutral"
            loading={sessionsLoading}
          />
          <div
            className="cursor-pointer"
            onClick={() => navigate('/approvals')}
            role="button"
            tabIndex={0}
            onKeyDown={(e) => e.key === 'Enter' && navigate('/approvals')}
          >
            <MetricCard
              label="Pending Approvals"
              value={approvalsLoading ? '…' : pendingCount}
              trend={pendingCount > 0 ? 'click to review' : 'none pending'}
              trendDir={pendingCount > 0 ? 'down' : 'neutral'}
              loading={approvalsLoading}
            />
          </div>
          <MetricCard
            label="Endpoints Monitored"
            value={endpointCount ?? '—'}
            trendDir="neutral"
          />
          <MetricCard
            label="Monthly Token Spend"
            value={
              spendLoading
                ? '…'
                : totalSpend != null
                ? `$${totalSpend.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                : '—'
            }
            loading={spendLoading}
          />
        </div>

        {/* Active Sessions */}
        <section>
          <h2 className="mb-3 text-sm font-semibold text-gray-700">Active Sessions</h2>
          {sessionsLoading ? (
            <div className="flex justify-center py-8"><LoadingSpinner /></div>
          ) : sessions.length === 0 ? (
            <EmptyState title="No active sessions" />
          ) : (
            <div className="overflow-hidden rounded-lg border border-gray-200">
              <table className="min-w-full divide-y divide-gray-200 text-sm">
                <thead className="bg-gray-50">
                  <tr>
                    {['Agent', 'Model', 'Task scope', 'Active since', 'Duration', 'Risk'].map((h) => (
                      <th key={h} className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 bg-white">
                  {sessions.map((a) => (
                    <tr
                      key={a.id}
                      className="cursor-pointer hover:bg-gray-50"
                      onClick={() => navigate(`/audit?agent_name=${encodeURIComponent(a.name)}`)}
                    >
                      <td className="px-4 py-3 font-medium text-gray-900">{a.name}</td>
                      <td className="px-4 py-3 text-gray-600">{a.model}</td>
                      <td className="px-4 py-3 text-gray-600 max-w-xs truncate">{a.scope}</td>
                      <td className="px-4 py-3 text-gray-500">{a.last_seen ? formatTs(a.last_seen) : '—'}</td>
                      <td className="px-4 py-3 text-gray-500">{formatDuration(a.last_seen)}</td>
                      <td className="px-4 py-3"><RiskPill tier={a.risk_tier} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>

        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          {/* Recent Alerts */}
          <section>
            <h2 className="mb-3 text-sm font-semibold text-gray-700">Recent Alerts</h2>
            {alertsLoading ? (
              <div className="flex justify-center py-8"><LoadingSpinner /></div>
            ) : alerts.length === 0 ? (
              <EmptyState title="No alerts" description="All clear" />
            ) : (
              <div className="overflow-hidden rounded-lg border border-gray-200 bg-white divide-y divide-gray-100">
                {alerts.map((alert) => (
                  <div key={alert.id} className="flex items-start gap-3 px-4 py-3">
                    <span className={`mt-0.5 rounded-full px-2 py-0.5 text-[10px] font-bold uppercase flex-shrink-0 ${SEVERITY_STYLES[alert.severity] ?? 'bg-gray-100 text-gray-700'}`}>
                      {alert.severity}
                    </span>
                    <p className="flex-1 text-sm text-gray-700">{alert.message}</p>
                    <time className="text-xs text-gray-400 flex-shrink-0">{formatTs(alert.fired_at)}</time>
                  </div>
                ))}
              </div>
            )}
          </section>

          {/* Recent Audit Events */}
          <section>
            <h2 className="mb-3 text-sm font-semibold text-gray-700">Recent Audit Events</h2>
            {auditLoading ? (
              <div className="flex justify-center py-8"><LoadingSpinner /></div>
            ) : auditEntries.length === 0 ? (
              <EmptyState title="No audit events" />
            ) : (
              <div className="overflow-hidden rounded-lg border border-gray-200 bg-white divide-y divide-gray-100">
                {auditEntries.map((entry) => (
                  <div key={entry.id} className="flex items-center gap-3 px-4 py-2 text-sm">
                    <span className="w-24 truncate font-medium text-gray-800">{entry.agent_name}</span>
                    <span className="flex-1 truncate text-gray-500">{entry.tool_name} · {entry.action}</span>
                    <span className={`text-xs font-semibold capitalize ${DECISION_STYLES[entry.decision] ?? 'text-gray-600'}`}>
                      {entry.decision}
                    </span>
                    <time className="text-xs text-gray-400 flex-shrink-0">{formatTs(entry.timestamp)}</time>
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>

      </div>
    </div>
  )
}
