// AgentsPage.tsx — Gateway / Agents with inline Config panel
// Owned by FE-Gateway
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { zodResolver } from '@hookform/resolvers/zod'
import { useAgents, useAgentConfig, useUpdateAgentConfig } from '@/hooks/useAgents'
import type { Agent } from '@/hooks/useAgents'

// ── Validation schema ─────────────────────────────────────────────────────────

const VALID_SCANNERS = ['ai_apps', 'models', 'mcp_servers', 'cloud_clients', 'network_activity', 'browser'] as const

const configSchema = z.object({
  scan_interval_seconds: z
    .number({ invalid_type_error: 'Required' })
    .int()
    .min(60, 'Min 60 s')
    .max(86400, 'Max 86400 s'),
  model_scan_paths: z
    .string()
    .min(1, 'At least one path required'),
  max_report_size_mb: z
    .number({ invalid_type_error: 'Required' })
    .min(1, 'Min 1 MB')
    .max(50, 'Max 50 MB'),
  enabled_scanners: z
    .array(z.enum(VALID_SCANNERS))
    .min(1, 'Select at least one scanner'),
})

type ConfigFormValues = z.infer<typeof configSchema>

// ── Config panel ──────────────────────────────────────────────────────────────

function ConfigPanel({ agent, onClose }: { agent: Agent; onClose: () => void }) {
  const { data: cfg, isLoading } = useAgentConfig(agent.id)
  const update = useUpdateAgentConfig()
  const [toast, setToast] = useState<string | null>(null)

  const form = useForm<ConfigFormValues>({
    resolver: zodResolver(configSchema),
    values: cfg
      ? {
          scan_interval_seconds: cfg.scan_interval_seconds,
          model_scan_paths: cfg.model_scan_paths.join(', '),
          max_report_size_mb: Math.round(cfg.max_report_size_bytes / 1048576),
          enabled_scanners: (cfg.enabled_scanners as (typeof VALID_SCANNERS)[number][]).filter(
            (s): s is (typeof VALID_SCANNERS)[number] => (VALID_SCANNERS as readonly string[]).includes(s)
          ),
        }
      : undefined,
  })

  const onSubmit = async (values: ConfigFormValues) => {
    await update.mutateAsync({
      id: agent.id,
      body: {
        scan_interval_seconds: values.scan_interval_seconds,
        model_scan_paths: values.model_scan_paths.split(',').map(p => p.trim()).filter(Boolean),
        max_report_size_bytes: values.max_report_size_mb * 1048576,
        enabled_scanners: values.enabled_scanners,
      },
    })
    setToast('Config saved')
    setTimeout(() => setToast(null), 3000)
  }

  return (
    <div className="fixed inset-y-0 right-0 w-96 bg-white shadow-xl flex flex-col z-50">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <div>
          <h2 className="font-semibold text-gray-900">Configure Agent</h2>
          <p className="text-xs text-gray-500 truncate">{agent.name}</p>
        </div>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">&times;</button>
      </div>

      {/* Toast */}
      {toast && (
        <div className="mx-6 mt-4 px-4 py-2 bg-green-50 border border-green-200 rounded text-green-700 text-sm">
          {toast}
        </div>
      )}

      {/* Form */}
      <div className="flex-1 overflow-y-auto px-6 py-4">
        {isLoading ? (
          <p className="text-sm text-gray-400">Loading config…</p>
        ) : (
          <form id="config-form" onSubmit={form.handleSubmit(onSubmit)} className="space-y-5">
            {/* Scan interval */}
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Scan interval (seconds)
              </label>
              <input
                type="number"
                {...form.register('scan_interval_seconds', { valueAsNumber: true })}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              />
              {form.formState.errors.scan_interval_seconds && (
                <p className="mt-1 text-xs text-red-600">{form.formState.errors.scan_interval_seconds.message}</p>
              )}
            </div>

            {/* Model scan paths */}
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Model scan paths <span className="text-gray-400 font-normal">(comma-separated)</span>
              </label>
              <textarea
                {...form.register('model_scan_paths')}
                rows={3}
                className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
              />
              {form.formState.errors.model_scan_paths && (
                <p className="mt-1 text-xs text-red-600">{form.formState.errors.model_scan_paths.message}</p>
              )}
            </div>

            {/* Max report size */}
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Max report size (MB)
              </label>
              <input
                type="number"
                {...form.register('max_report_size_mb', { valueAsNumber: true })}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              />
              {form.formState.errors.max_report_size_mb && (
                <p className="mt-1 text-xs text-red-600">{form.formState.errors.max_report_size_mb.message}</p>
              )}
            </div>

            {/* Enabled scanners */}
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                Enabled scanners
              </label>
              <div className="space-y-2">
                {VALID_SCANNERS.map(scanner => (
                  <label key={scanner} className="flex items-center gap-2 text-sm text-gray-700 cursor-pointer">
                    <input
                      type="checkbox"
                      value={scanner}
                      {...form.register('enabled_scanners')}
                      className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                    />
                    <span className="font-mono">{scanner}</span>
                  </label>
                ))}
              </div>
              {form.formState.errors.enabled_scanners && (
                <p className="mt-1 text-xs text-red-600">{form.formState.errors.enabled_scanners.message}</p>
              )}
            </div>
          </form>
        )}
      </div>

      {/* Footer */}
      <div className="px-6 py-4 border-t flex gap-3">
        <button
          type="submit"
          form="config-form"
          disabled={update.isPending}
          className="flex-1 bg-indigo-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50"
        >
          {update.isPending ? 'Saving…' : 'Save config'}
        </button>
        <button
          onClick={onClose}
          className="px-4 py-2 text-sm text-gray-600 hover:text-gray-900"
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export function AgentsPage() {
  const { data, isLoading, error } = useAgents()
  const [configAgent, setConfigAgent] = useState<Agent | null>(null)

  const agents: Agent[] = (data as any)?.data ?? []

  if (isLoading) {
    return <div className="p-6 text-sm text-gray-400">Loading agents…</div>
  }
  if (error) {
    return <div className="p-6 text-sm text-red-500">Failed to load agents.</div>
  }

  return (
    <div className="p-6">
      <h1 className="text-lg font-semibold text-gray-900 mb-4">Gateway Agents</h1>

      {agents.length === 0 ? (
        <p className="text-sm text-gray-400">No agents registered yet.</p>
      ) : (
        <div className="overflow-x-auto rounded border border-gray-200">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
              <tr>
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Model</th>
                <th className="px-4 py-3 text-left">Risk</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Owner</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {agents.map(agent => (
                <tr key={agent.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 font-medium text-gray-900">{agent.name}</td>
                  <td className="px-4 py-3 font-mono text-gray-600">{agent.model}</td>
                  <td className="px-4 py-3">
                    <RiskBadge tier={(agent as any).risk_tier} />
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge status={(agent as any).status} />
                  </td>
                  <td className="px-4 py-3 text-gray-500">{(agent as any).owner}</td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => setConfigAgent(agent)}
                      className="text-indigo-600 hover:text-indigo-800 text-xs font-medium"
                    >
                      Configure
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Config slide-out panel */}
      {configAgent && (
        <>
          {/* Backdrop */}
          <div
            className="fixed inset-0 bg-black/20 z-40"
            onClick={() => setConfigAgent(null)}
          />
          <ConfigPanel agent={configAgent} onClose={() => setConfigAgent(null)} />
        </>
      )}
    </div>
  )
}

// ── Small badge components ────────────────────────────────────────────────────

function RiskBadge({ tier }: { tier?: string }) {
  const colors: Record<string, string> = {
    low: 'bg-green-100 text-green-700',
    medium: 'bg-yellow-100 text-yellow-700',
    high: 'bg-orange-100 text-orange-700',
    critical: 'bg-red-100 text-red-700',
  }
  const cls = colors[tier ?? ''] ?? 'bg-gray-100 text-gray-600'
  return <span className={`px-2 py-0.5 rounded text-xs font-medium ${cls}`}>{tier ?? '—'}</span>
}

function StatusBadge({ status }: { status?: string }) {
  const colors: Record<string, string> = {
    active: 'bg-green-100 text-green-700',
    suspended: 'bg-yellow-100 text-yellow-700',
    revoked: 'bg-red-100 text-red-700',
  }
  const cls = colors[status ?? ''] ?? 'bg-gray-100 text-gray-600'
  return <span className={`px-2 py-0.5 rounded text-xs font-medium ${cls}`}>{status ?? '—'}</span>
}
