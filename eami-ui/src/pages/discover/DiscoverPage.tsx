import { useState } from 'react'
import { Topbar } from '@/components/layout/Topbar'
import { PageHeader } from '@/components/common/PageHeader'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { EmptyState } from '@/components/common/EmptyState'
import { useEndpoints, useEndpoint } from '@/hooks/useEndpoints'
import type { components } from '@/api/schema'
import { Monitor, ChevronDown, ChevronRight, X, Search } from 'lucide-react'

type Endpoint = components['schemas']['Endpoint']
type EndpointReport = components['schemas']['EndpointReport']
type MCPServer = components['schemas']['MCPServer']
type GPU = components['schemas']['GPU']
type PythonEnv = components['schemas']['PythonEnv']
type NodeProject = components['schemas']['NodeProject']
type AIApp = components['schemas']['AIApp']
type LocalModel = components['schemas']['LocalModel']
type CloudClient = components['schemas']['CloudClient']
type NetworkConnection = components['schemas']['NetworkConnection']

// ── Formatting helpers ──────────────────────────────────────────────────────

function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null) return '—'
  if (bytes >= 1_073_741_824) return `${(bytes / 1_073_741_824).toFixed(1)} GB`
  if (bytes >= 1_048_576) return `${(bytes / 1_048_576).toFixed(0)} MB`
  return `${bytes} B`
}

