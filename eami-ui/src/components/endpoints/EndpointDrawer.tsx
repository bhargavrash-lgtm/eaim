import { useState } from 'react'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { EmptyState } from '@/components/common/EmptyState'
import { SlideOverPanel } from '@/components/common/SlideOverPanel'
import { useEndpoint } from '@/hooks/useEndpoints'
import type { components } from '@/api/schema'
import { ChevronDown, ChevronRight, X } from 'lucide-react'
import { LinkedAgentControl } from './LinkedAgentControl'
import { formatBytes, formatRelativeTime } from './format'
import { CategoryStateLabel } from './CategoryStateLabel'
import { STATE_DESCRIPTION, STATE_LABEL, categoryState, type CategoryState, type EndpointWithScannerStatus } from './scannerState'

// Moved from pages/discover/DiscoverPage.tsx (B-252 C0), behaviour unchanged.

type EndpointReport = components['schemas']['EndpointReport']
type MCPServer = components['schemas']['MCPServer']
type GPU = components['schemas']['GPU']
type PythonEnv = components['schemas']['PythonEnv']
type NodeProject = components['schemas']['NodeProject']
type AIApp = components['schemas']['AIApp']
type LocalModel = components['schemas']['LocalModel']
type CloudClient = components['schemas']['CloudClient']
type NetworkConnection = components['schemas']['NetworkConnection']

// ── Collapsible section ──────────────────────────────────────────────────────

// Item 4: the badge and body show the category's honest state. The list (and
// a genuine "None detected") only render when the scanner actually ran.
function Section({ title, state, children }: { title: string; state: CategoryState; children: React.ReactNode }) {
  const [open, setOpen] = useState(state.kind === 'count' && state.count > 0)
  return (
    <div className="border border-gray-200 rounded-lg overflow-hidden">
      <button
        className="w-full flex items-center justify-between px-4 py-3 bg-gray-50 text-sm font-medium text-gray-700 hover:bg-gray-100"
        onClick={() => setOpen((o) => !o)}
      >
        <span className="flex items-center gap-2">
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          {title}
          {state.kind === 'count'
            ? <span className="rounded-full bg-gray-200 px-1.5 py-0.5 text-2xs font-bold text-gray-600">{state.count}</span>
            : <CategoryStateLabel state={state} />}
        </span>
      </button>
      {open && (
        <div className="px-4 py-3 text-sm text-gray-700 bg-white">
          {state.kind === 'count' ? children : <p className="text-gray-400">{STATE_DESCRIPTION[state.kind]}</p>}
        </div>
      )}
    </div>
  )
}

// ── Slide-out drawer ─────────────────────────────────────────────────────────

// Exported for reuse by AgentDetailPage's relationship graph (B-200) --
// the Endpoint node click reuses this real, self-contained drawer
// directly (fetches its own data via endpointId, no dependency on
// DiscoverPage's own local state) rather than duplicating it.
export function EndpointDrawer({ endpointId, onClose }: { endpointId: string; onClose: () => void }) {
  const { data, isLoading } = useEndpoint(endpointId)
  const endpoint = data as EndpointWithScannerStatus | undefined
  const report: EndpointReport | undefined = endpoint?.latest_report

  return (
    <SlideOverPanel onClose={onClose}>
      <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4">
        <h2 className="text-base font-semibold text-gray-900">{endpoint?.hostname ?? '…'}</h2>
        <button onClick={onClose} className="rounded p-1 text-gray-400 hover:bg-gray-100"><X className="h-4 w-4" /></button>
      </div>

        {isLoading ? (
          <div className="flex flex-1 items-center justify-center overflow-y-auto py-16"><LoadingSpinner /></div>
        ) : endpoint && endpoint.has_report === false ? (
          <div className="flex flex-1 items-center justify-center overflow-y-auto py-16">
            <EmptyState title={STATE_LABEL.never} description={STATE_DESCRIPTION.never} />
          </div>
        ) : !endpoint || !report ? (
          <div className="flex flex-1 items-center justify-center overflow-y-auto py-16">
            <EmptyState title="No report data available" />
          </div>
        ) : (
          <div className="flex-1 overflow-y-auto p-5 space-y-3">
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
                  <div className="text-gray-400 uppercase tracking-wide text-2xs font-semibold">{k}</div>
                  <div className="mt-0.5 font-medium text-gray-800 break-all">{v}</div>
                </div>
              ))}
            </div>

            {/* Linked governed agent (B-164/B-165). key={endpoint.id}
                forces a fresh component instance per endpoint -- the
                drawer never unmounts between row clicks (only its
                endpointId prop changes), so without this key a pending
                mutation/selection for one endpoint could briefly leak
                into another endpoint's control (code-review finding). */}
            <LinkedAgentControl key={endpoint.id} endpoint={endpoint} />

            {/* MCP Servers — now MCPServer[] directly */}
            <Section title="MCP Servers" state={categoryState(endpoint, 'mcp_servers', report.mcp_servers?.length ?? 0)}>
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
            <Section title="AI Apps" state={categoryState(endpoint, 'ai_apps', report.ai_apps?.length ?? 0)}>
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
            <Section title="Local Models" state={categoryState(endpoint, 'local_models', report.local_models?.length ?? 0)}>
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
            <Section title="Cloud Clients" state={categoryState(endpoint, 'cloud_clients', report.cloud_clients?.length ?? 0)}>
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
            <Section title="GPUs" state={categoryState(endpoint, 'gpus', report.gpus?.length ?? 0)}>
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
            <Section title="Network Activity" state={categoryState(endpoint, 'network_activity', (report.network_activity as any)?.active_connections?.length ?? 0)}>
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
            <Section title="Python Environments" state={categoryState(endpoint, 'python_envs', report.python_envs?.length ?? 0)}>
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
                              <span key={j} className="rounded bg-gray-100 px-1.5 py-0.5 text-2xs text-gray-600">{pkg}</span>
                            ))}
                          </div>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
            </Section>

            {/* Node Projects — replaces nodejs_ai */}
            <Section title="Node.js AI Projects" state={categoryState(endpoint, 'node_projects', report.node_projects?.length ?? 0)}>
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
                              <span key={j} className="rounded bg-gray-100 px-1.5 py-0.5 text-2xs text-gray-600">{pkg}</span>
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
    </SlideOverPanel>
  )
}
