import createClient from 'openapi-fetch'
import type { paths } from './schema'
import { useAuthStore } from '@/stores/authStore'

// baseUrl is intentionally empty: all API calls use relative paths (/v1/...)
// The Vite dev-server proxy (vite.config.ts) forwards them to eami-api internally.
// VITE_API_URL is only used server-side in vite.config.ts as the proxy target.
export const api = createClient<paths>({
  baseUrl: '',
  headers: {
    'Content-Type': 'application/json',
  },
})

// Inject Bearer token from auth store on every request
api.use({
  onRequest(context) {
    const { accessToken } = useAuthStore.getState()
    if (accessToken) {
      context.request.headers.set('Authorization', `Bearer ${accessToken}`)
    }
    return context.request
  },
})
