import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Bell, CheckCircle } from 'lucide-react'
import {
  PageHeader,
  DataTable,
  ConfirmDialog,
  EmptyState,
  LoadingSpinner,
} from '@/components/common'
import {
  useAlerts,
  useAlertRules,
  useAcknowledgeAlert,
  useResolveAlert,
  useCreateAlertRule,
  useUpdateAlertRule,
  useDeleteAlertRule,
  useTestAlertRule,
  type Alert,
  type AlertRule,
  type AlertRuleInput,
  type MetricKey,
  type TestRuleResult,
} from '@/hooks/useAlerts'

// ── Constants ─────────────────────────────────────────────────────────────────
const METRIC_LABELS: Record<MetricKey, string> = {
  denied_actions:     'Denied Actions',
  escalated_actions:  'Escalated Actions',
  scope_drift:        'Scope Drift Detections',
  new_endpoints:      'New Endpoints',
  token_spend_usd:    'Token Spend (USD)',
  failed_deliveries:  'Failed Deliveries',
}

const WINDOW_OPTIONS: { value: number; label: string }[] = [
  { value: 5,    label: '5 min' },
  { value: 15,   label: '15 min' },
  { value: 60,   label: '1 hour' },
  { value: 1440, label: '24 hours' },
]

// ── Severity badge ────────────────────────────────────────────────────────────
type SeverityLevel = Alert['severity']

const SEVERITY_STYLES: Record<SeverityLevel, string> = {
  critical: 'bg-red-100 text-red-800',
  high:     'bg-orange-100 text-orange-800',
  warning:  'bg-amber-100 text-amber-800',
  info:     'bg-blue-100 text-blue-800',
}

