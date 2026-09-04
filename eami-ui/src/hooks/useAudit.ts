import { useQuery, useMutation } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
import { STALE_TIMES } from '@/lib/query'
import type { components } from '@/api/schema'
import type { ToolDataHandling } from './useTools'

export interface AuditParams {
  agent_name?: string
  tool_name?: string
  decision?: 'allowed' | 'denied' | 'escalated'
  from?: string
  to?: string
  page?: number
  per_page?: number
}

// AuditEntry as generated from api/openapi.yaml doesn't include
// data_handling_designation (B-078's audit_log column, B-094's read path) --
// same undocumented-field situation as ToolWithActions in useTools.ts, and
// the same fix: extend the generated type locally rather than hand-editing
// schema.ts, which the real codegen would just overwrite anyway.
export type AuditEntry = components['schemas']['AuditEntry'] & {
  data_handling_designation?: ToolDataHandling
  // redacted_count (B-156/B-167): the number of sensitive items masked
  // before this dispatch's real outbound call -- a count only, never the
  // redacted content. undefined for every non-ai_provider call and every
  // row written before this migration -- a real 0 (redaction ran, found
  // nothing) is a different, meaningful value, not the same as absent.
  redacted_count?: number
}

export function useAudit(params?: AuditParams) {
  return useQuery({
    queryKey: ['audit', params],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/audit', {
        params: { query: params },
      })
      if (error) throw error
      return data
    },
    staleTime: STALE_TIMES.DEFAULT,
  })
}

// AuditVerifyResult mirrors eami-api/internal/store.AuditVerifyResult's JSON
// shape exactly (GET /v1/audit/verify isn't in api/openapi.yaml -- same
// ships-undocumented precedent as B-038/B-045/B-078).
export interface AuditVerifyResult {
  valid: boolean
  total_rows: number
  first_broken_at?: string
  message: string
  checked_from?: string
  checked_to?: string
}

// useVerifyAuditChainToEntry calls the real, already-shipped, already-tested
// GET /v1/audit/verify, bounded to walk the chain from genesis through one
// specific entry -- NOT the cheap, always-on per-entry self-consistency
// check (which never leaves the browser). This is the only call in this
// feature that can honestly back a "chain verified" claim; the panel must
// never present its result as interchangeable with the local check.
//
// KNOWN LIMITATION, disclosed not silently worked around: VerifyAuditChain's
// boundary query is `timestamp <= $to`, not an exact row match. Under
// concurrent writes, two rows could in principle share a timestamp precise
// enough to tie at the boundary, which could include a row written after
// the target entry in the walk. `entry.timestamp` is passed through exactly
// as received from the API (full precision, not reformatted) to make this
// as tight as the endpoint allows, but the theoretical tie case isn't
// eliminated -- fixing it would mean changing VerifyAuditChain itself,
// explicitly out of this brief's scope.
export function useVerifyAuditChainToEntry() {
  return useMutation({
    mutationFn: async (entryTimestamp: string) => {
      const url = '/v1/audit/verify?to=' + encodeURIComponent(entryTimestamp)
      return apiFetch<AuditVerifyResult>(url)
    },
  })
}
