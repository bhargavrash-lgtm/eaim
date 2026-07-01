import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type ApprovalRequest = components['schemas']['ApprovalRequest']
export type ApprovalDecision = components['schemas']['ApprovalDecision']

export interface ApprovalsParams {
  status?: 'pending' | 'approved' | 'denied' | 'expired' | 'cancelled'
  risk_level?: 'low' | 'medium' | 'high' | 'critical'
  page?: number
  per_page?: number
}

interface DecideParams {
  id: string
  decision: 'approved' | 'denied'
  reason?: string
}

// Shape of every paginated approvals response
interface ApprovalsResponse {
  data: ApprovalRequest[]
  meta: { total: number; page: number; per_page: number }
}

export function useApprovals(params?: ApprovalsParams) {
  return useQuery({
    queryKey: ['approvals', params],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/approvals', {
        params: { query: params },
      })
      if (error) throw error
      return data as ApprovalsResponse
    },
    staleTime: 5_000,
    refetchInterval: 5_000,
  })
}

export function usePendingApprovalCount() {
  const { data } = useApprovals({ status: 'pending' })
  return data?.meta?.total ?? 0
}

export function useDecideApproval() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, decision, reason }: DecideParams) => {
      const { data, error } = await api.POST('/v1/approvals/{approvalId}/decide', {
        params: { path: { approvalId: id } },
        body: { decision, reason },
      })
      if (error) throw error
      return data
    },

    // Optimistically remove the decided approval from every cached approvals list
    // so operators see instant feedback without waiting for the next 5s poll.
    onMutate: async ({ id }) => {
      await qc.cancelQueries({ queryKey: ['approvals'] })

      // Snapshot all matching cache entries for rollback on error
      const previousApprovals = qc.getQueriesData<ApprovalsResponse>({
        queryKey: ['approvals'],
      })

      qc.setQueriesData<ApprovalsResponse>({ queryKey: ['approvals'] }, (old) => {
        if (!old) return old
        return {
          ...old,
          data: old.data.filter((a) => a.id !== id),
          meta: { ...old.meta, total: Math.max(0, old.meta.total - 1) },
        }
      })

      return { previousApprovals }
    },

    // Roll back the optimistic update if the mutation fails (e.g. 409 double-decide)
    onError: (_err, _vars, context) => {
      if (context?.previousApprovals) {
        for (const [queryKey, queryData] of context.previousApprovals) {
          qc.setQueryData(queryKey, queryData)
        }
      }
    },

    // Always refetch after settle so the server state wins
    onSettled: () => qc.invalidateQueries({ queryKey: ['approvals'] }),
  })
}
