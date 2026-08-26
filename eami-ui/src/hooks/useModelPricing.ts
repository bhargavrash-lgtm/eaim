import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '@/api/client'

// Model pricing admin CRUD (B-112) -- /v1/admin/model-pricing isn't yet in
// api/openapi.yaml (Architect-EAMI-owned, out of this session's file
// boundary), so every hook here uses apiFetch(), the documented escape
// hatch (client.ts), not the generated client -- same precedent as
// useWorkflows.ts/B-108's ToolSpend deviation.
//
// The 3 cache-rate fields (B-111) are optional/undefined when a model has
// no configured rate (a non-Claude model, or a Claude model an admin
// hasn't backfilled yet) -- distinguishing "priced at $0" from "no rate
// set" the same way the API response does (omitempty, not a literal 0).
export type ModelPricing = {
  model: string
  cost_per_1k_in: number
  cost_per_1k_out: number
  cost_per_1k_cache_write_5m?: number
  cost_per_1k_cache_write_1h?: number
  cost_per_1k_cache_read?: number
  updated_at: string
}

export type ModelPricingCreate = {
  model: string
  cost_per_1k_in: number
  cost_per_1k_out: number
  cost_per_1k_cache_write_5m?: number
  cost_per_1k_cache_write_1h?: number
  cost_per_1k_cache_read?: number
}

// Every field optional -- PATCH semantics, an absent field leaves that
// column unchanged (mirrors WorkflowUpdate's omitted-vs-present convention).
export type ModelPricingUpdate = Partial<Omit<ModelPricingCreate, 'model'>>

export function useModelPricing() {
  return useQuery({
    queryKey: ['model-pricing'],
    queryFn: () => apiFetch<{ data: ModelPricing[] }>('/v1/admin/model-pricing'),
    staleTime: 30_000,
  })
}

export function useCreateModelPricing() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: ModelPricingCreate) =>
      apiFetch<ModelPricing>('/v1/admin/model-pricing', { method: 'POST', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['model-pricing'] }),
  })
}

export function useUpdateModelPricing() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ model, body }: { model: string; body: ModelPricingUpdate }) =>
      apiFetch<ModelPricing>(`/v1/admin/model-pricing/${encodeURIComponent(model)}`, {
        method: 'PATCH',
        body,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['model-pricing'] }),
  })
}

export function useDeleteModelPricing() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (model: string) =>
      apiFetch<void>(`/v1/admin/model-pricing/${encodeURIComponent(model)}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['model-pricing'] }),
  })
}
