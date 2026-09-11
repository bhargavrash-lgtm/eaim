// PasteEventsPage.tsx -- Paste Detection (B-038)
// Read-only admin view over B-032's paste_events: captured browser-paste
// events into known AI-tool web UIs (destination domain, timestamp, and
// coarse length/hash indicators only -- there is no raw pasted content
// anywhere in this table to display).
import { useMemo, useState } from 'react'
import {
  BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer,
} from 'recharts'
import { Topbar } from '@/components/layout/Topbar'
import { PageHeader } from '@/components/common/PageHeader'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { EmptyState } from '@/components/common/EmptyState'
import { Card } from '@/components/common/Card'
import { DataTable } from '@/components/common/DataTable'
import type { Column } from '@/components/common/DataTable'
import { Copy } from 'lucide-react'
import { usePasteEvents, usePasteEventsTimeSeries } from '@/hooks/usePasteEvents'
import type { PasteEvent } from '@/hooks/usePasteEvents'
import { CHART_PALETTE } from '@/lib/chartPalette'

// Same 6-domain list as eami-api's KnownPasteDestinations / the browser
// extension's domains.js -- bundled, not fetched, matching this codebase's
// established convention for every domain allowlist (see paste_domains.go's
// own comment for why this is a separate list per module, not shared code).
const KNOWN_DOMAINS = [
  'chat.openai.com',
  'claude.ai',
  'copilot.microsoft.com',
  'gemini.google.com',
  'perplexity.ai',
  'poe.com',
]

// 5 of these 6 come from the shared CHART_PALETTE (src/lib/chartPalette.ts);
// copilot.microsoft.com's sky blue (#0ea5e9) is not part of that shared
// array -- it's a genuinely unique assignment, not a duplicate, so it
// stays a local literal here rather than being folded into the shared module.
const DOMAIN_COLORS: Record<string, string> = {
  'chat.openai.com': CHART_PALETTE[1],
  'claude.ai': CHART_PALETTE[0],
  'copilot.microsoft.com': '#0ea5e9',
  'gemini.google.com': CHART_PALETTE[2],
  'perplexity.ai': CHART_PALETTE[4],
  'poe.com': CHART_PALETTE[3],
}
const FALLBACK_COLORS = CHART_PALETTE.slice(5)

const PAGE_SIZE = 50

function isoDate(d: Date): string {
  return d.toISOString().split('T')[0]
}

