import { type ReactNode } from 'react'
import { Bell, Menu, LogOut } from 'lucide-react'
import { useUIStore } from '@/stores/uiStore'
import { useAuthStore } from '@/stores/authStore'
import { useNavigate } from 'react-router-dom'

interface TopbarProps {
  title: string
  subtitle?: string
  actions?: ReactNode
}

export function Topbar({ title, subtitle, actions }: TopbarProps) {
  const toggleSidebar = useUIStore((s) => s.toggleSidebar)
  const { user, logout } = useAuthStore()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    navigate('/login')
  }

  return (
    <header className="flex h-14 items-center justify-between border-b border-gray-200 bg-white px-4">
      <div className="flex items-center gap-3">
        <button
          onClick={toggleSidebar}
          className="rounded p-1 text-gray-500 hover:bg-gray-100"
          aria-label="Toggle sidebar"
        >
          <Menu className="h-5 w-5" />
        </button>
        <div>
          <h1 className="text-sm font-semibold text-gray-900">{title}</h1>
          {subtitle && <p className="text-xs text-gray-500">{subtitle}</p>}
        </div>
      </div>

      <div className="flex items-center gap-3">
        {actions}
        <button
          className="rounded p-1 text-gray-500 hover:bg-gray-100"
          aria-label="Notifications"
        >
          <Bell className="h-5 w-5" />
        </button>
        <div className="flex items-center gap-2">
          <span className="text-xs text-gray-600">{user?.email}</span>
          <button
            onClick={handleLogout}
            className="rounded p-1 text-gray-500 hover:bg-gray-100"
            aria-label="Log out"
          >
            <LogOut className="h-4 w-4" />
          </button>
        </div>
      </div>
    </header>
  )
}
