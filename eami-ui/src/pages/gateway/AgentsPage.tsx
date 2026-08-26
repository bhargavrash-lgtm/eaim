// AgentsPage.tsx — Gateway / Agents with inline Config panel
// Owned by FE-Gateway
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { zodResolver } from '@hookform/resolvers/zod'
import { ConfirmDialog, DataTable } from '@/components/common'
import type { Column } from '@/components/common'
import {
  useAgents,
  useAgentConfig,
  useUpdateAgentConfig,
  useCreateAgent,
  useUpdateAgent,
  useDeleteAgent,
} from '@/hooks/useAgents'
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

// ── Add Agent panel (B-087) ─────────────────────────────────────────────────────

// risk_tier is deliberately restricted to low/medium/high here, matching
// api/openapi.yaml's documented AgentCreate enum exactly -- the real
// backend's own validRiskTiers also accepts "critical" (a minor,
// disclosed spec/implementation gap, not fixed here, out of this brief's
// scope), but this form only ever sends values the documented contract
// promises, never relying on undocumented backend leniency.
const RISK_TIERS = ['low', 'medium', 'high'] as const

const createAgentSchema = z.object({
  name: z.string().min(1, 'Required'),
  model: z.string().min(1, 'Required'),
  owner: z.string().min(1, 'Required'),
  scope: z.string().min(1, 'Required'),
  risk_tier: z.enum(RISK_TIERS),
  token_ttl_seconds: z
    .number({ invalid_type_error: 'Required' })
    .int()
    .min(60, 'Min 60 s')
    .max(14400, 'Max 14400 s'),
})

type CreateAgentFormValues = z.infer<typeof createAgentSchema>

