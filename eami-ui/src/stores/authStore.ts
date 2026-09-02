import { create } from 'zustand'
import { persist, createJSONStorage } from 'zustand/middleware'

interface User {
  id: string
  email: string
  name?: string
  role: 'admin' | 'operator' | 'viewer'
  org_id: string
}

interface AuthState {
  accessToken: string | null
  refreshToken: string | null
  user: User | null
  isAuthenticated: boolean

  setTokens: (accessToken: string, refreshToken: string) => void
  setUser: (user: User) => void
  logout: () => void
}

// B-146: session persistence, localStorage vs. HttpOnly cookie.
// Chose localStorage (via Zustand's `persist`) because an HttpOnly cookie
// would need eami-api changes (Set-Cookie on login/refresh, CORS/SameSite
// rework across the Vite dev-proxy vs. eami-proxy topologies) that this
// fix's own scope explicitly forbids -- not a preference, a hard boundary.
// Real tradeoff, not glossed over: a successful XSS on this origin could
// read the token from localStorage. Mitigations actually verified in this
// codebase, not assumed: zero `dangerouslySetInnerHTML` usage repo-wide,
// and B-118's short access-token TTL (900s default) bounds how long a
// stolen access token stays useful. Neither eliminates the risk. Migrating
// to an HttpOnly cookie is a real, separate, backend-touching future brief
// if this tradeoff becomes unacceptable.
//
// No client-side pre-emptive expiry check on rehydration: eami-api's
// refresh_token is a cryptographically random opaque value (auth.go's
// IssueRefreshToken, hex.EncodeToString over crypto/rand), not a JWT --
// confirmed live, there is no client-decodable `exp` claim to check
// (accessToken is a real JWT, but gating on ITS expiry would force a
// login on every routine reload after its short TTL, even when a valid
// refresh token could silently repair the session). A session whose
// refresh token has actually died server-side rehydrates optimistically
// and is corrected on the very next API call by the existing
// 401->refresh->logout() flow (lib/auth.ts, untouched by this fix) --
// the only mechanism that can actually know, since that's a server-side
// fact.
const STORAGE_KEY = 'eami-auth'

// B-146 AC2 (redirect back to the originally-requested URL after login).
// AppShell.tsx (the route guard) and router.tsx are out of this fix's
// MAY-MODIFY scope, so there is no way to have the guard pass the
// attempted path via router state. Capturing window.location at this
// module's first evaluation works instead: on a hard navigation (refresh,
// bookmark, pasted/shared URL -- exactly B-146's scenario), this module
// loads before React Router has rendered anything, so the browser's URL
// is still the one the user actually asked for.
const REDIRECT_EXEMPT_PATHS = new Set(['/login', '/setup', '/'])

let capturedRedirectPath: string | null = null
if (typeof window !== 'undefined') {
  const { pathname, search } = window.location
  if (!REDIRECT_EXEMPT_PATHS.has(pathname)) {
    capturedRedirectPath = pathname + search
  }
}

export function consumeRedirectPath(): string | null {
  const path = capturedRedirectPath
  capturedRedirectPath = null
  return path
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      refreshToken: null,
      user: null,
      isAuthenticated: false,

      setTokens: (accessToken, refreshToken) =>
        set({ accessToken, refreshToken, isAuthenticated: true }),

      setUser: (user) => set({ user }),

      logout: () =>
        set({
          accessToken: null,
          refreshToken: null,
          user: null,
          isAuthenticated: false,
        }),
    }),
    {
      name: STORAGE_KEY,
      storage: createJSONStorage(() => localStorage),
    },
  ),
)
