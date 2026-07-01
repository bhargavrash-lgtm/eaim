import { create } from 'zustand'

type Theme = 'light' | 'dark'

interface UIState {
  sidebarOpen: boolean
  theme: Theme

  setSidebarOpen: (open: boolean) => void
  toggleSidebar: () => void
  setTheme: (theme: Theme) => void
}

export const useUIStore = create<UIState>((set) => ({
  sidebarOpen: true,
  theme: 'light',

  setSidebarOpen: (open) => set({ sidebarOpen: open }),
  toggleSidebar: () => set((s) => ({ sidebarOpen: !s.sidebarOpen })),
  setTheme: (theme) => set({ theme }),
}))