function AddAgentPanel({ onClose }: { onClose: () => void }) {
  const create = useCreateAgent()
  const [toast, setToast] = useState<string | null>(null)

  const form = useForm<CreateAgentFormValues>({
    resolver: zodResolver(createAgentSchema),
    defaultValues: { name: '', model: '', owner: '', scope: '', risk_tier: 'low', token_ttl_seconds: 900 },
  })

  const onSubmit = async (values: CreateAgentFormValues) => {
    try {
      await create.mutateAsync(values)
      setToast('Agent created')
      setTimeout(() => { setToast(null); onClose() }, 1000)
    } catch (err) {
      setToast((err as any)?.message ?? 'Failed to create agent')
    }
  }

  return (
    <div className="fixed inset-y-0 right-0 w-96 bg-white shadow-xl flex flex-col z-50">
      <div className="flex items-center justify-between px-6 py-4 border-b">
        <h2 className="font-semibold text-gray-900">Add Agent</h2>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">&times;</button>
      </div>

      {toast && (
        <div className={`mx-6 mt-4 px-4 py-2 rounded text-sm border ${
          toast.includes('Failed') ? 'bg-red-50 border-red-200 text-red-700' : 'bg-green-50 border-green-200 text-green-700'
        }`}>
          {toast}
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-6 py-4">
        <form id="add-agent-form" onSubmit={form.handleSubmit(onSubmit)} className="space-y-5">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Name</label>
            <input {...form.register('name')} placeholder="claude-support-01"
              className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            {form.formState.errors.name && <p className="mt-1 text-xs text-red-600">{form.formState.errors.name.message}</p>}
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Model</label>
            <input {...form.register('model')} placeholder="claude-sonnet-5"
              className="w-full border rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            {form.formState.errors.model && <p className="mt-1 text-xs text-red-600">{form.formState.errors.model.message}</p>}
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Owner</label>
            <input {...form.register('owner')} placeholder="Support team"
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            {form.formState.errors.owner && <p className="mt-1 text-xs text-red-600">{form.formState.errors.owner.message}</p>}
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Declared scope</label>
            <textarea {...form.register('scope')} rows={3} placeholder="What this agent is allowed to do, in plain language"
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            {form.formState.errors.scope && <p className="mt-1 text-xs text-red-600">{form.formState.errors.scope.message}</p>}
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Risk tier</label>
            <select {...form.register('risk_tier')}
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
              {RISK_TIERS.map(t => <option key={t} value={t}>{t}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Token TTL (seconds)</label>
            <input type="number" {...form.register('token_ttl_seconds', { valueAsNumber: true })}
              className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            {form.formState.errors.token_ttl_seconds && <p className="mt-1 text-xs text-red-600">{form.formState.errors.token_ttl_seconds.message}</p>}
          </div>
        </form>
      </div>

      <div className="px-6 py-4 border-t flex gap-3">
        <button type="submit" form="add-agent-form" disabled={create.isPending}
          className="flex-1 bg-indigo-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-indigo-700 disabled:opacity-50">
          {create.isPending ? 'Creating…' : 'Create agent'}
        </button>
        <button onClick={onClose} className="px-4 py-2 text-sm text-gray-600 hover:text-gray-900">Cancel</button>
      </div>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export function AgentsPage() {
  const { data, isLoading, error } = useAgents()
  // Deep-linking/highlighting by ID (B-092): ?highlight=<agent id> lands
  // on and highlights that row via DataTable's getRowId/highlightRowId.
  const [searchParams] = useSearchParams()
  const highlightId = searchParams.get('highlight')
  const [configAgent, setConfigAgent] = useState<Agent | null>(null)
  const [showAdd, setShowAdd] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<Agent | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  const updateAgent = useUpdateAgent()
  const deleteAgent = useDeleteAgent()

  const agents: Agent[] = (data as any)?.data ?? []

  function handleToggleSuspend(agent: Agent) {
    setActionError(null)
    const nextStatus = (agent as any).status === 'suspended' ? 'active' : 'suspended'
    updateAgent.mutate(
      { id: agent.id, body: { status: nextStatus } },
      { onError: (err) => setActionError((err as any)?.message ?? 'Failed to update agent status') },
    )
  }

  function handleConfirmDelete() {
    if (!deleteTarget) return
    setActionError(null)
    deleteAgent.mutate(deleteTarget.id, {
      onSuccess: () => setDeleteTarget(null),
      // Deliberately NOT closing the dialog on error -- a 409 from B-077's
      // fix ("cannot delete an agent with existing history -- suspend it
      // instead") is real, actionable information the admin needs to see,
      // not a reason to silently dismiss the dialog.
      onError: (err) => setActionError((err as any)?.message ?? 'Failed to delete agent'),
    })
  }

  if (isLoading) {
    return <div className="p-6 text-sm text-gray-400">Loading agents…</div>
  }
  if (error) {
    return <div className="p-6 text-sm text-red-500">Failed to load agents.</div>
  }

  // B-104: shared DataTable (closes the row-click bug class B-081 found --
  // hover/cursor styling only applies here because a real onRowClick is
  // passed below). pageSize is set high enough to never engage DataTable's
  // own pager, matching this page's existing "show every agent" behavior.
  const agentColumns: Column<Agent>[] = [
    { key: 'name', header: 'Name', render: (agent) => <span className="font-medium text-gray-900">{agent.name}</span> },
    { key: 'model', header: 'Model', render: (agent) => <span className="font-mono text-gray-600">{agent.model}</span> },
    { key: 'risk_tier', header: 'Risk', render: (agent) => <RiskBadge tier={(agent as any).risk_tier} /> },
    { key: 'status', header: 'Status', render: (agent) => <StatusBadge status={(agent as any).status} /> },
    { key: 'owner', header: 'Owner', render: (agent) => <span className="text-gray-500">{(agent as any).owner}</span> },
    {
      key: 'actions',
      header: 'Actions',
      className: 'text-right',
      render: (agent) => (
        <div className="flex items-center justify-end gap-3">
          <button
            onClick={(e) => { e.stopPropagation(); setConfigAgent(agent) }}
            className="text-indigo-600 hover:text-indigo-800 text-xs font-medium"
          >
            Configure
          </button>
          <button
            onClick={(e) => { e.stopPropagation(); handleToggleSuspend(agent) }}
            disabled={updateAgent.isPending}
            className="text-amber-600 hover:text-amber-800 text-xs font-medium disabled:opacity-50"
          >
            {(agent as any).status === 'suspended' ? 'Reactivate' : 'Suspend'}
          </button>
          <button
            onClick={(e) => { e.stopPropagation(); setActionError(null); setDeleteTarget(agent) }}
            className="text-red-600 hover:text-red-800 text-xs font-medium"
          >
            Delete
          </button>
        </div>
      ),
    },
  ]

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-900">Gateway Agents</h1>
        <button
          onClick={() => setShowAdd(true)}
          className="flex items-center gap-1.5 bg-indigo-600 text-white rounded px-3 py-1.5 text-sm font-medium hover:bg-indigo-700"
        >
          + Add agent
        </button>
      </div>

      {actionError && (
        <div className="mb-4 flex items-start gap-3 rounded-md bg-red-50 border border-red-200 px-4 py-3 text-sm text-red-800">
          <span className="flex-1">{actionError}</span>
          <button onClick={() => setActionError(null)} className="shrink-0 text-red-500 hover:text-red-700 text-base leading-none" aria-label="Dismiss">×</button>
        </div>
      )}

      {agents.length === 0 ? (
        <p className="text-sm text-gray-400">No agents registered yet.</p>
      ) : (
        <DataTable
          columns={agentColumns}
          data={agents}
          onRowClick={setConfigAgent}
          pageSize={1000}
          getRowId={(agent) => agent.id}
          highlightRowId={highlightId}
        />
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

      {/* Add Agent slide-out panel */}
      {showAdd && (
        <>
          <div className="fixed inset-0 bg-black/20 z-40" onClick={() => setShowAdd(false)} />
          <AddAgentPanel onClose={() => setShowAdd(false)} />
        </>
      )}

      {/* Delete confirm dialog */}
      {deleteTarget && (
        <ConfirmDialog
          open
          title={`Delete "${deleteTarget.name}"?`}
          description="This permanently removes the agent identity. If it has real episode, approval, or workflow-run history, deletion will be refused -- suspend it instead."
          confirmLabel="Delete"
          destructive
          isLoading={deleteAgent.isPending}
          onConfirm={handleConfirmDelete}
          onCancel={() => setDeleteTarget(null)}
        >
          {/* actionError is rendered in normal page flow, which a full-
              screen ConfirmDialog overlay (fixed inset-0 z-50) completely
              covers -- a blocked-delete 409 would otherwise set a message
              the admin can never actually see while the dialog stays open
              (code-review finding). Rendered via ConfirmDialog's own
              children slot so it's visible inside the dialog itself. */}
          {actionError && (
            <div className="rounded-md bg-red-50 border border-red-200 px-3 py-2 text-sm text-red-800">
              {actionError}
            </div>
          )}
        </ConfirmDialog>
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
