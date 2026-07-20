// MemoryPage.tsx — Episode library
// Owned by FE-Ops
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Search, ChevronDown, ChevronRight, CheckCircle, XCircle, AlertTriangle, Clock } from 'lucide-react'
import { PageHeader, LoadingSpinner, EmptyState } from '@/components/common'
import { apiFetch } from '@/api/client'

// ── Types ──────────────────────────────────────────────────────────────────────

interface EpisodeStep {
  tool_name: string
  action: string
  params?: Record<string, unknown>
  result?: unknown
  decision: string
  timestamp: string
}

interface Episode {
  id: string
  org_id: string
  agent_id?: string
  agent_name: string
  task: string
  steps: EpisodeStep[]
  outcome: string
  token_total: number
  approved_by?: string
  created_at: string
}

interface EpisodeListResponse {
  data: Episode[]
  meta: { total: number; page: number; per_page: number }
}

// ── Outcome badge ─────────────────────────────────────────────────────────────

const OUTCOME_CONFIG = {
  success: { cls: 'bg-green-100 text-green-800',  Icon: CheckCircle,    label: 'Success'  },
  blocked: { cls: 'bg-red-100 text-red-800',       Icon: XCircle,        label: 'Blocked'  },
  failed:  { cls: 'bg-gray-100 text-gray-700',     Icon: AlertTriangle,  label: 'Failed'   },
  partial: { cls: 'bg-amber-100 text-amber-800',   Icon: Clock,          label: 'Partial'  },
}

