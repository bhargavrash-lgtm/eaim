import { type ReactNode } from 'react'

// B-201 Phase 2, Option B, finalized (Batch 4): PageHeader is now
// permanently a subtitle-and-actions-only bar. Its own `title`/<h1>
// rendering was removed here -- AppTopBar's breadcrumb (components/
// layout/AppTopBar.tsx) is the real <h1> for every page in the app now;
// WorkflowCanvasPage (Batch 4) was confirmed as the last real caller
// still passing `title` before this cleanup, via a real grep across
// every PageHeader call site in src/, not assumed.
interface PageHeaderProps {
  subtitle?: string
  actions?: ReactNode
}

export function PageHeader({ subtitle, actions }: PageHeaderProps) {
  return (
    <div className="flex items-start justify-between border-b border-gray-200 bg-white px-6 py-4">
      {subtitle && <p className="text-sm text-gray-500">{subtitle}</p>}
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}
