import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { STALE_TIMES } from '@/lib/query'
import type { components } from '@/api/schema'

// ── Types ─────────────────────────────────────────────────────────────────────

export type AlertStatus = 'open' | 'acknowledged' | 'resolved'

export type MetricKey =
  | 'denied_actions'
  | 'escalated_actions'
  | 'scope_drift'
  | 'new_endpoints'
  | 'token_spend_usd'
  | 'failed_deliveries'

export type Alert = components['schemas']['Alert']

export type AlertRule = components['schemas']['AlertRule'] & {
  metric: MetricKey
  threshold: number
  window_minutes: number
}

/**
 * All fields are optional so that Zod's z.infer output (which marks every
 * field optional via addQuestionMarks<T, any>) is assignable to this type.
 * Values are validated by the form before the mutation fires, so non-null
 * assertions in the mapping helpers are safe.
 */
export type AlertRuleInput = {
  name?: string
  metric?: MetricKey
  threshold?: number
  window_minutes?: number
  severity?: 'info' | 'warning' | 'high' | 'critical'
  enabled?: boolean
}

export type TestRuleResult = {
  would_fire: boolean
  metric_value: number
  metric_key: MetricKey
  current_value: number
  threshold: number
}

// ── API mapping helpers ───────────────────────────────────────────────────────

type ApiMetric   = components['schemas']['AlertRuleCreate']['metric']
type ApiSeverity = components['schemas']['AlertRuleCreate']['severity']

const METRIC_TO_API: Record<MetricKey, ApiMetric> = {
  denied_actions:    'denied_actions_count',
  escalated_actions: 'escalated_actions_count',
  scope_drift:       'scope_drift_count',
  new_endpoints:     'new_endpoints_count',
  token_spend_usd:   'token_spend_usd',
  failed_deliveries: 'failed_delivery_count',
}

const SEVERITY_TO_API: Record<NonNullable<AlertRuleInput['severity']>, ApiSeverity> = {
  info:     'low',
  warning:  'medium',
  high:     'high',
  critical: 'critical',
}

function toApiCreate(input: AlertRuleInput): components['schemas']['AlertRuleCreate'] {
  return {
    name:      input.name!,
    metric:    METRIC_TO_API[input.metric!],
    condition: 'gt',
    threshold: input.threshold!,
    window:    input.window_minutes! as 5 | 15 | 60 | 1440,
    severity:  SEVERITY_TO_API[input.severity!],
    enabled:   input.enabled ?? true,
  }
}

function toApiUpdate(input: Partial<AlertRuleInput>): components['schemas']['AlertRuleUpdate'] {
  const patch: components['schemas']['AlertRuleUpdate'] = {}
  if (input.name !== undefined)           patch.name      = input.name
  if (input.metric !== undefined)         patch.metric    = METRIC_TO_API[input.metric]
  if (input.threshold !== undefined)      patch.threshold = input.threshold
  if (input.window_minutes !== undefined) patch.window    = input.window_minutes as 5 | 15 | 60 | 1440
  if (input.severity !== undefined)       patch.severity  = SEVERITY_TO_API[input.severity]
  if (input.enabled !== undefined)        patch.enabled   = input.enabled
  return patch
}

// ── Query hooks ───────────────────────────────────────────────────────────────

interface AlertParams {
  severity?: Alert['severity']
  status?:   AlertStatus
  from?:     string
  to?:       string
  resolved?: boolean
}

export function useAlerts(params?: AlertParams) {
  return useQuery({
    queryKey: ['alerts', params],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/alerts', {
        params: { query: params },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.ALERTS,
  })
}

export function useAlertRules() {
  return useQuery({
    queryKey: ['alert-rules'],
    queryFn: async (): Promise<{ data?: AlertRule[] }> => {
      const { data, error } = await api.GET('/v1/alerts/rules', {})
      if (error) throw error
      // Cast: API schema omits metric/threshold/window_minutes from AlertRule;
      // the server populates them and the UI depends on them.
      return data as { data?: AlertRule[] }
    },
    staleTime: STALE_TIMES.DEFAULT,
  })
}

// ── Mutation hooks ────────────────────────────────────────────────────────────

export function useAcknowledgeAlert() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      api.POST('/v1/alerts/{id}/acknowledge', { params: { path: { id } } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alerts'] }),
  })
}

export function useResolveAlert() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      api.POST('/v1/alerts/{id}/resolve', { params: { path: { id } } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alerts'] }),
  })
}

export function useCreateAlertRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: AlertRuleInput) =>
      api.POST('/v1/alerts/rules', { body: toApiCreate(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
}

export function useUpdateAlertRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<AlertRuleInput> }) =>
      api.PUT('/v1/alerts/rules/{id}', { params: { path: { id } }, body: toApiUpdate(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
}

export function useDeleteAlertRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      api.DELETE('/v1/alerts/rules/{id}', { params: { path: { id } } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
}

export function useTestAlertRule() {
  return useMutation({
    mutationFn: async (id: string): Promise<TestRuleResult> => {
      const { data, error } = await api.POST('/v1/alerts/rules/{id}/test', {
        params: { path: { id } },
      })
      if (error) throw error
      if (!data) throw new Error('No data returned from test rule')
      // Single narrowing cast: TestRuleResult extends the API response shape,
      // server returns the full object including metric_key/current_value/threshold.
      return data as TestRuleResult
    },
  })
}