function SeverityBadge({ severity }: { severity: SeverityLevel }) {
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-semibold uppercase tracking-wide ${SEVERITY_STYLES[severity]}`}
    >
      {severity}
    </span>
  )
}

// ── Relative time ─────────────────────────────────────────────────────────────
function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60_000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins} min${mins === 1 ? '' : 's'} ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

// ── Inline toast ──────────────────────────────────────────────────────────────
interface ToastProps {
  message: string
  onDismiss: () => void
}

function Toast({ message, onDismiss }: ToastProps) {
  return (
    <div className="fixed bottom-6 right-6 z-50 flex items-center gap-3 rounded-lg bg-gray-900 px-4 py-3 text-sm text-white shadow-lg max-w-sm">
      <span className="flex-1">{message}</span>
      <button
        onClick={onDismiss}
        className="text-gray-400 hover:text-white transition-colors shrink-0"
        aria-label="Dismiss"
      >
        ×
      </button>
    </div>
  )
}

// ── Rule form (slide-out panel) ───────────────────────────────────────────────
const ruleSchema = z.object({
  name:           z.string().min(1, 'Name is required'),
  metric:         z.enum([
    'denied_actions', 'escalated_actions', 'scope_drift',
    'new_endpoints', 'token_spend_usd', 'failed_deliveries',
  ] as const),
  threshold:      z.coerce.number().positive('Must be a positive number'),
  window_minutes: z.coerce.number().positive(),
  severity:       z.enum(['info', 'warning', 'high', 'critical'] as const),
})

type RuleFormValues = z.infer<typeof ruleSchema>

interface RuleFormPanelProps {
  editRule?: AlertRule
  onClose: () => void
  onSave: (values: RuleFormValues) => void
  isSaving: boolean
}

function RuleFormPanel({ editRule, onClose, onSave, isSaving }: RuleFormPanelProps) {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<RuleFormValues>({
    resolver: zodResolver(ruleSchema),
    defaultValues: editRule
      ? {
          name:           editRule.name,
          metric:         editRule.metric,
          threshold:      editRule.threshold,
          window_minutes: editRule.window_minutes,
          severity:       editRule.severity,
        }
      : { window_minutes: 60, severity: 'high' },
  })

  return (
    <>
      <div className="fixed inset-0 bg-black/30 z-40" onClick={onClose} aria-hidden="true" />
      <div className="fixed right-0 top-0 h-full w-full max-w-lg bg-white shadow-xl z-50 flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">
            {editRule ? 'Edit Alert Rule' : 'New Alert Rule'}
          </h2>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-gray-600 text-xl leading-none"
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <form
          onSubmit={handleSubmit(onSave)}
          className="flex-1 overflow-y-auto px-6 py-5 space-y-5"
        >
          {/* Rule name */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Rule name
            </label>
            <input
              {...register('name')}
              type="text"
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              placeholder="e.g. High deny rate in production"
            />
            {errors.name && (
              <p className="mt-1 text-xs text-red-600">{errors.name.message}</p>
            )}
          </div>

          {/* Metric */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Metric
            </label>
            <select
              {...register('metric')}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            >
              {(Object.entries(METRIC_LABELS) as [MetricKey, string][]).map(([k, v]) => (
                <option key={k} value={k}>{v}</option>
              ))}
            </select>
          </div>

          {/* Threshold + Time window (side by side) */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Threshold
              </label>
              <input
                {...register('threshold')}
                type="number"
                min={1}
                step="any"
                className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="5"
              />
              {errors.threshold && (
                <p className="mt-1 text-xs text-red-600">{errors.threshold.message}</p>
              )}
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Time window
              </label>
              <select
                {...register('window_minutes')}
                className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              >
                {WINDOW_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
            </div>
          </div>

          {/* Severity */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Severity
            </label>
            <select
              {...register('severity')}
              className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            >
              <option value="info">Info</option>
              <option value="warning">Warning</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>

          <div className="flex gap-3 pt-2">
            <button
              type="submit"
              disabled={isSaving}
              className="flex-1 rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 transition-colors"
            >
              {isSaving ? 'Saving…' : 'Save rule'}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="flex-1 rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 transition-colors"
            >
              Cancel
            </button>
          </div>
        </form>
      </div>
    </>
  )
}

// ── Active alerts tab ─────────────────────────────────────────────────────────
function ActiveAlertsTab() {
  const [resolveTarget, setResolveTarget] = useState<Alert | null>(null)
  const { data, isLoading, isFetching } = useAlerts({ resolved: false })
  const acknowledge = useAcknowledgeAlert()
  const resolve     = useResolveAlert()

  const alerts = data?.data ?? []

  const columns = [
    {
      key: 'severity',
      header: 'Severity',
      render: (row: Alert) => <SeverityBadge severity={row.severity} />,
    },
    {
      key: 'rule_name',
      header: 'Rule',
      render: (row: Alert) => (
        <span className="font-medium text-gray-900">{row.rule_name}</span>
      ),
    },
    {
      key: 'message',
      header: 'Triggered value',
      render: (row: Alert) => (
        <span className="text-sm text-gray-700 font-mono">{row.message}</span>
      ),
    },
    {
      key: 'fired_at',
      header: 'Fired',
      render: (row: Alert) => (
        <span className="text-sm text-gray-500">{timeAgo(row.fired_at)}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (row: Alert) => (
        <span
          className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
            row.status === 'acknowledged'
              ? 'bg-yellow-100 text-yellow-800'
              : row.status === 'resolved'
              ? 'bg-green-100 text-green-800'
              : 'bg-gray-100 text-gray-700'
          }`}
        >
          {row.status}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      render: (row: Alert) => (
        <div className="flex gap-2 justify-end">
          {row.status === 'open' && (
            <button
              onClick={(e) => { e.stopPropagation(); acknowledge.mutate(row.id) }}
              disabled={acknowledge.isPending}
              className="px-3 py-1 text-xs font-medium rounded border border-gray-300 text-gray-700 hover:bg-gray-50 disabled:opacity-50 transition-colors"
            >
              Acknowledge
            </button>
          )}
          {row.status !== 'resolved' && (
            <button
              onClick={(e) => { e.stopPropagation(); setResolveTarget(row) }}
              className="px-3 py-1 text-xs font-medium rounded bg-indigo-600 text-white hover:bg-indigo-700 transition-colors"
            >
              Resolve
            </button>
          )}
        </div>
      ),
    },
  ]

  return (
    <>
      {isLoading ? (
        <div className="flex justify-center py-12">
          <LoadingSpinner size="lg" />
        </div>
      ) : alerts.length === 0 ? (
        <EmptyState
          icon={<CheckCircle className="h-10 w-10" />}
          title="No active alerts — all clear"
          description="All systems are operating within policy thresholds."
        />
      ) : (
        <DataTable
          columns={columns}
          data={alerts}
          loading={isFetching}
        />
      )}
      {resolveTarget && (
        <ConfirmDialog
          open={true}
          title="Resolve this alert?"
          description={`Mark "${resolveTarget.rule_name}" as resolved. This does not change the underlying condition.`}
          confirmLabel="Resolve"
          isLoading={resolve.isPending}
          onConfirm={() =>
            resolve.mutate(resolveTarget.id, {
              onSettled: () => setResolveTarget(null),
            })
          }
          onCancel={() => setResolveTarget(null)}
        />
      )}
    </>
  )
}