function formatRelativeTime(iso: string): string {
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (secs < 60) return `${secs}s ago`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`
  return `${Math.floor(secs / 86400)}d ago`
}

// ── Collapsible section ──────────────────────────────────────────────────────

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  const [open, setOpen] = useState(count > 0)
  return (
    <div className="border border-gray-200 rounded-lg overflow-hidden">
      <button
        className="w-full flex items-center justify-between px-4 py-3 bg-gray-50 text-sm font-medium text-gray-700 hover:bg-gray-100"
        onClick={() => setOpen((o) => !o)}
      >
        <span className="flex items-center gap-2">
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          {title}
          <span className="rounded-full bg-gray-200 px-1.5 py-0.5 text-[10px] font-bold text-gray-600">{count}</span>
        </span>
      </button>
      {open && <div className="px-4 py-3 text-sm text-gray-700 bg-white">{children}</div>}
    </div>
  )
}

// ── Slide-out drawer ─────────────────────────────────────────────────────────

function EndpointDrawer({ endpointId, onClose }: { endpointId: string; onClose: () => void }) {
  const { data: endpoint, isLoading } = useEndpoint(endpointId)
  const report: EndpointReport | undefined = endpoint?.latest_report

  return (
    <div className="fixed inset-0 z-40 flex justify-end">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} />
      <div className="relative z-50 w-full max-w-xl bg-white shadow-xl overflow-y-auto flex flex-col">
        <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4">
          <h2 className="text-base font-semibold text-gray-900">{endpoint?.hostname ?? '…'}</h2>
          <button onClick={onClose} className="rounded p-1 text-gray-400 hover:bg-gray-100"><X className="h-4 w-4" /></button>
        </div>

        {isLoading ? (
          <div className="flex flex-1 items-center justify-center py-16"><LoadingSpinner /></div>
        ) : !endpoint || !report ? (
          <div className="flex flex-1 items-center justify-center py-16">
            <EmptyState title="No report data available" />
          </div>
        ) : (
          <div className="flex-1 p-5 space-y-3">
            {/* Summary */}
            <div className="grid grid-cols-2 gap-2 text-xs">
              {([
                ['OS', endpoint.os ?? '—'],
                ['Agent version', endpoint.agent_version ?? '—'],
                ['Agent ID', endpoint.agent_id ?? '—'],
                ['Last seen', formatRelativeTime(endpoint.last_seen)],
                ['Risk score', endpoint.risk_score != null ? `${endpoint.risk_score.toFixed(0)} / 100` : '—'],
              ] as [string, string][]).map(([k, v]) => (
                <div key={k} className="rounded bg-gray-50 px-3 py-2">
                  <div className="text-gray-400 uppercase tracking-wide text-[9px] font-semibold">{k}</div>
                  <div className="mt-0.5 font-medium text-gray-800 break-all">{v}</div>
                </div>
              ))}
            </div>

            {/* MCP Servers — now MCPServer[] directly */}
            <Section title="MCP Servers" count={report.mcp_servers?.length ?? 0}>
              {(report.mcp_servers ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {(report.mcp_servers as MCPServer[]).map((s, i) => (
                      <li key={i} className="flex items-center justify-between">
                        <span className="font-medium">{s.name}</span>
                        <span className="text-gray-400 text-xs">
                          {s.source}{s.port != null ? `:${s.port}` : ''} · {s.active ? 'active' : 'inactive'}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* AI Apps */}
            <Section title="AI Apps" count={report.ai_apps?.length ?? 0}>
              {(report.ai_apps ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {(report.ai_apps as AIApp[]).map((app, i) => (
                      <li key={i} className="flex items-center justify-between">
                        <span className="font-medium">{app.name}</span>
                        <span className="text-gray-400 text-xs">{app.version ?? '—'}</span>
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* Local Models */}
            <Section title="Local Models" count={report.local_models?.length ?? 0}>
              {(report.local_models ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {(report.local_models as LocalModel[]).map((m, i) => (
                      <li key={i} className="flex items-center justify-between">
                        <span className="font-medium">{m.name}</span>
                        <span className="text-gray-400 text-xs">
                          {m.source} · {m.size_bytes != null ? formatBytes(m.size_bytes) : '—'}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* Cloud Clients */}
            <Section title="Cloud Clients" count={report.cloud_clients?.length ?? 0}>
              {(report.cloud_clients ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {(report.cloud_clients as CloudClient[]).map((c, i) => (
                      <li key={i} className="flex items-center justify-between">
                        <span className="font-medium">{c.provider}</span>
                        <span className="text-gray-400 text-xs">
                          {c.configured ? 'configured' : 'not configured'}
                          {c.key_prefix ? ` · ${c.key_prefix.slice(0, 7)}…` : ''}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* GPU — vram_bytes (not vram_mb) */}
            <Section title="GPUs" count={report.gpus?.length ?? 0}>
              {(report.gpus ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {(report.gpus as GPU[]).map((g, i) => (
                      <li key={i} className="flex items-center justify-between">
                        <span className="font-medium">{g.name}</span>
                        <span className="text-gray-400 text-xs">
                          {formatBytes(g.vram_bytes)} VRAM · {g.driver_version ?? '—'}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* Network Activity */}
            <Section title="Network Activity" count={(report.network_activity as any)?.active_connections?.length ?? 0}>
              {((report.network_activity as any)?.active_connections ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {((report.network_activity as any)?.active_connections as NetworkConnection[] ?? []).map((n, i) => (
                      <li key={i} className="flex items-center justify-between text-xs">
                        <span className="font-medium">{n.remote_host}:{n.remote_port}</span>
                        <span className="text-gray-400">{n.process_name} (PID {n.pid}) · {n.state}</span>
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* Python Envs — new schema: path, type, ai_packages: string[], detected_at */}
            <Section title="Python Environments" count={report.python_envs?.length ?? 0}>
              {(report.python_envs ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {(report.python_envs as PythonEnv[]).map((env, i) => (
                      <li key={i}>
                        <div className="flex items-center justify-between">
                          <span className="font-medium truncate">{env.path}</span>
                          <span className="text-gray-400 text-xs ml-2 capitalize">{env.type}</span>
                        </div>
                        {(env.ai_packages ?? []).length > 0 && (
                          <div className="mt-1 flex flex-wrap gap-1">
                            {(env.ai_packages as string[]).map((pkg, j) => (
                              <span key={j} className="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600">{pkg}</span>
                            ))}
                          </div>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* Node Projects — replaces nodejs_ai */}
            <Section title="Node.js AI Projects" count={report.node_projects?.length ?? 0}>
              {(report.node_projects ?? []).length === 0
                ? <p className="text-gray-400">None detected</p>
                : (
                  <ul className="space-y-1">
                    {(report.node_projects as NodeProject[]).map((p, i) => (
                      <li key={i} className="text-xs">
                        <div className="flex items-center justify-between">
                          <span className="font-medium truncate">{p.path}</span>
                        </div>
                        {(p.ai_packages ?? []).length > 0 && (
                          <div className="mt-1 flex flex-wrap gap-1">
                            {(p.ai_packages as string[]).map((pkg, j) => (
                              <span key={j} className="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600">{pkg}</span>
                            ))}
                          </div>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
            </Section>
          </div>
        )}
      </div>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export function DiscoverPage() {
  const [search, setSearch] = useState('')
  const [osFilter, setOsFilter] = useState<string>('')
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const { data, isLoading } = useEndpoints({
    search: search || undefined,
    per_page: 25,
  })

  const endpoints: Endpoint[] = (data?.data ?? []).filter((ep) =>
    osFilter ? (ep.os ?? '').toLowerCase().includes(osFilter.toLowerCase()) : true,
  )

  return (
    <div>
      <Topbar title="Discover" subtitle="Endpoint AI asset inventory" />
      <div className="p-6">
        <PageHeader title="Endpoints" subtitle="All discovered endpoints" />

        {/* Filter bar */}
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-gray-400" />
            <input
              type="text"
              placeholder="Search hostname…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8 pr-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-1 focus:ring-brand-500 w-56"
            />
          </div>
          <select
            value={osFilter}
            onChange={(e) => setOsFilter(e.target.value)}
            className="px-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-1 focus:ring-brand-500"
          >
            <option value="">All platforms</option>
            <option value="windows">Windows</option>
            <option value="darwin">macOS</option>
            <option value="linux">Linux</option>
          </select>
          <span className="text-xs text-gray-500">{data?.meta?.total ?? 0} endpoints</span>
        </div>

        {/* Table */}
        <div className="mt-4">
          {isLoading ? (
            <div className="flex justify-center py-16"><LoadingSpinner size="lg" /></div>
          ) : endpoints.length === 0 ? (
            <EmptyState
              icon={<Monitor className="h-10 w-10" />}
              title="No endpoints found"
              description="Adjust your filters or wait for agents to check in."
            />
          ) : (
            <div className="overflow-hidden rounded-lg border border-gray-200">
              <table className="min-w-full divide-y divide-gray-200 text-sm">
                <thead className="bg-gray-50">
                  <tr>
                    {['Hostname', 'OS', 'Agent version', 'AI apps', 'Local models', 'MCPs', 'GPUs', 'Last seen'].map((h) => (
                      <th key={h} className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 bg-white">
                  {endpoints.map((ep) => (
                    <tr key={ep.id} className="cursor-pointer hover:bg-gray-50" onClick={() => setSelectedId(ep.id)}>
                      <td className="px-4 py-3 font-medium text-gray-900">{ep.hostname}</td>
                      <td className="px-4 py-3 text-gray-500 capitalize">{ep.os ?? '—'}</td>
                      <td className="px-4 py-3 text-gray-500">{ep.agent_version ?? '—'}</td>
                      <td className="px-4 py-3 text-gray-600">{ep.ai_app_count ?? 0}</td>
                      <td className="px-4 py-3 text-gray-600">{ep.local_model_count ?? 0}</td>
                      <td className="px-4 py-3 text-gray-600">{ep.mcp_server_count ?? 0}</td>
                      <td className="px-4 py-3 text-gray-600">{ep.gpu_count ?? 0}</td>
                      <td className="px-4 py-3 text-gray-400">{formatRelativeTime(ep.last_seen)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
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
