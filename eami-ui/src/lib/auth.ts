import { api } from '@/api/client'
import { useAuthStore } from '@/stores/authStore'

/**
 * Attempt to refresh the access token using the stored refresh token.
 * Called by the openapi-fetch middleware on 401.
 * Returns true if successful, false otherwise.
 */
export async function attemptTokenRefresh(): Promise<boolean> {
  const { refreshToken, logout } = useAuthStore.getState()
  if (!refreshToken) {
    logout()
    return false
  }

  try {
    const { data, error } = await api.POST('/v1/auth/refresh', {
      body: { refresh_token: refreshToken },
    })

    if (error || !data) {
      logout()
      return false
    }

    useAuthStore.getState().setTokens(data.access_token, data.refresh_token)
    return true
  } catch {
    logout()
    return false
  }
}

/**
 * Install a 401→refresh middleware on the API client.
 * Call once at app startup (main.tsx).
 */
export function setupAuthRefresh(): void {
  api.use({
    async onResponse(context) {
      if (context.response.status === 401) {
        const refreshed = await attemptTokenRefresh()
        if (refreshed) {
          const { accessToken } = useAuthStore.getState()
          const retryReq = context.request.clone()
          if (accessToken) {
            retryReq.headers.set('Authorization', `Bearer ${accessToken}`)
          }
          return fetch(retryReq)
        }
      }
      return context.response
    },
  })
}
