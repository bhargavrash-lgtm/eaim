// The eight detection sections for one endpoint's latest scan report, each
// showing its honest state (master-sequence item 4). Moved verbatim from
// EndpointDrawer (B-252 C2) so Endpoint Detail's Overview renders exactly
// what Discover's drawer did; the drawer is retired with Discover (C4).
import { useState } from 'react'
import type { components } from '@/api/schema'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { formatBytes, formatModelSource } from './format'
import { CategoryStateLabel } from './CategoryStateLabel'
import { STATE_DESCRIPTION, categoryState, type CategoryState, type EndpointWithScannerStatus } from './scannerState'

// scanner_notes (B-269 Slice 0) isn't in api/openapi.yaml yet (drift logged
// in API_CONTRACT_DRIFT.md); typed locally.
type EndpointReport = components['schemas']['EndpointReport'] & { scanner_notes?: Record<string, string[]> }
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

// ── Detection sections ───────────────────────────────────────────────────────

export function EndpointDetections({ endpoint, report }: { endpoint: EndpointWithScannerStatus; report: EndpointReport }) {
  return (
    <div className="space-y-3">
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
        {/* B-269 Slice 0: what the scan deliberately skipped is never silent. */}
        {report.scanner_notes?.models?.includes('depth_limited') && (
          <p className="mb-2 text-xs text-amber-700">Some model folders go deeper than 8 levels; files below that depth weren't scanned.</p>
        )}
        {report.scanner_notes?.models?.includes('path_root') && (
          <p className="mb-2 text-xs text-amber-700">A configured scan path was, or led to, a whole drive or filesystem root and was skipped.</p>
        )}
        {report.scanner_notes?.models?.includes('path_network') && (
          <p className="mb-2 text-xs text-amber-700">A configured scan path led to a network share and was skipped.</p>
        )}
        {(report.local_models ?? []).length === 0
          ? <p className="text-gray-400">None detected</p>
          : (
            <ul className="space-y-1">
              {(report.local_models as LocalModel[]).map((m, i) => (
                <li key={i} className="flex items-center justify-between">
                  <span className="font-medium">{m.name}</span>
                  <span className="text-gray-400 text-xs">
                    {formatModelSource(m.source)} · {m.size_bytes != null ? formatBytes(m.size_bytes) : '—'}
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
  )
}
