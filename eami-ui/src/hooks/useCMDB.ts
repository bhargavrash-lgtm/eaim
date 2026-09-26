import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, apiFetch } from '@/api/client'
import type { components } from '@/api/schema'

export type CMDBAsset = components['schemas']['CMDBAsset']
export type CMDBCategory = components['schemas']['CMDBCategory']
export type CMDBType = components['schemas']['CMDBType']
export type CMDBAssetKind = components['schemas']['CMDBAssetKind']
export type CMDBCategoryWrite = components['schemas']['CMDBCategoryWrite']
export type CMDBTypeWrite = components['schemas']['CMDBTypeWrite']

export type CMDBAssetParams = {
  page?: number
  per_page?: number
  kind?: CMDBAssetKind
  category_id?: string
  type_id?: string
  workspace_id?: string
  q?: string
}

export function useCMDBClassifications() {
  return useQuery({
    queryKey: ['cmdb-classifications'],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/cmdb/classifications')
      if (error) throw error
      return data
    },
  })
}

export function useCMDBAssets(params: CMDBAssetParams) {
  return useQuery({
    queryKey: ['cmdb-assets', params],
    queryFn: async () => {
      const { data, error } = await api.GET('/v1/cmdb/assets', { params: { query: params } })
      if (error) throw error
      return data
    },
  })
}

export function useCMDBWorkspaces() {
  return useQuery({
    queryKey: ['workspaces'],
    queryFn: () => apiFetch<{ data: { id: string; name: string }[] }>('/v1/workspaces'),
  })
}

function useCMDBMutation<TVariables>(mutationFn: (variables: TVariables) => Promise<unknown>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['cmdb-classifications'] })
      qc.invalidateQueries({ queryKey: ['cmdb-assets'] })
    },
  })
}

export function useCreateCMDBCategory() {
  return useCMDBMutation(async (body: CMDBCategoryWrite) => {
    const { error } = await api.POST('/v1/cmdb/categories', { body })
    if (error) throw error
  })
}

export function useUpdateCMDBCategory() {
  return useCMDBMutation(async ({ id, body }: { id: string; body: CMDBCategoryWrite }) => {
    const { error } = await api.PATCH('/v1/cmdb/categories/{categoryId}', { params: { path: { categoryId: id } }, body })
    if (error) throw error
  })
}

export function useDeleteCMDBCategory() {
  return useCMDBMutation(async (id: string) => {
    const { error } = await api.DELETE('/v1/cmdb/categories/{categoryId}', { params: { path: { categoryId: id } } })
    if (error) throw error
  })
}

export function useCreateCMDBType() {
  return useCMDBMutation(async (body: CMDBTypeWrite) => {
    const { error } = await api.POST('/v1/cmdb/types', { body })
    if (error) throw error
  })
}

export function useUpdateCMDBType() {
  return useCMDBMutation(async ({ id, body }: { id: string; body: CMDBTypeWrite }) => {
    const { error } = await api.PATCH('/v1/cmdb/types/{typeId}', { params: { path: { typeId: id } }, body })
    if (error) throw error
  })
}

export function useDeleteCMDBType() {
  return useCMDBMutation(async (id: string) => {
    const { error } = await api.DELETE('/v1/cmdb/types/{typeId}', { params: { path: { typeId: id } } })
    if (error) throw error
  })
}

export function useSetCMDBAssetClassification() {
  return useCMDBMutation(async ({ kind, id, ciTypeId }: { kind: CMDBAssetKind; id: string; ciTypeId: string | null }) => {
    const { error } = await api.PATCH('/v1/cmdb/assets/{assetKind}/{assetId}/classification', {
      params: { path: { assetKind: kind, assetId: id } },
      body: { ci_type_id: ciTypeId },
    })
    if (error) throw error
  })
}
