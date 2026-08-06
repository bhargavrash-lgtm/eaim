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

// apiFetch — lightweight fetch helper for endpoints not yet in the OpenAPI schema.
// Automatically injects the Bearer token from the auth store.
//
// method/body (B-045) are optional so existing GET-only call sites are
// unaffected -- generic, not tied to any one resource, so future
// undocumented-endpoint write flows (edit forms on other pages, same
// situation Tools hit here) can reuse this instead of a raw fetch/axios
// call, which CLAUDE.md's frontend-API-access convention disallows in
// components.
export async function apiFetch<T = unknown>(
  path: string,
  options?: { method?: string; body?: unknown },
): Promise<T> {
  const { accessToken } = useAuthStore.getState()
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (accessToken) headers['Authorization'] = `Bearer ${accessToken}`
  const res = await fetch(path, {
    method: options?.method ?? 'GET',
    headers,
    body: options?.body !== undefined ? JSON.stringify(options.body) : undefined,
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  // PATCH/DELETE-style endpoints may return 204 No Content -- res.json()
  // would throw on an empty body, so only parse when there's content.
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}
