import { X } from 'lucide-react'
import { SlideOverPanel } from '@/components/common/SlideOverPanel'
import { StatusPill } from '@/components/common/StatusPill'
import { AssetWorkspaceBadge } from './AssetWorkspaceBadge'
import type { ToolWithActions } from '@/hooks/useTools'

// ToolAssetPanel -- B-196 increment 1's real tool detail view.
//
// Deliberately read-only (no edit form -- that's ToolsPage.tsx's own
// EditToolPanel, out of this increment's scope) and deliberately has no
// connections/relationship section: gateway_tools carries no real
// relationship data today (confirmed in this brief's own Part B
// investigation) -- rendering an empty "Connections" section here would
// imply one exists and just happens to be empty, which isn't true. The
// task brief is explicit: "no fabricated edges for tools/nodes that have
// no real relationship data."
export function ToolAssetPanel({ tool, onClose }: { tool: ToolWithActions; onClose: () => void }) {
  return (
    <SlideOverPanel onClose={onClose}>
      <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4">
        <h2 className="text-base font-semibold text-gray-900">{tool.name}</h2>
        <button onClick={onClose} className="rounded p-1 text-gray-400 hover:bg-gray-100">
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-5 space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <StatusPill status={tool.status as 'connected' | 'degraded' | 'disconnected'} />
          <AssetWorkspaceBadge scoped={false} />
        </div>

        <div className="grid grid-cols-2 gap-2 text-xs">
          {(
            [
              ['Type', tool.type],
              ['Auth', tool.auth_type ?? '—'],
              ['Provider', tool.provider ?? '—'],
              ['Data handling', tool.data_handling_designation ?? 'unknown'],
              ['Last used', tool.last_used ?? 'never'],
            ] as [string, string][]
          ).map(([k, v]) => (
            <div key={k} className="rounded bg-gray-50 px-3 py-2">
              <div className="text-gray-400 uppercase tracking-wide text-2xs font-semibold">{k}</div>
              <div className="mt-0.5 font-medium text-gray-800 break-all">{v}</div>
            </div>
          ))}
        </div>

        <p className="text-xs text-gray-400">
          This asset type has no real relationship data yet (see B-196's Part B investigation) -- nothing else to show here.
        </p>
      </div>
    </SlideOverPanel>
  )
}
