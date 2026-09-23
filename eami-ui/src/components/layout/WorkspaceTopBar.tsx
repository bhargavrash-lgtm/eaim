// WorkspaceTopBar.tsx -- Workspace mode's own minimal top bar, per-page
// like AppTopBar (Admin mode) already is -- rendered by each workspace
// page, not baked into WorkspaceShell, same convention. Deliberately
// simpler than AppTopBar: no search/notifications chrome (Workspace mode
// is "restricted, calm, few numbers," DESIGN_SYSTEM.md §0) -- just the
// page title, matching the Layer4 canvas mockup's own top bar shape.
import type { ReactNode } from 'react'

export function WorkspaceTopBar({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <header className="flex h-[60px] items-center justify-between border-b border-gray-200 bg-white px-8 shadow-l2">
      <span className="text-sm font-semibold text-ink">{title}</span>
      {action}
    </header>
  )
}
