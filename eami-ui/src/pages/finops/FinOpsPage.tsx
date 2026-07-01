import { useState, useMemo } from 'react'
import {
  BarChart, Bar, LineChart, Line, XAxis, YAxis, CartesianGrid,
  Tooltip, Legend, ResponsiveContainer,
} from 'recharts'
import { Topbar } from '@/components/layout/Topbar'
import { MetricCard } from '@/components/common/MetricCard'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { EmptyState } from '@/components/common/EmptyState'
import { useFinOpsSummary, useFinOpsTimeSeries } from '@/hooks/useFinOps'
import type { components } from '@/api/schema'

type AgentSpend = components['schemas']['AgentSpend']

// ── Date helpers ──────────────────────────────────────────────────────────────

function isoDate(d: Date): string {
  return d.toISOString().split('T')[0]
}

function monthStart(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`
}

function prevMonthRange(from: string, to: string): { from: string; to: string } {
  const f = new Date(from)
  const t = new Date(to)
  const days = Math.round((t.getTime() - f.getTime()) / 86_400_000)
  const prevTo = new Date(f.getTime() - 86_400_000)
  const prevFrom = new Date(prevTo.getTime() - days * 86_400_000)
  return { from: isoDate(prevFrom), to: isoDate(prevTo) }
}

function formatUsd(v: number | null | undefined): string {
  if (v == null) return '—'
  return `$${v.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

function formatTokens(n: number | null | undefined): string {
  if (n == null) return '—'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`
  return String(n)
}

function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

const MODEL_COLORS = [
  '#6366f1', '#10b981', '#f59e0b', '#ef4444',
  '#8b5cf6', '#06b6d4', '#f97316', '#84cc16',
]

// ── Date range picker ─────────────────────────────────────────────────────────

interface DateRangePickerProps {
  from: string
  to: string
  onChange: (from: string, to: string) => void
}

function DateRangePicker({ from, to, onChange }: DateRangePickerProps) {
  function handleFrom(v: string) {
    const f = new Date(v)
    const t = new Date(to)
    if (t.getTime() - f.getTime() > 90 * 86_400_000) {
      const capped = new Date(f.getTime() + 90 * 86_400_000)
      onChange(v, isoDate(capped))
    } else {
      onChange(v, to)
    }
  }
  function handleTo(v: string) {
    const f = new Date(from)
    const t = new Date(v)
    if (t.getTime() - f.getTime() > 90 * 86_400_000) {
      const capped = new Date(t.getTime() - 90 * 86_400_000)
      onChange(isoDate(capped), v)
    } else {
      onChange(from, v)
    }
  }
  return (
    <div className="flex items-center gap-2 text-sm">
      <input type="date" value={from} onChange={(e) => handleFrom(e.target.value)}
        className="border border-gray-300 rounded px-2 py-1 text-sm focus:outline-none focus:ring-1 focus:ring-brand-500" />
      <span className="text-gray-400">to</span>
      <input type="date" value={to} onChange={(e) => handleTo(e.target.value)}
        max={isoDate(new Date())}
        className="border border-gray-300 rounded px-2 py-1 text-sm focus:outline-none focus:ring-1 focus:ring-brand-500" />
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export function FinOpsPage() {
  const now = new Date()
  const [from, setFrom] = useState(monthStart(now))
  const [to, setTo] = useState(isoDate(now))

  const prev = useMemo(() => prevMonthRange(from, to), [from, to])

  const { data: summary, isLoading: summaryLoading } = useFinOpsSummary(from, to)
  const { data: prevSummary } = useFinOpsSummary(prev.from, prev.to)
  const { data: timeSeries, isLoading: tsLoading } = useFinOpsTimeSeries(from, to, 'day')
  const { data: prevTimeSeries } = useFinOpsTimeSeries(prev.from, prev.to, 'day')

  // KPI values
  const totalSpend = summary?.total_cost_usd
  const prevSpend = prevSummary?.total_cost_usd
  const spendTrend = totalSpend != null && prevSpend != null && prevSpend > 0
    ? `${((totalSpend - prevSpend) / prevSpend * 100).toFixed(1)}% vs prev period`
    : undefined
  const spendTrendDir = totalSpend != null && prevSpend != null
    ? totalSpend > prevSpend ? 'up' : totalSpend < prevSpend ? 'down' : 'neutral'
    : 'neutral' as const
  const topAgent: AgentSpend | undefined = summary?.by_agent?.slice().sort(
    (a, b) => (b.cost_usd ?? 0) - (a.cost_usd ?? 0),
  )[0]
  const avgCost = summary?.avg_cost_per_outcome

  // Daily spend bar chart data — annotated with model proportions for stacking
  const models = (summary?.by_model ?? []).map((m, i) => ({
    model: m.model ?? `model-${i}`,
    cost: m.cost_usd ?? 0,
    color: MODEL_COLORS[i % MODEL_COLORS.length],
  }))
  const totalModelCost = models.reduce((s, m) => s + m.cost, 0)

  const barData = useMemo(() => {
    const series = timeSeries?.series ?? []
    return series.map((pt) => {
      const row: Record<string, string | number> = { date: pt.timestamp ? shortDate(pt.timestamp) : "" }
      // Distribute day's cost across models proportionally from summary
      models.forEach((m) => {
        const fraction = totalModelCost > 0 ? m.cost / totalModelCost : 1 / Math.max(models.length, 1)
        row[m.model] = Number(((pt.cost_usd ?? 0) * fraction).toFixed(4))
      })
      if (models.length === 0) row['Total'] = pt.cost_usd ?? 0
      return row
    })
  }, [timeSeries, models, totalModelCost])

  // Cumulative line chart data
  const cumulativeData = useMemo(() => {
    const curr = timeSeries?.series ?? []
    const prevArr = prevTimeSeries?.series ?? []
    const len = Math.max(curr.length, prevArr.length)
    let cumCurr = 0
    let cumPrev = 0
    return Array.from({ length: len }, (_, i) => {
      cumCurr += curr[i]?.cost_usd ?? 0
      cumPrev += prevArr[i]?.cost_usd ?? 0
      return {
        day: `Day ${i + 1}`,
        'This period': Number(cumCurr.toFixed(2)),
        'Prev period': Number(cumPrev.toFixed(2)),
      }
    })
  }, [timeSeries, prevTimeSeries])

  // Agent spend table
  const agentSpend: AgentSpend[] = (summary?.by_agent ?? [])
    .slice()
    .sort((a, b) => (b.cost_usd ?? 0) - (a.cost_usd ?? 0))

  return (
    <div>
      <Topbar
        title="FinOps"
        subtitle="Token spend and ROI"
        actions={
          <DateRangePicker
            from={from}
            to={to}
            onChange={(f, t) => { setFrom(f); setTo(t) }}
          />
        }
      />
      <div className="p-6 space-y-6">

        {/* KPI row */}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <MetricCard
            label="Total Spend"
            value={summaryLoading ? '…' : formatUsd(totalSpend)}
            trend={spendTrend}
            trendDir={spendTrendDir}
            loading={summaryLoading}
          />
          <MetricCard
            label="vs Previous Period"
            value={
              prevSpend != null && totalSpend != null
                ? formatUsd(totalSpend - prevSpend)
                : '—'
            }
            trendDir={spendTrendDir}
            loading={summaryLoading}
          />
          <MetricCard
            label="Top Spending Agent"
            value={topAgent?.agent_name ?? '—'}
            trend={topAgent ? formatUsd(topAgent.cost_usd) : undefined}
            trendDir="neutral"
            loading={summaryLoading}
          />
          <MetricCard
            label="Avg Cost / Outcome"
            value={summaryLoading ? '…' : formatUsd(avgCost)}
            loading={summaryLoading}
          />
        </div>

        {/* Charts row */}
        <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">

          {/* Daily spend bar chart */}
          <div className="rounded-lg border border-gray-200 bg-white p-4">
            <h2 className="mb-4 text-sm font-semibold text-gray-700">Daily Spend by Model</h2>
            {tsLoading ? (
              <div className="flex justify-center py-10"><LoadingSpinner /></div>
            ) : barData.length === 0 ? (
              <EmptyState title="No data for this period" />
            ) : (
              <ResponsiveContainer width="100%" height={220}>
                <BarChart data={barData} margin={{ top: 0, right: 0, left: 0, bottom: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#f3f4f6" />
                  <XAxis dataKey="date" tick={{ fontSize: 10 }} interval="preserveStartEnd" />
                  <YAxis tick={{ fontSize: 10 }} tickFormatter={(v: number) => `$${v.toFixed(2)}`} width={50} />
                  <Tooltip formatter={(v: number) => `$${v.toFixed(4)}`} />
                  <Legend wrapperStyle={{ fontSize: 11 }} />
                  {models.length > 0
                    ? models.map((m) => (
                        <Bar key={m.model} dataKey={m.model} stackId="a" fill={m.color} />
                      ))
                    : <Bar dataKey="Total" fill={MODEL_COLORS[0]} />
                  }
                </BarChart>
              </ResponsiveContainer>
            )}
          </div>

          {/* Cumulative line chart */}
          <div className="rounded-lg border border-gray-200 bg-white p-4">
            <h2 className="mb-4 text-sm font-semibold text-gray-700">Cumulative Spend: This Period vs Previous</h2>
            {tsLoading ? (
              <div className="flex justify-center py-10"><LoadingSpinner /></div>
            ) : cumulativeData.length === 0 ? (
              <EmptyState title="No data for this period" />
            ) : (
              <ResponsiveContainer width="100%" height={220}>
                <LineChart data={cumulativeData} margin={{ top: 0, right: 0, left: 0, bottom: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#f3f4f6" />
                  <XAxis dataKey="day" tick={{ fontSize: 10 }} interval="preserveStartEnd" />
                  <YAxis tick={{ fontSize: 10 }} tickFormatter={(v: number) => `$${v}`} width={50} />
                  <Tooltip formatter={(v: number) => `$${v.toFixed(2)}`} />
                  <Legend wrapperStyle={{ fontSize: 11 }} />
                  <Line type="monotone" dataKey="This period" stroke="#6366f1" strokeWidth={2} dot={false} />
                  <Line type="monotone" dataKey="Prev period" stroke="#d1d5db" strokeWidth={2} dot={false} strokeDasharray="4 2" />
                </LineChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>

        {/* Agent spend table */}
        <section>
          <h2 className="mb-3 text-sm font-semibold text-gray-700">Spend by Agent</h2>
          {summaryLoading ? (
            <div className="flex justify-center py-8"><LoadingSpinner /></div>
          ) : agentSpend.length === 0 ? (
            <EmptyState title="No spend data for this period" />
          ) : (
            <div className="overflow-hidden rounded-lg border border-gray-200">
              <table className="min-w-full divide-y divide-gray-200 text-sm">
                <thead className="bg-gray-50">
                  <tr>
                    {['Agent', 'Input tokens', 'Output tokens', 'Requests', 'Total cost', '% of total'].map((h) => (
                      <th key={h} className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 bg-white">
                  {agentSpend.map((a) => {
                    const pct = totalSpend && totalSpend > 0 && a.cost_usd != null
                      ? ((a.cost_usd / totalSpend) * 100).toFixed(1)
                      : '—'
                    return (
                      <tr key={a.agent_id ?? a.agent_name}>
                        <td className="px-4 py-3 font-medium text-gray-900">{a.agent_name}</td>
                        <td className="px-4 py-3 text-gray-600">{formatTokens(a.tokens_in)}</td>
                        <td className="px-4 py-3 text-gray-600">{formatTokens(a.tokens_out)}</td>
                        <td className="px-4 py-3 text-gray-600">{a.request_count ?? '—'}</td>
                        <td className="px-4 py-3 font-medium text-gray-900">{formatUsd(a.cost_usd)}</td>
                        <td className="px-4 py-3">
                          <div className="flex items-center gap-2">
                            <div className="h-1.5 flex-1 rounded-full bg-gray-100 max-w-20">
                              <div
                                className="h-1.5 rounded-full bg-brand-500"
                                style={{ width: `${Math.min(100, parseFloat(pct as string) || 0)}%` }}
                              />
                            </div>
                            <span className="text-gray-500 text-xs">{pct}%</span>
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </section>

      </div>
    </div>
  )
}
