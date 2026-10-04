// Endpoint Detail (B-252 C2; DESIGN_SYSTEM.md §7.7) at /assets/endpoints/:id.
// One canonical page per endpoint, replacing Discover's drawer. Tabs are real
// only: Overview (Discover's content, with item 4's honest scanner states),
// Agent Link, Classification. Connections stays future (C6) and is not
// rendered as an empty shell.
import { useEffect } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { Lock, Monitor } from 'lucide-react'
import { EmptyState, LoadingSpinner } from '@/components/common'
import { AppTopBar } from '@/components/layout/AppTopBar'
import { AssetClassificationTab } from '@/components/cmdb/AssetClassificationTab'
import { EndpointDetections } from '@/components/endpoints/EndpointDetections'
import { LinkedAgentControl } from '@/components/endpoints/LinkedAgentControl'
import { formatOS, formatRelativeTime } from '@/components/endpoints/format'
import { STATE_DESCRIPTION, STATE_LABEL, type EndpointWithScannerStatus } from '@/components/endpoints/scannerState'
import { useEndpoint } from '@/hooks/useEndpoints'
import { can, useOrgRole } from '@/lib/rbac'

const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'agent-link', label: 'Agent Link' },
  { id: 'classification', label: 'Classification' },
] as const
type TabId = (typeof TABS)[number]['id']
const TAB_VISIBLE: Record<TabId, (role: string | undefined) => boolean> = {
  overview: () => true,
  'agent-link': () => true,
  classification: can.viewCMDB,
}

// first_seen is returned by GET /v1/endpoints/{id} but missing from
// api/openapi.yaml's Endpoint schema (drift logged for Architect-EAMI).
type EndpointDetail = EndpointWithScannerStatus & { first_seen?: string }

const CARD = 'rounded-[10px] border border-[rgba(228,231,240,0.55)] bg-white shadow-l1'
// §7.4's honest pending card (same treatment as RelationshipGraph's).
const PENDING = 'flex flex-col items-center justify-center gap-1.5 rounded-lg border-[1.5px] border-dashed border-gray-300 bg-white px-6 py-14 text-center shadow-l1'

function errorCode(error: unknown): string | undefined {
  return error && typeof error === 'object' && 'code' in error && typeof error.code === 'string' ? error.code : undefined
}

function PageShell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex h-full flex-col overflow-hidden">
      <AppTopBar breadcrumb={[{ label: 'Assets', href: '/assets?kind=endpoint' }, { label: title }]} />
      <div className="flex-1 overflow-y-auto px-10 py-8">{children}</div>
    </div>
  )
}

