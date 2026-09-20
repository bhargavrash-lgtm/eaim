import { type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Search, Bell } from 'lucide-react'
import { UserMenu } from '@/components/common/UserMenu'

// B-201 Phase 2: the shared top bar (DESIGN_SYSTEM.md §6), extracted from
// AgentDetailPage.tsx's already-proven implementation (B-200) -- reused
// verbatim, not rebuilt. Supersedes Topbar.tsx (deleted once every real
// caller has migrated off it, end of Batch 2) and PageHeader.tsx's own
// title rendering (Option B: PageHeader keeps its subtitle/actions role,
// slimmed once every real caller has migrated, end of Batch 4).
//
// Search/notifications render as explicitly-disabled chrome, identically
// on every page -- confirmed by the B-201 Part A audit that neither is
// real functionality anywhere in this codebase (Topbar.tsx's own bell was
// already a decorative no-op; no search endpoint of any kind exists).
// UserMenu (Phase 1) is reused directly, not reimplemented -- this
// component owns zero logout logic of its own.
//
// breadcrumb is an ordered list of segments; only non-last segments may
// carry an href (they render as real links back up the hierarchy). A
// single-segment breadcrumb (e.g. [{label: 'Policies'}]) renders as a
// plain bold title with no separator, covering every flat list page --
// the same prop shape handles both cases, no separate "title" prop needed.
export interface BreadcrumbSegment {
  label: string
  href?: string
}

interface AppTopBarProps {
  breadcrumb: BreadcrumbSegment[]
  // The page's own real contextual action (an "+ Add X" button, a "Save
  // changes" button, etc.) -- omitted entirely, never a placeholder,
  // for a page that genuinely has none. DESIGN_SYSTEM.md §6: "never
  // force an irrelevant action onto a page that doesn't need one."
  action?: ReactNode
}

export function AppTopBar({ breadcrumb, action }: AppTopBarProps) {
  return (
    <header className="flex h-[60px] items-center justify-between border-b border-gray-200 bg-white px-8 shadow-l2">
      <div className="flex items-center gap-2.5 text-sm text-ink-faint">
        {breadcrumb.map((seg, i) => {
          const isLast = i === breadcrumb.length - 1
          return (
            <div key={`${seg.label}-${i}`} className="flex items-center gap-2.5">
              {i > 0 && <span>/</span>}
              {seg.href ? (
                <Link to={seg.href} className="font-medium text-ink-faint hover:text-ink">{seg.label}</Link>
              ) : isLast ? (
                // Real heading element, not a <span> -- every migrated
                // page's real <h1> now lives here (the same accessibility
                // fix already applied to AgentDetailPage's own title,
                // reapplied here so no page loses it during migration).
                <h1 className="font-semibold text-ink text-sm">{seg.label}</h1>
              ) : (
                <span className="font-medium text-ink-faint">{seg.label}</span>
              )}
            </div>
          )
        })}
      </div>
      <div className="flex items-center gap-3.5">
        <div
          className="flex items-center gap-2 rounded-lg bg-gray-100 px-3 py-1.5 text-gray-400"
          title="Global search — not built yet (chrome only, not wired to a real search endpoint)"
        >
          <Search className="h-[15px] w-[15px]" />
          <span className="text-xs">Search…</span>
        </div>
        <button
          className="cursor-not-allowed rounded-lg p-2 text-gray-400"
          title="Notifications — not built yet (chrome only, no real feed exists)"
          disabled
        >
          <Bell className="h-[19px] w-[19px]" />
        </button>
        {action}
        <UserMenu />
      </div>
    </header>
  )
}
