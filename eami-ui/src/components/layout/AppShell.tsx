import { Outlet, Navigate } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { useAuthStore } from '@/stores/authStore'

export function AppShell() {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }

  return (
    <div className="flex h-screen overflow-hidden bg-gray-50">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Page content renders via <Outlet> — each page renders its own Topbar */}
        <main className="flex-1 overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