function formatTs(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

function formatBytes(n: number | null | undefined): string {
  if (n == null) return '—'
  if (n >= 1_048_576) return `${(n / 1_048_576).toFixed(1)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${n} B`
}

// Hash cell -- truncated, click to copy. Mirrors AuditPage.tsx's HashCell.
function HashCell({ hash }: { hash: string }) {
  const [copied, setCopied] = useState(false)
  function handleCopy() {
    navigator.clipboard.writeText(hash).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }
  return (
    <button onClick={handleCopy} title={copied ? 'Copied!' : hash}
      className="font-mono text-xs text-gray-400 hover:text-indigo-600">
      {hash.slice(0, 8)}...
    </button>
  )
}

interface Filters {
  domain: string
  from: string
  to: string
}

function defaultFilters(): Filters {
  const now = new Date()
  const thirtyDaysAgo = new Date(now.getTime() - 30 * 86_400_000)
  return { domain: '', from: isoDate(thirtyDaysAgo), to: isoDate(now) }
}

export function PasteEventsPage() {
  const [filters, setFilters] = useState<Filters>(defaultFilters())
  const [page, setPage] = useState(1)

  function set<K extends keyof Filters>(key: K, val: Filters[K]) {
    setFilters((prev) => ({ ...prev, [key]: val }))
    setPage(1)
  }

  // List: from/to as RFC3339, covering the whole selected day range.
  const fromISO = filters.from ? new Date(filters.from + 'T00:00:00Z').toISOString() : undefined
  const toISO = filters.to ? new Date(filters.to + 'T23:59:59Z').toISOString() : undefined

  const { data, isLoading, isFetching, error } = usePasteEvents({
    domain: filters.domain || undefined,
    from: fromISO,
    to: toISO,
    page,
    per_page: PAGE_SIZE,
  })

  const { data: timeSeries, isLoading: tsLoading } = usePasteEventsTimeSeries(
    filters.from, filters.to, 'day', filters.domain || undefined,
  )

  const events: PasteEvent[] = data?.data ?? []
  const total: number = data?.meta?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const pasteEventColumns: Column<PasteEvent>[] = [
    { key: 'occurred_at', header: 'Timestamp', render: (e) => <span className="text-xs text-gray-400 font-mono whitespace-nowrap">{formatTs(e.occurred_at)}</span> },
    { key: 'destination_domain', header: 'Domain', render: (e) => <span className="font-medium text-gray-900">{e.destination_domain}</span> },
    { key: 'content_length', header: 'Length', render: (e) => <span className="text-gray-600">{formatBytes(e.content_length)}</span> },
    { key: 'content_hash', header: 'Hash', render: (e) => e.content_hash ? <HashCell hash={e.content_hash} /> : <span className="text-gray-300">—</span> },
    { key: 'os_username', header: 'OS user', render: (e) => <span className="text-gray-500">{e.os_username ?? '—'}</span> },
  ]

  // Pivot the {bucket, domain, count} series into one row per bucket, one
  // column per domain, for a stacked bar chart -- real counts straight
  // from the query, not an estimated/distributed proxy (see
  // PasteEventsTimeSeries's own comment for why this differs from
  // FinOpsTimeSeries's per-model approximation).
  const domainsInSeries = useMemo(() => {
    const set = new Set<string>()
    for (const pt of timeSeries?.series ?? []) set.add(pt.domain)
    return Array.from(set)
  }, [timeSeries])

  const chartData = useMemo(() => {
    const byBucket = new Map<string, Record<string, string | number>>()
    for (const pt of timeSeries?.series ?? []) {
      const row = byBucket.get(pt.bucket) ?? { date: shortDate(pt.bucket) }
      row[pt.domain] = pt.count
      byBucket.set(pt.bucket, row)
    }
    return Array.from(byBucket.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([, row]) => row)
  }, [timeSeries])

  return (
    <div>
      <Topbar title="Paste Detection" subtitle="Shadow AI activity -- captured pastes into known AI tools" />
      <div className="p-6 space-y-6">
        <PageHeader
          title="Captured Paste Events"
          subtitle={total > 0 ? `${total.toLocaleString()} events in range` : undefined}
        />

        {/* Filter bar */}
        <div className="flex flex-wrap items-end gap-3 rounded-lg border border-gray-200 bg-gray-50 px-4 py-3">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Domain</label>
            <select value={filters.domain} onChange={(e) => set('domain', e.target.value)}
              className="border rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-brand-500">
              <option value="">All domains</option>
              {KNOWN_DOMAINS.map((d) => <option key={d} value={d}>{d}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">From</label>
            <input type="date" value={filters.from} onChange={(e) => set('from', e.target.value)}
              max={filters.to}
              className="border rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-brand-500" />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">To</label>
            <input type="date" value={filters.to} onChange={(e) => set('to', e.target.value)}
              min={filters.from} max={isoDate(new Date())}
              className="border rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-brand-500" />
          </div>
          <button onClick={() => setFilters(defaultFilters())}
            className="border border-gray-300 text-gray-600 rounded px-3 py-1.5 text-sm hover:bg-white">
            Reset
          </button>
        </div>

        {/* Aggregation view -- AC2: counts by domain over time */}
        <Card className="rounded-lg border-gray-200 p-4">
          <h2 className="mb-4 text-sm font-semibold text-gray-700">Events by Domain, Over Time</h2>
          {tsLoading ? (
            <div className="flex justify-center py-10"><LoadingSpinner /></div>
          ) : chartData.length === 0 ? (
            <EmptyState title="No paste events in this range" />
          ) : (
            <ResponsiveContainer width="100%" height={240}>
              <BarChart data={chartData} margin={{ top: 0, right: 0, left: 0, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#f3f4f6" />
                <XAxis dataKey="date" tick={{ fontSize: 10 }} interval="preserveStartEnd" />
                <YAxis tick={{ fontSize: 10 }} width={40} allowDecimals={false} />
                <Tooltip />
                <Legend wrapperStyle={{ fontSize: 11 }} />
                {domainsInSeries.map((domain, i) => (
                  <Bar key={domain} dataKey={domain} stackId="a"
                    fill={DOMAIN_COLORS[domain] ?? FALLBACK_COLORS[i % FALLBACK_COLORS.length]} />
                ))}
              </BarChart>
            </ResponsiveContainer>
          )}
        </Card>

        {/* Event table -- AC1: filterable list */}
        <div>
          {isLoading ? (
            <div className="flex justify-center py-16"><LoadingSpinner size="lg" /></div>
          ) : error ? (
            <div className="p-6 text-sm text-red-500">Failed to load paste events.</div>
          ) : events.length === 0 ? (
            <EmptyState
              icon={<Copy className="h-10 w-10" />}
              title="No paste events detected yet"
              description="Events appear here once the browser extension is installed and users paste into a known AI tool."
            />
          ) : (
            <>
              <DataTable
                columns={pasteEventColumns}
                data={events}
                loading={isFetching}
                pageSize={PAGE_SIZE}
                getRowId={(e) => e.id}
              />

              {totalPages > 1 && (
                <div className="flex items-center justify-between border-t border-gray-200 bg-gray-50 px-4 py-3 rounded-b-lg">
                  <span className="text-xs text-gray-500">
                    {((page - 1) * PAGE_SIZE + 1).toLocaleString()}&#8211;{Math.min(page * PAGE_SIZE, total).toLocaleString()} of {total.toLocaleString()} events
                  </span>
                  <div className="flex gap-2">
                    <button disabled={page === 1 || isFetching} onClick={() => setPage((p) => p - 1)}
                      className="rounded border border-gray-300 px-3 py-1 text-xs disabled:opacity-40 hover:bg-white">
                      Previous
                    </button>
                    <span className="px-2 py-1 text-xs text-gray-500">Page {page} of {totalPages}</span>
                    <button disabled={page === totalPages || isFetching} onClick={() => setPage((p) => p + 1)}
                      className="rounded border border-gray-300 px-3 py-1 text-xs disabled:opacity-40 hover:bg-white">
                      Next
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
