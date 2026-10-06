// AgentConfigPanel -- the agent "Configure" slide-over (endpoint scanner
// settings served to a linked endpoint via B-165's remote-config route).
// Moved verbatim from AgentsPage.tsx so both the Agents list and Agent
// Detail's Actions tab open the exact same panel; behaviour is unchanged.
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { zodResolver } from '@hookform/resolvers/zod'
import { SlideOverPanel, Button } from '@/components/common'
import { useAgentConfig, useUpdateAgentConfig } from '@/hooks/useAgents'
import type { Agent } from '@/hooks/useAgents'

// ── Validation schema ─────────────────────────────────────────────────────────

// Every scanner the agent gates on. Must match the API's store.AllScanners:
// the form drops any stored name missing from this list, so an incomplete
// list here silently strips scanners on the next save (B-271).
const VALID_SCANNERS = [
  'ai_apps', 'models', 'mcp_servers', 'cloud_clients', 'network_activity', 'browser',
  'ai_processes', 'gpu', 'python_envs', 'nodejs_ai',
] as const

const configSchema = z.object({
  scan_interval_seconds: z
    .number({ invalid_type_error: 'Required' })
    .int()
    .min(60, 'Min 60 s')
    .max(86400, 'Max 86400 s'),
  // Optional (B-269 Slice 0, S4): new agents default to no extra model
  // paths, and an empty list is a real value meaning "none".
  model_scan_paths: z.string(),
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

export function AgentConfigPanel({ agent, onClose }: { agent: Agent; onClose: () => void }) {
  const { data: cfg, isLoading, isError, refetch } = useAgentConfig(agent.id)
  // B-236: if the real config can't be loaded, show that -- never a form. An
  // empty (or default-filled) form here could be saved over the agent's
  // real, deliberately chosen settings. A refetch with no cached data puts
  // the query back into its loading state, so `retrying` keeps the error
  // panel (and its spinning Retry) mounted until the retry settles.
  const [retrying, setRetrying] = useState(false)
  const loadFailed = isError || retrying
  const onRetry = async () => {
    setRetrying(true)
    try {
      await refetch()
    } finally {
      setRetrying(false)
    }
  }
  const update = useUpdateAgentConfig()
  const [toast, setToast] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<string | null>(null)

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
    setSaveError(null)
    try {
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
    } catch (err) {
      // Previously an unhandled rejection: a failed save showed nothing.
      setSaveError((err as { message?: string } | null)?.message ?? 'Failed to save config')
    }
  }

  return (
    <SlideOverPanel onClose={onClose}>
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

      {saveError && (
        <div className="mx-6 mt-4 px-4 py-2 bg-red-50 border border-red-200 rounded text-red-700 text-sm">
          {saveError}
        </div>
      )}

      {/* Form */}
      <div className="flex-1 overflow-y-auto px-6 py-4">
        {isLoading && !retrying ? (
          <p className="text-sm text-gray-400">Loading config…</p>
        ) : loadFailed ? (
          <div role="alert" className="rounded-[10px] bg-status-danger px-4 py-4 shadow-l1">
            <p className="text-sm font-semibold text-status-danger-text">Couldn&apos;t load this agent&apos;s config</p>
            <p className="mt-1 text-sm text-status-danger-text">
              The saved settings couldn&apos;t be read, so nothing is shown and nothing can be saved. Try again in a
              moment.
            </p>
            <Button variant="outline" size="sm" className="mt-3" isLoading={retrying} onClick={() => void onRetry()}>
              Retry
            </Button>
          </div>
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
              {/* B-269 Slice 0 (S1, B-194): what counts in these folders. */}
              <p className="mt-1 text-xs text-gray-500">
                Optional. Use specific absolute folders, not a whole drive or every user profile (/home, /Users, C:\Users).
                Only model files are reported: .gguf, .ggml, .safetensors, .bin, .pt, .pth, .ckpt, .onnx, .tflite, .h5,
                .keras, .pb, .mlmodel, .llamafile, at or above the minimum size, and only their name, path, size and
                modified time. <span className="font-medium">.bin</span>, .pb and .h5 can also match non-model files.
                Folders deeper than 8 levels aren&apos;t scanned.
              </p>
              {cfg?.path_warnings?.includes('path_profile_parent') && (
                <p className="mt-1 text-xs text-amber-700">
                  These saved paths include a whole-profile folder. They still apply, but can't be added again: change
                  them to specific folders when you next edit this list.
                </p>
              )}
              {cfg?.path_warnings?.includes('path_root') && (
                <p className="mt-1 text-xs text-red-600">
                  These saved paths include a filesystem root. Updated agents refuse the whole config until it is
                  removed, so none of these settings reach them.
                </p>
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
        {!loadFailed && (
          <Button type="submit" form="config-form" isLoading={update.isPending} className="flex-1">
            Save config
          </Button>
        )}
        <Button variant="secondary" onClick={onClose} disabled={update.isPending}>
          {loadFailed ? 'Close' : 'Cancel'}
        </Button>
      </div>
    </SlideOverPanel>
  )
}
