import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch, ApiFetchError } from '@/api/client'

// Modular licensing & entitlement system, Brief 1 of 3 (B-157 epic, built
// as B-169). GET/POST /v1/settings/license aren't in api/openapi.yaml yet
// (Architect-EAMI-owned, out of this brief's scope) -- apiFetch, the
// documented escape hatch (client.ts), same established precedent as
// useTools.ts's useUpdateTool/useDiscoverOpenAPI.
export type LicenseStatus = 'active' | 'expired'

export interface License {
  modules: string[]
  valid_from: string
  valid_until: string
  status: LicenseStatus
}

// useLicense returns undefined (not an error state) when the org has
// never uploaded a license at all -- the real backend 404 for that case
// is treated as "no license yet", not a fetch failure, so the Settings
// page can render an empty/upload-prompt state rather than an error banner.
export function useLicense() {
  return useQuery({
    queryKey: ['settings', 'license'],
    queryFn: async () => {
      try {
        return await apiFetch<License>('/v1/settings/license', { method: 'GET' })
      } catch (err) {
        if (err instanceof ApiFetchError && err.status === 404) {
          return null
        }
        throw err
      }
    },
  })
}

export function useUploadLicense() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (rawLicense: string) => {
      return apiFetch<License>('/v1/settings/license', {
        method: 'POST',
        body: { raw_license: rawLicense },
      })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings', 'license'] }),
  })
}
