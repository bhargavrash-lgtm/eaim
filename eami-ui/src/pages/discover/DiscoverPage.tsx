import { useEffect, useState } from 'react'
import { AppTopBar } from '@/components/layout/AppTopBar'
import { PageHeader } from '@/components/common/PageHeader'
import { EmptyState } from '@/components/common/EmptyState'
import { DataTable } from '@/components/common/DataTable'
import { Button } from '@/components/common/Button'
import type { Column } from '@/components/common/DataTable'
import { useEndpoints } from '@/hooks/useEndpoints'
import { EndpointDrawer } from '@/components/endpoints/EndpointDrawer'
import { formatRelativeTime } from '@/components/endpoints/format'
import type { components } from '@/api/schema'
import { Monitor, Search } from 'lucide-react'

type Endpoint = components['schemas']['Endpoint']

function requestErrorMessage(error: unknown): string {
  return error && typeof error === 'object' && 'message' in error && typeof error.message === 'string'
    ? error.message
    : 'The endpoint inventory could not be loaded.'
}

// ── Main page ─────────────────────────────────────────────────────────────────

const PER_PAGE = 25

export function DiscoverPage() {
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [osFilter, setOsFilter] = useState<string>('')
  const [page, setPage] = useState(1)
  const [selectedId, setSelectedId] = useState<string | null>(null)

  // Debounced: the query and the page reset change together once typing
  // pauses, so no request goes out per keystroke or with a stale page.
  useEffect(() => {
    const next = searchInput.trim()
    if (next === search) return
    const id = setTimeout(() => {
      setSearch(next)
      setPage(1)
    }, 250)
    return () => clearTimeout(id)
  }, [searchInput, search])

  // B-226/B-227: search and paging are server-side. Before this, the page
  // only ever fetched the first 25 endpoints and the API ignored search.
  const { data, isLoading, isError, error, isFetching, refetch } = useEndpoints({
    search: search || undefined,
    page,
    per_page: PER_PAGE,
  })
  const total = data?.meta?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PER_PAGE))
  useEffect(() => {
    if (data && page > totalPages) setPage(totalPages)
  }, [data, page, totalPages])

  // The platform filter stays client-side (the API has no OS parameter), so
  // it can only narrow the endpoints already loaded for this page -- said so
  // in the UI rather than implying it filters the whole inventory.
  const pageEndpoints: Endpoint[] = data?.data ?? []
  const endpoints: Endpoint[] = pageEndpoints.filter((ep) =>
    osFilter ? (ep.os ?? '').toLowerCase().includes(osFilter.toLowerCase()) : true,
  )

  const endpointColumns: Column<Endpoint>[] = [
    { key: 'hostname', header: 'Hostname', render: (ep) => <span className="font-medium text-gray-900">{ep.hostname}</span> },
    { key: 'os', header: 'OS', render: (ep) => <span className="text-gray-500 capitalize">{ep.os ?? '—'}</span> },
    { key: 'agent_version', header: 'Agent version', render: (ep) => <span className="text-gray-500">{ep.agent_version ?? '—'}</span> },
    { key: 'ai_app_count', header: 'AI apps', render: (ep) => <span className="text-gray-600">{ep.ai_app_count ?? 0}</span> },
    { key: 'local_model_count', header: 'Local models', render: (ep) => <span className="text-gray-600">{ep.local_model_count ?? 0}</span> },
    { key: 'mcp_server_count', header: 'MCPs', render: (ep) => <span className="text-gray-600">{ep.mcp_server_count ?? 0}</span> },
    { key: 'gpu_count', header: 'GPUs', render: (ep) => <span className="text-gray-600">{ep.gpu_count ?? 0}</span> },
    { key: 'last_seen', header: 'Last seen', render: (ep) => <span className="text-gray-400">{formatRelativeTime(ep.last_seen)}</span> },
  ]

  return (
    <div>
      {/* B-201 Phase 2, Batch 2: collapses the real, confirmed-redundant
          stacked-header bug Part A found here -- Topbar's own "Discover /
          Endpoint AI asset inventory" sat directly above PageHeader's own
          "Endpoints / All discovered endpoints," two full title+subtitle
          bars for one page. Resolved by keeping ONE breadcrumb (matching
          the sidebar's own "Discover" label, consistent with every other
          migrated page) and ONE subtitle -- "All discovered endpoints" was
          kept since it's the more specific, content-relevant description
          of what's actually in the table below; "Endpoint AI asset
          inventory" was dropped as the more generic, now-redundant of the
          two, not silently -- disclosed here and in BUILT.md. */}
      <AppTopBar breadcrumb={[{ label: 'Discover' }]} />
      <div className="p-6">
        <PageHeader subtitle="All discovered endpoints" />

        {/* Filter bar */}
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-gray-400" />
            <input
              type="text"
              placeholder="Search hostname…"
              value={searchInput}
              maxLength={200}
              onChange={(e) => setSearchInput(e.target.value)}
              className="pl-8 pr-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-1 focus:ring-brand-500 w-56"
            />
          </div>
          <select
            value={osFilter}
            title="Filters the endpoints on the current page only"
            onChange={(e) => setOsFilter(e.target.value)}
            className="px-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-1 focus:ring-brand-500"
          >
            <option value="">All platforms</option>
            <option value="windows">Windows</option>
            <option value="darwin">macOS</option>
            <option value="linux">Linux</option>
          </select>
          <span className="text-xs text-gray-500">{isLoading ? 'Loading…' : isError ? 'Endpoints unavailable' : `${total} endpoints`}</span>
          {osFilter && (
            <span className="text-xs text-gray-500">
              Platform filter applies to this page only — showing {endpoints.length} of {pageEndpoints.length} on this page
            </span>
          )}
        </div>

        {/* Table */}
        <div className="mt-4">
          {isError ? (
            <div className="rounded-lg border border-red-200 bg-white p-8 text-center">
              <p className="mb-3 text-sm text-red-700">{requestErrorMessage(error)}</p>
              <Button variant="outline" isLoading={isFetching} onClick={() => refetch()}>Retry</Button>
            </div>
          ) : (
          <DataTable
            columns={endpointColumns}
            data={endpoints}
            loading={isLoading}
            pageSize={PER_PAGE}
            getRowId={(ep) => ep.id}
            onRowClick={(ep) => setSelectedId(ep.id)}
            renderEmpty={() => (
              <EmptyState
                icon={<Monitor className="h-10 w-10" />}
                title="No endpoints found"
                description="Adjust your filters or wait for agents to check in."
              />
            )}
          />
          )}
          {!isError && totalPages > 1 && (
            <div className="mt-4 flex items-center justify-between text-sm text-gray-500">
              <span>Page {page} of {totalPages}</span>
              <div className="flex gap-2">
                <Button size="sm" variant="outline" disabled={page === 1} onClick={() => setPage((p) => p - 1)}>Previous</Button>
                <Button size="sm" variant="outline" disabled={page >= totalPages} onClick={() => setPage((p) => p + 1)}>Next</Button>
              </div>
            </div>
          )}
        </div>
      </div>

      {selectedId && (
        <EndpointDrawer endpointId={selectedId} onClose={() => setSelectedId(null)} />
      )}
    </div>
  )
}