function OutcomeBadge({ outcome }: { outcome: string }) {
  const cfg = OUTCOME_CONFIG[outcome as keyof typeof OUTCOME_CONFIG]
  if (!cfg) return <span className="text-xs text-gray-400">{outcome}</span>
  const { Icon, cls, label } = cfg
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium ${cls}`}>
      <Icon className="h-3 w-3" />
      {label}
    </span>
  )
}

// ── Decision badge for steps ──────────────────────────────────────────────────

function DecisionDot({ decision }: { decision: string }) {
  const cls =
    decision === 'allowed'   ? 'bg-green-400' :
    decision === 'blocked'   ? 'bg-red-400'   :
    decision === 'escalated' ? 'bg-amber-400' : 'bg-gray-400'
  return <span className={`inline-block w-2 h-2 rounded-full ${cls} mt-1.5 flex-shrink-0`} />
}

// ── Episode row (expandable) ──────────────────────────────────────────────────

function EpisodeRow({ ep }: { ep: Episode }) {
  const [expanded, setExpanded] = useState(false)
  const ts = new Date(ep.created_at).toLocaleString()

  return (
    <div className="border border-gray-200 rounded-lg overflow-hidden">
      <button
        className="w-full text-left px-4 py-3 flex items-center gap-3 hover:bg-gray-50 transition-colors"
        onClick={() => setExpanded(v => !v)}
      >
        {expanded
          ? <ChevronDown className="h-4 w-4 text-gray-400 flex-shrink-0" />
          : <ChevronRight className="h-4 w-4 text-gray-400 flex-shrink-0" />
        }
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-mono text-sm font-medium text-gray-900 truncate">{ep.task}</span>
            <OutcomeBadge outcome={ep.outcome} />
          </div>
          <div className="text-xs text-gray-500 mt-0.5">
            {ep.agent_name} · {ep.steps.length} step{ep.steps.length !== 1 ? 's' : ''} · {ts}
          </div>
        </div>
        {ep.token_total > 0 && (
          <span className="text-xs text-gray-400 flex-shrink-0">{ep.token_total} tok</span>
        )}
      </button>

      {expanded && (
        <div className="border-t border-gray-100 bg-gray-50 px-4 py-3 space-y-3">
          {ep.steps.length === 0 ? (
            <p className="text-xs text-gray-400">No steps recorded.</p>
          ) : (
            ep.steps.map((step, i) => (
              <div key={i} className="flex gap-3 text-xs">
                <DecisionDot decision={step.decision} />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-mono font-medium text-gray-800">
                      {step.tool_name}/{step.action}
                    </span>
                    <span className="text-gray-400 capitalize">{step.decision}</span>
                  </div>
                  {step.params && Object.keys(step.params).length > 0 && (
                    <pre className="mt-1 text-gray-600 whitespace-pre-wrap break-all">
                      {JSON.stringify(step.params, null, 2)}
                    </pre>
                  )}
                </div>
              </div>
            ))
          )}
          {ep.approved_by && (
            <p className="text-xs text-gray-500">Approved by: {ep.approved_by}</p>
          )}
        </div>
      )}
    </div>
  )
}

// ── Hooks ─────────────────────────────────────────────────────────────────────

function useEpisodes(outcome: string, page: number) {
  return useQuery<EpisodeListResponse>({
    queryKey: ['episodes', outcome, page],
    queryFn: () => {
      const params = new URLSearchParams({ page: String(page), per_page: '25' })
      if (outcome) params.set('outcome', outcome)
      return apiFetch(`/v1/memory/episodes?${params}`)
    },
  })
}

function useEpisodeSearch(q: string) {
  return useQuery<{ data: Episode[] }>({
    queryKey: ['episodes-search', q],
    queryFn: () => apiFetch(`/v1/memory/episodes/search?q=${encodeURIComponent(q)}`),
    enabled: q.length >= 2,
  })
}

// ── Page ─────────────────────────────────────────────────────────────────────

const OUTCOME_FILTERS = [
  { value: '',         label: 'All'      },
  { value: 'success',  label: 'Success'  },
  { value: 'blocked',  label: 'Blocked'  },
  { value: 'failed',   label: 'Failed'   },
  { value: 'partial',  label: 'Partial'  },
]

export function MemoryPage() {
  const [searchText, setSearchText] = useState('')
  const [outcomeFilter, setOutcomeFilter] = useState('')
  const [page, setPage] = useState(1)

  const isSearching = searchText.length >= 2
  const listQuery   = useEpisodes(outcomeFilter, page)
  const searchQuery = useEpisodeSearch(searchText)

  const activeQuery = isSearching ? searchQuery : listQuery
  const episodes: Episode[] = isSearching
    ? (searchQuery.data?.data ?? [])
    : (listQuery.data?.data ?? [])
  const total = isSearching ? episodes.length : (listQuery.data?.meta.total ?? 0)

  return (
    <div className="p-6 space-y-4">
      <PageHeader
        title="Memory"
        subtitle="Tool call episode library — every action taken by AI agents"
      />

      {/* Controls */}
      <div className="flex flex-col sm:flex-row gap-3">
        {/* Search */}
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
          <input
            type="text"
            placeholder="Search episodes by task…"
            value={searchText}
            onChange={e => { setSearchText(e.target.value); setPage(1) }}
            className="w-full pl-9 pr-3 py-2 text-sm border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </div>

        {/* Outcome filter (hidden while searching) */}
        {!isSearching && (
          <div className="flex gap-1">
            {OUTCOME_FILTERS.map(f => (
              <button
                key={f.value}
                onClick={() => { setOutcomeFilter(f.value); setPage(1) }}
                className={`px-3 py-2 text-xs rounded-lg border transition-colors ${
                  outcomeFilter === f.value
                    ? 'bg-indigo-600 text-white border-indigo-600'
                    : 'bg-white text-gray-600 border-gray-200 hover:bg-gray-50'
                }`}
              >
                {f.label}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Results */}
      {activeQuery.isLoading ? (
        <LoadingSpinner />
      ) : activeQuery.isError ? (
        <p className="text-sm text-red-500">Failed to load episodes.</p>
      ) : episodes.length === 0 ? (
        <EmptyState
          title={isSearching ? 'No matching episodes' : 'No episodes yet'}
          description={
            isSearching
              ? 'Try a different search term.'
              : 'Episodes are recorded automatically each time an AI agent makes a tool call through the gateway.'
          }
        />
      ) : (
        <>
          <p className="text-xs text-gray-500">
            {isSearching ? `${total} result${total !== 1 ? 's' : ''}` : `${total} total`}
          </p>
          <div className="space-y-2">
            {episodes.map(ep => <EpisodeRow key={ep.id} ep={ep} />)}
          </div>

          {/* Pagination (list mode only) */}
          {!isSearching && total > 25 && (
            <div className="flex justify-between items-center pt-2">
              <button
                onClick={() => setPage(p => Math.max(1, p - 1))}
                disabled={page === 1}
                className="text-sm text-indigo-600 disabled:text-gray-300"
              >
                ← Previous
              </button>
              <span className="text-xs text-gray-500">Page {page}</span>
              <button
                onClick={() => setPage(p => p + 1)}
                disabled={page * 25 >= total}
                className="text-sm text-indigo-600 disabled:text-gray-300"
              >
                Next →
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