export function EndpointDetailPage() {
  const { id } = useParams<{ id: string }>()
  const role = useOrgRole()
  const allowed = can.viewEndpoints(role)
  // A role without endpoint reads (approver) never requests the endpoint.
  const { data, isLoading, error } = useEndpoint(allowed ? id ?? '' : '')
  const endpoint = data as EndpointDetail | undefined
  const tabs = TABS.filter((t) => TAB_VISIBLE[t.id](role))
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const activeTab: TabId = tabs.some((t) => t.id === tabParam) ? (tabParam as TabId) : 'overview'
  const hiddenTabParam = tabParam != null && TABS.some((t) => t.id === tabParam) && !tabs.some((t) => t.id === tabParam)
  useEffect(() => {
    if (hiddenTabParam) setSearchParams({ tab: 'overview' }, { replace: true })
  }, [hiddenTabParam, setSearchParams])
  function setTab(next: TabId) {
    setSearchParams({ tab: next }, { replace: true })
  }
  function onTabKeyDown(e: React.KeyboardEvent) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
    const i = tabs.findIndex((t) => t.id === activeTab)
    const next = tabs[(i + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length]
    setTab(next.id)
    document.getElementById(`endpoint-tab-${next.id}`)?.focus()
  }

  // Approvers (B-253): no endpoint reads. An honest role state, in the same
  // tone as Agent Detail's Actions-tab note -- not an error, not a blank page.
  if (!allowed) {
    return (
      <PageShell title="Endpoint">
        <div className={CARD}>
          <EmptyState
            icon={<Lock className="h-10 w-10" />}
            title="Endpoint details aren't available for your role"
            description="Your role can review and decide approval requests. Endpoint inventory, detections and classification require the admin, operator or viewer role."
          />
        </div>
      </PageShell>
    )
  }
  if (isLoading) {
    return <PageShell title="Endpoint"><div className="flex justify-center py-16"><LoadingSpinner /></div></PageShell>
  }
  // No Discovery license: §7.4's honest pending state, never an empty page
  // implying the endpoint has nothing.
  if (errorCode(error) === 'module_not_licensed') {
    return (
      <PageShell title="Endpoint">
        <div className={PENDING}>
          <Monitor className="h-8 w-8 text-gray-300" />
          <p className="text-sm font-medium text-gray-600">Endpoint inventory not available</p>
          <p className="max-w-md text-xs text-gray-500">Endpoint details require a Discovery license for your organization. Governed agents and tools remain available in Assets.</p>
        </div>
      </PageShell>
    )
  }
  if (error || !endpoint) {
    const notFound = errorCode(error) === 'not_found' || (!error && !endpoint)
    return (
      <PageShell title="Endpoint">
        <div className={CARD}>
          <EmptyState
            title={notFound ? 'Endpoint not found' : 'Failed to load endpoint'}
            description={notFound ? 'It may have been removed, or the link is from another organization.' : 'Try reloading the page.'}
          />
        </div>
      </PageShell>
    )
  }

  const report = endpoint.latest_report
  const summary: [string, string][] = [
    ['OS', formatOS(endpoint.os)],
    ['Agent version', endpoint.agent_version || '—'],
    ['Agent ID', endpoint.agent_id ?? '—'],
    ['First seen', endpoint.first_seen ? formatRelativeTime(endpoint.first_seen) : '—'],
    ['Last seen', formatRelativeTime(endpoint.last_seen)],
    ['Risk score', endpoint.risk_score != null ? `${endpoint.risk_score.toFixed(0)} / 100` : '—'],
  ]

  return (
    <PageShell title={endpoint.hostname}>
      <div className="mb-6 flex items-center gap-3.5">
        <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-[10px] bg-brand-50">
          <Monitor className="h-5 w-5 text-brand-600" />
        </div>
        <div>
          {/* h2: AppTopBar's breadcrumb is the page's h1 (as Agent Detail). */}
          <h2 className="text-xl font-bold text-ink">{endpoint.hostname}</h2>
          <div className="font-mono text-2xs text-ink-faint">{formatOS(endpoint.os)} · last seen {formatRelativeTime(endpoint.last_seen)}</div>
        </div>
      </div>

      <div className="mb-6 border-b border-gray-200">
        <nav className="-mb-px flex gap-6" role="tablist" aria-label="Endpoint sections" onKeyDown={onTabKeyDown}>
          {tabs.map((tab) => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              id={`endpoint-tab-${tab.id}`}
              aria-controls={`endpoint-panel-${tab.id}`}
              aria-selected={activeTab === tab.id}
              tabIndex={activeTab === tab.id ? 0 : -1}
              onClick={() => setTab(tab.id)}
              className={`border-b-2 py-3 text-sm font-medium transition-colors ${activeTab === tab.id ? 'border-brand-600 text-brand-700' : 'border-transparent text-gray-500 hover:text-gray-700'}`}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {activeTab === 'overview' && (
        <div role="tabpanel" id="endpoint-panel-overview" aria-labelledby="endpoint-tab-overview" className="space-y-5">
          <div className="grid grid-cols-2 gap-2 text-xs md:grid-cols-3">
            {summary.map(([k, v]) => (
              <div key={k} className="rounded bg-gray-50 px-3 py-2">
                <div className="text-2xs font-semibold uppercase tracking-wide text-gray-400">{k}</div>
                <div className="mt-0.5 break-all font-medium text-gray-800">{v}</div>
              </div>
            ))}
          </div>
          {/* Item 4: a report-less endpoint says so; detections never show a bare 0. */}
          {endpoint.has_report === false ? (
            <div className={CARD}>
              <EmptyState title={STATE_LABEL.never} description={STATE_DESCRIPTION.never} />
            </div>
          ) : !report ? (
            // A report exists but its data didn't come back: not "never reported".
            <div className={CARD}>
              <EmptyState title="Report data unavailable" description="This endpoint has reported, but its latest scan report couldn't be loaded. Try reloading the page." />
            </div>
          ) : (
            <EndpointDetections endpoint={endpoint} report={report} />
          )}
        </div>
      )}

      {activeTab === 'agent-link' && (
        // Founder decision: shown even with no scan report -- a report-less
        // endpoint is exactly where manual linking is most useful.
        <div role="tabpanel" id="endpoint-panel-agent-link" aria-labelledby="endpoint-tab-agent-link" className={`${CARD} p-5`}>
          <LinkedAgentControl key={endpoint.id} endpoint={endpoint} />
        </div>
      )}

      {activeTab === 'classification' && TAB_VISIBLE.classification(role) && (
        <div role="tabpanel" id="endpoint-panel-classification" aria-labelledby="endpoint-tab-classification">
          <AssetClassificationTab kind="endpoint" id={endpoint.id} noun="endpoint" />
        </div>
      )}
    </PageShell>
  )
}
