import { NavLink } from 'react-router-dom'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { NAV_ITEMS, NAV_GROUPS } from './Navigation'
import { useUIStore } from '@/stores/uiStore'
import { usePendingApprovalCount } from '@/hooks/useApprovals'
import { Logo } from './Logo'

// B-200: real collapsible nav rail (DESIGN_SYSTEM.md §6/Layer 2). Reuses
// the EXISTING sidebarOpen/toggleSidebar wiring (useUIStore) unchanged --
// no new state. Before this brief, sidebarOpen=false rendered nothing at
// all (the sidebar fully disappeared); it now renders a real icon-only
// collapsed rail instead, the Material pattern the canvas actually shows.
// The toggle lives here, next to the logo, matching the canvas's own
// placement -- not in Topbar.tsx's hamburger, which isn't even rendered
// on 9 of 14 pages (including this brief's own AgentsPage/AgentDetailPage).
// Deliberately NOT persisted (matches sidebarOpen's existing non-persisted
// convention) -- see BACKLOG.md's B-200 entry for why that's a considered
// choice, not an oversight.
export function Sidebar() {
  const sidebarOpen = useUIStore((s) => s.sidebarOpen)
  const toggleSidebar = useUIStore((s) => s.toggleSidebar)
  const pendingApprovals = usePendingApprovalCount()

  if (!sidebarOpen) {
    return (
      <aside className="flex h-full w-[72px] flex-col items-center border-r border-gray-200 bg-white">
        {/* B-203: header height matches AppTopBar's h-[60px] exactly (see
            the expanded branch's own header below) so the collapsed
            rail's border-b lands on the same line as AppTopBar's border-b
            regardless of sidebar state -- not just the expanded case. */}
        <div className="flex h-[60px] w-full items-center justify-center border-b border-gray-200">
          <button
            onClick={toggleSidebar}
            aria-label="Expand navigation"
            className="flex h-7 w-7 items-center justify-center rounded-md border border-gray-200 text-ink-faint hover:bg-gray-50"
          >
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
        </div>
        <nav className="flex flex-1 flex-col items-center gap-1.5 overflow-y-auto overflow-x-hidden py-3">
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              title={item.label}
              className={({ isActive }) =>
                `relative flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg transition-colors ${
                  isActive ? 'bg-brand-600 text-white' : 'text-gray-500 hover:bg-gray-100'
                }`
              }
            >
              <item.icon className="h-[18px] w-[18px]" />
              {item.badgeKey === 'pendingApprovals' && pendingApprovals > 0 && (
                <span className="absolute -right-0.5 -top-0.5 h-2.5 w-2.5 rounded-full bg-red-500" />
              )}
            </NavLink>
          ))}
        </nav>
      </aside>
    )
  }

  return (
    <aside className="flex h-full w-60 flex-col border-r border-gray-200 bg-white">
      {/* Logo -- sourced entirely from branding/config.ts; the wordmark
          image already carries the product name, so no separate name
          text is rendered alongside it here (see BUILT.md).
          B-203: height is h-[60px], matching AppTopBar's own h-[60px]
          exactly (was h-14/56px -- a real, live-measured 4px mismatch
          against AppTopBar's border-b, since Sidebar and AppTopBar are
          independent flex siblings under AppShell.tsx sharing one visual
          seam, not two nested elements that would auto-align). */}
      <div className="flex h-[60px] items-center justify-between border-b border-gray-200 px-4">
        <Logo variant="full" className="h-6 w-auto" />
        <button
          onClick={toggleSidebar}
          aria-label="Collapse navigation"
          className="flex h-[26px] w-[26px] items-center justify-center rounded-md border border-gray-200 text-ink-faint hover:bg-gray-50"
        >
          <ChevronLeft className="h-3.5 w-3.5" />
        </button>
      </div>

      {/* Nav groups */}
      <nav className="flex-1 overflow-y-auto py-4">
        {NAV_GROUPS.map((group) => {
          const items = NAV_ITEMS.filter((i) => i.group === group.key)
          if (items.length === 0) return null
          return (
            <div key={group.key} className="mb-4">
              <p className="mb-1 px-4 text-2xs font-semibold uppercase tracking-widest text-gray-400">
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
                    <span className="rounded-full bg-red-500 px-1.5 py-0.5 text-2xs font-bold text-white">
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