// ── Alert rules tab ───────────────────────────────────────────────────────────
type RuleFormState = { open: false } | { open: true; rule?: AlertRule }

interface AlertRulesTabProps {
  formState: RuleFormState
  setFormState: (s: RuleFormState) => void
}

function AlertRulesTab({ formState, setFormState }: AlertRulesTabProps) {
  const [deleteTarget, setDeleteTarget] = useState<AlertRule | null>(null)
  const [toast, setToast] = useState<string | null>(null)

  const { data, isLoading, isFetching } = useAlertRules()
  const createRule = useCreateAlertRule()
  const updateRule = useUpdateAlertRule()
  const deleteRule = useDeleteAlertRule()
  const testRule   = useTestAlertRule()

  const rules = data?.data ?? []

  function handleSave(values: RuleFormValues) {
    const body: AlertRuleInput = values
    if (formState.open && formState.rule) {
      updateRule.mutate(
        { id: formState.rule.id, body },
        { onSuccess: () => setFormState({ open: false }) },
      )
    } else {
      createRule.mutate(body, {
        onSuccess: () => setFormState({ open: false }),
      })
    }
  }

  function handleTest(rule: AlertRule) {
    testRule.mutate(rule.id, {
      onSuccess: (result: TestRuleResult) => {
        const metricLabel = METRIC_LABELS[result.metric_key] ?? result.metric_key
        const msg = result.would_fire
          ? `Would fire: ${metricLabel} = ${result.current_value} (threshold: ${result.threshold})`
          : `Would not fire: ${metricLabel} = ${result.current_value} (threshold: ${result.threshold})`
        setToast(msg)
        window.setTimeout(() => setToast(null), 6000)
      },
    })
  }

  function conditionSummary(rule: AlertRule): string {
    const metric = METRIC_LABELS[rule.metric] ?? rule.metric
    const window = WINDOW_OPTIONS.find((o) => o.value === rule.window_minutes)?.label
      ?? `${rule.window_minutes}m`
    return `${metric} > ${rule.threshold} in ${window}`
  }

  const columns = [
    {
      key: 'name',
      header: 'Name',
      render: (row: AlertRule) => (
        <span className="font-medium text-gray-900">{row.name}</span>
      ),
    },
    {
      key: 'metric',
      header: 'Metric',
      render: (row: AlertRule) => METRIC_LABELS[row.metric] ?? row.metric,
    },
    {
      key: 'condition',
      header: 'Condition',
      render: (row: AlertRule) => (
        <span className="text-sm text-gray-600 font-mono">
          {conditionSummary(row)}
        </span>
      ),
    },
    {
      key: 'severity',
      header: 'Severity',
      render: (row: AlertRule) => <SeverityBadge severity={row.severity} />,
    },
    {
      key: 'enabled',
      header: 'Enabled',
      render: (row: AlertRule) => (
        <button
          role="switch"
          aria-checked={row.enabled}
          onClick={(e) => {
            e.stopPropagation()
            updateRule.mutate({ id: row.id, body: { enabled: !row.enabled } })
          }}
          className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-indigo-500 ${
            row.enabled ? 'bg-indigo-600' : 'bg-gray-200'
          }`}
        >
          <span
            className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform ${
              row.enabled ? 'translate-x-4' : 'translate-x-1'
            }`}
          />
        </button>
      ),
    },
    {
      key: 'actions',
      header: '',
      render: (row: AlertRule) => (
        <div className="flex gap-2 justify-end">
          <button
            onClick={(e) => { e.stopPropagation(); handleTest(row) }}
            disabled={testRule.isPending}
            className="px-3 py-1 text-xs font-medium rounded border border-gray-300 text-gray-700 hover:bg-gray-50 disabled:opacity-50 transition-colors"
          >
            Test
          </button>
          <button
            onClick={(e) => {
              e.stopPropagation()
              setFormState({ open: true, rule: row })
            }}
            className="px-3 py-1 text-xs font-medium rounded border border-gray-300 text-gray-700 hover:bg-gray-50 transition-colors"
          >
            Edit
          </button>
          <button
            onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}
            className="px-3 py-1 text-xs font-medium rounded border border-red-200 text-red-600 hover:bg-red-50 transition-colors"
          >
            Delete
          </button>
        </div>
      ),
    },
  ]

  const isSaving = createRule.isPending || updateRule.isPending

  return (
    <>
      {isLoading ? (
        <div className="flex justify-center py-12">
          <LoadingSpinner size="lg" />
        </div>
      ) : rules.length === 0 ? (
        <EmptyState
          icon={<Bell className="h-10 w-10" />}
          title="No alert rules"
          description="Create a rule to start monitoring your gateway for policy violations and anomalies."
          action={
            <button
              onClick={() => setFormState({ open: true })}
              className="mt-4 px-4 py-2 rounded-md bg-indigo-600 text-sm font-medium text-white hover:bg-indigo-700 transition-colors"
            >
              New rule
            </button>
          }
        />
      ) : (
        <DataTable
          columns={columns}
          data={rules}
          loading={isFetching}
        />
      )}

      {/* Delete confirm */}
      {deleteTarget && (
        <ConfirmDialog
          open={true}
          title={`Delete "${deleteTarget.name}"?`}
          description="This alert rule will be permanently removed. Existing fired alerts are not affected."
          confirmLabel="Delete"
          destructive
          isLoading={deleteRule.isPending}
          onConfirm={() =>
            deleteRule.mutate(deleteTarget.id, {
              onSettled: () => setDeleteTarget(null),
            })
          }
          onCancel={() => setDeleteTarget(null)}
        />
      )}

      {/* New / edit slide-out */}
      {formState.open && (
        <RuleFormPanel
          editRule={formState.rule}
          onClose={() => setFormState({ open: false })}
          onSave={handleSave}
          isSaving={isSaving}
        />
      )}

      {/* Test result toast */}
      {toast && <Toast message={toast} onDismiss={() => setToast(null)} />}
    </>
  )
}

