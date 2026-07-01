import { create } from 'zustand'

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

export const useAuthStore = create<AuthState>((set) => ({
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
}))
