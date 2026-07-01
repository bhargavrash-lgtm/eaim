import { NavLink } from 'react-router-dom'
import { NAV_ITEMS, NAV_GROUPS } from './Navigation'
import { useUIStore } from '@/stores/uiStore'
import { usePendingApprovalCount } from '@/hooks/useApprovals'
import { Shield } from 'lucide-react'

export function Sidebar() {
  const sidebarOpen = useUIStore((s) => s.sidebarOpen)
  const pendingApprovals = usePendingApprovalCount()

  if (!sidebarOpen) return null

  return (
    <aside className="flex h-full w-60 flex-col border-r border-gray-200 bg-white">
      {/* Logo */}
      <div className="flex h-14 items-center gap-2 border-b border-gray-200 px-4">
        <Shield className="h-6 w-6 text-brand-600" />
        <span className="text-sm font-bold tracking-wide text-gray-900">EAMI</span>
      </div>

      {/* Nav groups */}
      <nav className="flex-1 overflow-y-auto py-4">
        {NAV_GROUPS.map((group) => {
          const items = NAV_ITEMS.filter((i) => i.group === group.key)
          if (items.length === 0) return null
          return (
            <div key={group.key} className="mb-4">
              <p className="mb-1 px-4 text-[10px] font-semibold uppercase tracking-widest text-gray-400">
                {group.label}
              </p>
              {items.map((item) => (
                <NavLink
                  key={item.path}
                  to={item.path}
                  className={({ isActive }) =>
                    `flex items-center gap-3 px-4 py-2 text-sm font-medium transition-colors ${
                      isActive
                        ? 'bg-brand-50 text-brand-700'
                        : 'text-gray-600 hover:bg-gray-50 hover:text-gray-900'
                    }`
                  }
                >
                  <item.icon className="h-4 w-4 flex-shrink-0" />
                  <span className="flex-1">{item.label}</span>
                  {item.badgeKey === 'pendingApprovals' && pendingApprovals > 0 && (
                    <span className="rounded-full bg-red-500 px-1.5 py-0.5 text-[10px] font-bold text-white">
                      {pendingApprovals > 99 ? '99+' : pendingApprovals}
                    </span>
                  )}
                </NavLink>
              ))}
            </div>
          )
        })}
      </nav>
    </aside>
  )
}