// ── Page ─────────────────────────────────────────────────────────────────────
type Tab = 'alerts' | 'rules'

export default function AlertsPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const tab = (searchParams.get('tab') ?? 'alerts') as Tab
  const [ruleFormState, setRuleFormState] = useState<RuleFormState>({ open: false })

  function setTab(t: Tab) {
    setSearchParams(t === 'alerts' ? {} : { tab: t }, { replace: true })
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'alerts', label: 'Active Alerts' },
    { id: 'rules',  label: 'Alert Rules' },
  ]

  return (
    <div className="space-y-6">
      <PageHeader
        title="Alerts"
        subtitle="Monitor policy violations and anomalies across the gateway"
        actions={
          tab === 'rules' ? (
            <button
              onClick={() => setRuleFormState({ open: true })}
              className="px-4 py-2 rounded-md bg-indigo-600 text-sm font-medium text-white hover:bg-indigo-700 transition-colors"
            >
              New rule
            </button>
          ) : undefined
        }
      />

      {/* Tab bar */}
      <div className="border-b border-gray-200">
        <nav className="-mb-px flex gap-6">
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`pb-3 text-sm font-medium border-b-2 transition-colors ${
                tab === t.id
                  ? 'border-indigo-600 text-indigo-600'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
              }`}
            >
              {t.label}
            </button>
          ))}
        </nav>
      </div>

      {tab === 'alerts' ? (
        <ActiveAlertsTab />
      ) : (
        <AlertRulesTab
          formState={ruleFormState}
          setFormState={setRuleFormState}
        />
      )}
    </div>
  )
}
