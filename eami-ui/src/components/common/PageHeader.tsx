import { type ReactNode } from 'react'

// B-201 Phase 2, Option B: title is now optional and mid-migration --
// AppTopBar's breadcrumb (components/layout/AppTopBar.tsx) is the real
// <h1> for every page that has adopted it; PageHeader's own title
// rendering stays working (still an <h1> when passed) purely so
// WorkflowCanvasPage (Batch 4, the last real caller) isn't broken before
// its own turn. Once Batch 4 migrates it too, `title` and its rendering
// will be removed entirely -- do not add a new caller passing `title`
// in the meantime.
interface PageHeaderProps {
  title?: string
  subtitle?: string
  actions?: ReactNode
}

export function PageHeader({ title, subtitle, actions }: PageHeaderProps) {
  return (
    <div className="flex items-start justify-between border-b border-gray-200 bg-white px-6 py-4">
      <div>
        {title && <h1 className="text-lg font-semibold text-gray-900">{title}</h1>}
        {subtitle && <p className="mt-0.5 text-sm text-gray-500">{subtitle}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}
