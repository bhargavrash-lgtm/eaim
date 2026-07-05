// AuditPage.tsx -- Audit Log
// Owned by FE-Ops
import { useState } from 'react'
import { Search, Shield, ShieldOff, ShieldAlert } from 'lucide-react'
import { PageHeader, LoadingSpinner, EmptyState } from '@/components/common'
import { useAudit } from '@/hooks/useAudit'
import type { AuditParams } from '@/hooks/useAudit'
import type { components } from '@/api/schema'

type AuditEntry = components['schemas']['AuditEntry']

// Decision badge

const DECISION_CONFIG = {
  allowed:   { cls: 'bg-green-100 text-green-800',  Icon: Shield,      label: 'Allowed' },
  denied:    { cls: 'bg-red-100 text-red-800',       Icon: ShieldOff,   label: 'Denied' },
  escalated: { cls: 'bg-amber-100 text-amber-800',   Icon: ShieldAlert, label: 'Escalated' },
}

function DecisionBadge({ decision }: { decision: string }) {
  const cfg = DECISION_CONFIG[decision as keyof typeof DECISION_CONFIG]
  if (!cfg) return <span className="text-xs text-gray-400">{decision}</span>
  const { Icon, cls, label } = cfg
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium ${cls}`}>
      <Icon className="h-3 w-3" />
      {label}
    </span>
  )
}

// Hash cell -- truncated, click to copy

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

// Helpers

function formatTs(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

// Filter bar state

interface Filters {
  agent_name: string
  tool_name:  string
  decision:   '' | 'allowed' | 'denied' | 'escalated'
  from:       string
  to:         string
}

const EMPTY_FILTERS: Filters = { agent_name: '', tool_name: '', decision: '', from: '', to: '' }

const PAGE_SIZE = 50

// Main page

export function AuditPage() {
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS)
  const [applied, setApplied] = useState<AuditParams>({})
  const [page, setPage]       = useState(1)

  function handleApply() {
    const params: AuditParams = { page: 1, per_page: PAGE_SIZE }
    if (filters.agent_name) params.agent_name = filters.agent_name
    if (filters.tool_name)  params.tool_name  = filters.tool_name
    if (filters.decision)   params.decision   = filters.decision as AuditParams['decision']
    if (filters.from)       params.from       = filters.from
    if (filters.to)         params.to         = filters.to
    setApplied(params)
    setPage(1)
  }

  function handleReset() {
    setFilters(EMPTY_FILTERS)
    setApplied({})
    setPage(1)
  }

  function set<K extends keyof Filters>(key: K, val: Filters[K]) {
    setFilters(prev => ({ ...prev, [key]: val }))
  }

  const { data, isLoading, isFetching, error } = useAudit({ ...applied, page, per_page: PAGE_SIZE })

  const entries: AuditEntry[] = (data as any)?.data ?? []
  const total: number = (data as any)?.meta?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="Audit Log"
        subtitle={'Immutable hash-chained record of all gateway decisions' + (total > 0 ? ' -- ' + total.toLocaleString() + ' events' : '')}
      />

      {/* Filter bar */}
      <div className="border-b border-gray-200 bg-gray-50 px-6 py-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Agent</label>
            <input value={filters.agent_name} onChange={e => set('agent_name', e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleApply()}
              className="border rounded px-2 py-1.5 text-sm w-36 focus:outline-none focus:ring-2 focus:ring-indigo-500"
              placeholder="any" />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Tool</label>
            <input value={filters.tool_name} onChange={e => set('tool_name', e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleApply()}
              className="border rounded px-2 py-1.5 text-sm w-36 focus:outline-none focus:ring-2 focus:ring-indigo-500"
              placeholder="any" />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Decision</label>
            <select value={filters.decision} onChange={e => set('decision', e.target.value as Filters['decision'])}
              className="border rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
              <option value="">All</option>
              <option value="allowed">Allowed</option>
              <option value="denied">Denied</option>
              <option value="escalated">Escalated</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">From</label>
            <input type="datetime-local" value={filters.from} onChange={e => set('from', e.target.value)}
              className="border rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">To</label>
            <input type="datetime-local" value={filters.to} onChange={e => set('to', e.target.value)}
              className="border rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>
          <div className="flex gap-2">
            <button onClick={handleApply}
              className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700">
              <Search className="h-4 w-4" />
              Search
            </button>
            <button onClick={handleReset}
              className="border border-gray-300 text-gray-600 rounded px-3 py-1.5 text-sm hover:bg-white">
              Reset
            </button>
          </div>
        </div>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <div className="flex justify-center py-16"><LoadingSpinner size="lg" /></div>
        ) : error ? (
          <div className="p-6 text-sm text-red-500">Failed to load audit log.</div>
        ) : entries.length === 0 ? (
          <div className="p-6">
            <EmptyState
              title="No audit events"
              description="Gateway decisions will appear here once agents start making tool calls."
            />
          </div>
        ) : (
          <>
            <div className={isFetching ? 'opacity-60 transition-opacity' : 'transition-opacity'}>
              <table className="w-full text-sm">
                <thead className="bg-gray-50 border-b border-gray-200 sticky top-0 z-10">
                  <tr>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase whitespace-nowrap">Timestamp</th>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase">Agent</th>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase">Tool</th>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase">Action</th>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase">Decision</th>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase whitespace-nowrap">Latency</th>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase">Tokens</th>
                    <th className="px-4 py-3 text-left text-xs font-semibold text-gray-500 uppercase" title="SHA-256 chain">Hash</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 bg-white">
                  {entries.map(entry => (
                    <tr key={entry.id} className="hover:bg-gray-50">
                      <td className="px-4 py-3 text-xs text-gray-400 font-mono whitespace-nowrap">
                        {formatTs(entry.timestamp)}
                      </td>
                      <td className="px-4 py-3 text-gray-700 max-w-[140px] truncate" title={entry.agent_name}>
                        {entry.agent_name}
                      </td>
                      <td className="px-4 py-3 font-mono text-xs text-gray-600 max-w-[120px] truncate" title={entry.tool_name}>
                        {entry.tool_name}
                      </td>
                      <td className="px-4 py-3 text-gray-600 max-w-[200px] truncate" title={entry.action}>
                        {entry.action}
                      </td>
                      <td className="px-4 py-3">
                        <DecisionBadge decision={entry.decision} />
                        {entry.approved_by && (
                          <div className="text-xs text-gray-400 mt-0.5">by {entry.approved_by}</div>
                        )}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-500 font-mono whitespace-nowrap">
                        {entry.latency_ms != null ? entry.latency_ms + ' ms' : '--'}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-500 font-mono whitespace-nowrap">
                        {entry.token_in != null
                          ? <span title={'in: ' + entry.token_in + ' / out: ' + entry.token_out}>{(entry.token_in + (entry.token_out ?? 0)).toLocaleString()}</span>
                          : '--'}
                      </td>
                      <td className="px-4 py-3">
                        <HashCell hash={entry.hash} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {totalPages > 1 && (
              <div className="flex items-center justify-between border-t border-gray-200 bg-gray-50 px-6 py-3">
                <span className="text-xs text-gray-500">
                  {((page - 1) * PAGE_SIZE + 1).toLocaleString()}&#8211;{Math.min(page * PAGE_SIZE, total).toLocaleString()} of {total.toLocaleString()} events
                </span>
                <div className="flex gap-2">
                  <button disabled={page === 1 || isFetching} onClick={() => setPage(p => p - 1)}
                    className="rounded border border-gray-300 px-3 py-1 text-xs disabled:opacity-40 hover:bg-white">
                    Previous
                  </button>
                  <span className="px-2 py-1 text-xs text-gray-500">Page {page} of {totalPages}</span>
                  <button disabled={page === totalPages || isFetching} onClick={() => setPage(p => p + 1)}
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
  )
}
