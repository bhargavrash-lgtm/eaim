import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api/client'
import { STALE_TIMES } from '@/lib/query'

// Not yet in api/openapi.yaml (B-038, same gap as B-033's /v1/reports/
// paste-events) -- these use the apiFetch escape hatch documented in
// client.ts rather than the generated, typed `api.GET`, same as
// MemoryPage.tsx's episode hooks.

export interface PasteEvent {
  id: string
  destination_domain: string
  occurred_at: string
  content_length?: number | null
  content_hash?: string | null
  os_username?: string | null
}

export interface PasteEventListResponse {
  data: PasteEvent[]
  meta: { total: number; page: number; per_page: number }
}

export interface PasteEventListParams {
  domain?: string
  from?: string
  to?: string
  page?: number
  per_page?: number
}

export function usePasteEvents(params: PasteEventListParams) {
  return useQuery<PasteEventListResponse>({
    queryKey: ['paste-events', params],
    queryFn: () => {
      const q = new URLSearchParams()
      if (params.domain) q.set('domain', params.domain)
      if (params.from) q.set('from', params.from)
      if (params.to) q.set('to', params.to)
      q.set('page', String(params.page ?? 1))
      q.set('per_page', String(params.per_page ?? 50))
      return apiFetch(`/v1/paste-events?${q}`)
    },
    staleTime: STALE_TIMES.DEFAULT,
  })
}

export interface PasteEventDomainPoint {
  bucket: string
  domain: string
  count: number
}

export interface PasteEventTimeSeries {
  granularity: string
  series: PasteEventDomainPoint[]
}

export function usePasteEventsTimeSeries(
  from: string,
  to: string,
  granularity: 'hour' | 'day' | 'week' = 'day',
  domain?: string,
) {
  return useQuery<PasteEventTimeSeries>({
    queryKey: ['paste-events', 'timeseries', from, to, granularity, domain],
    queryFn: () => {
      const q = new URLSearchParams({ from, to, granularity })
      if (domain) q.set('domain', domain)
      return apiFetch(`/v1/paste-events/timeseries?${q}`)
    },
    staleTime: STALE_TIMES.DEFAULT,
    enabled: Boolean(from && to),
  })
}
