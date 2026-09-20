// RelationshipGraph.tsx -- eami-api/eami-ui
// B-200: the real, scoped (Focused Mode -- this one agent's own
// connections, never an org-wide graph) relationship graph on the new
// Agent Detail page, translating DESIGN_SYSTEM.md §7.1 / the live
// canvas's Layer 3 into a hand-rolled SVG component (not a 3rd-party
// canvas library -- B-148's render-loop lesson was specific to
// sequential-workflow-designer-react, which this doesn't use; the general
// precaution still applies: selection state stays a primitive id/kind
// pair, never an object identity compared by reference, and the node/edge
// layout is memoized so it isn't recomputed on every render).
//
// One center node (the agent), up to 4 fixed junctions with real
// directional labels ("governed by"/"dispatches through"/"appears in"/
// "linked to"), each fanning out via real cubic-bezier curves to however
// many real target nodes that relationship type actually has -- zero,
// one, or many. A junction with zero real connections is omitted
// entirely (DESIGN_SYSTEM.md §7.4's "never fabricate what isn't real
// yet" principle, applied to an empty relationship type, not just an
// empty metric).
import { useMemo } from 'react'
import { ShieldCheck, Wrench, Workflow as WorkflowIcon, Monitor } from 'lucide-react'
import type { AgentConnections } from '@/hooks/useAgents'

export type SelectedGraphNode =
  | { kind: 'policy'; id: string }
  | { kind: 'tool'; id: string }
  | { kind: 'workflow'; id: string }
  | { kind: 'endpoint'; id: string }

type JunctionKey = 'policy' | 'tool' | 'workflow' | 'endpoint'

interface TargetNode {
  key: string
  id: string
  icon: typeof ShieldCheck
  title: string
  subtitle?: string
  isActive: boolean
  clickable: boolean
  onSelect: () => void
}

interface Junction {
  key: JunctionKey
  label: string
  isActive: boolean
  targets: TargetNode[]
}

// Layout constants. Center/junction/target x-positions and the bezier
// control-point formula (always the horizontal midpoint between the two
// endpoints) are taken directly from the live canvas's real SVG source
// (Layer3-RelationshipGraph.dc.html) -- not invented here.
const CENTER_W = 150
const CENTER_H = 72
const JUNCTION_X = 310
const TARGET_X = 460
const TARGET_W = 280
const TARGET_H = 72
const ROW_H = 95
const JUNCTION_GAP = 36
const PAD = 40

function bezierH(x1: number, y1: number, x2: number, y2: number): string {
  const midX = (x1 + x2) / 2
  return `M${x1},${y1} C${midX},${y1} ${midX},${y2} ${x2},${y2}`
}

// Controlled component: selection state lives in the parent (AgentDetailPage)
// -- the single source of truth for both "which node is highlighted here"
// and "which SlideOverPanel is open," rather than duplicating it in two
// places that could drift out of sync.
export function RelationshipGraph({
  agentName,
  connections,
  selected,
  onSelect,
}: {
  agentName: string
  connections: AgentConnections
  selected: SelectedGraphNode | null
  onSelect: (node: SelectedGraphNode) => void
}) {
  // Memoized: junctions/positions are only recomputed when the real
  // connections data actually changes, not on every render (e.g. a
  // parent re-render from an unrelated state change) -- the defensive
  // reapplication of B-148's lesson this brief's own plan called for.
  const layout = useMemo(() => {
    const junctions: Junction[] = [
      {
        key: 'policy' as const,
        label: 'governed by',
        isActive: false,
        targets: connections.policies.map((p) => ({
          key: `policy-${p.policy_id}`,
          id: p.policy_id,
          icon: ShieldCheck,
          title: p.name,
          subtitle: `${p.action} policy`,
          isActive: false,
          clickable: true,
          onSelect: () => onSelect({ kind: 'policy', id: p.policy_id }),
        })),
      },
      {
        key: 'tool' as const,
        label: 'dispatches through',
        isActive: connections.tools.some((t) => t.is_active),
        targets: connections.tools.map((t) => ({
          key: `tool-${t.tool_id ?? t.tool_name}`,
          id: t.tool_id ?? '',
          icon: Wrench,
          title: t.tool_name,
          subtitle: t.tool_id
            ? `${t.call_count_24h} calls, last 24h`
            : 'connector no longer exists',
          isActive: t.is_active,
          clickable: t.tool_id != null,
          onSelect: () => { if (t.tool_id) onSelect({ kind: 'tool', id: t.tool_id }) },
        })),
      },
      {
        key: 'workflow' as const,
        label: 'appears in',
        isActive: false,
        targets: connections.workflows.map((w) => ({
          key: `workflow-${w.workflow_id}`,
          id: w.workflow_id,
          icon: WorkflowIcon,
          title: w.name,
          isActive: false,
          clickable: true,
          onSelect: () => onSelect({ kind: 'workflow', id: w.workflow_id }),
        })),
      },
      {
        key: 'endpoint' as const,
        label: 'linked to',
        isActive: false,
        targets: connections.endpoint
          ? [{
              key: `endpoint-${connections.endpoint.endpoint_id}`,
              id: connections.endpoint.endpoint_id,
              icon: Monitor,
              title: connections.endpoint.hostname,
              isActive: false,
              clickable: true,
              onSelect: () => {
                if (connections.endpoint) onSelect({ kind: 'endpoint', id: connections.endpoint.endpoint_id })
              },
            }]
          : [],
      },
    ].filter((j) => j.targets.length > 0)

    let y = PAD
    const rows: { junction: Junction; junctionY: number; targetYs: number[] }[] = []
    for (const junction of junctions) {
      const n = junction.targets.length
      const bandTop = y
      const targetYs = Array.from({ length: n }, (_, i) => bandTop + i * ROW_H + TARGET_H / 2)
      const junctionY = targetYs.reduce((a, b) => a + b, 0) / n
      rows.push({ junction, junctionY, targetYs })
      y = bandTop + n * ROW_H + JUNCTION_GAP
    }
    const height = Math.max(y - JUNCTION_GAP + PAD, CENTER_H + PAD * 2)
    const centerY = height / 2

    return { rows, height, centerY, width: TARGET_X + TARGET_W + PAD }
  }, [connections, onSelect])

  if (layout.rows.length === 0) {
    return (
      <div className="flex h-[220px] flex-col items-center justify-center gap-1.5 rounded-lg border-[1.5px] border-dashed border-gray-300 bg-white shadow-l1">
        <div className="text-sm font-semibold text-gray-500">No real connections yet</div>
        <div className="max-w-md text-center text-xs text-gray-400">
          This agent has no policy, tool, workflow, or endpoint history in the audit log yet -- an honest empty state, not a loading error.
        </div>
      </div>
    )
  }

  return (
    <div className="relative overflow-hidden rounded-xl bg-white shadow-l2" style={{ height: layout.height }}>
      <svg width={layout.width} height={layout.height} style={{ position: 'absolute', top: 0, left: 0 }}>
        {layout.rows.map(({ junction, junctionY, targetYs }) => (
          <g key={junction.key}>
            <path
              d={bezierH(CENTER_W, layout.centerY, JUNCTION_X, junctionY)}
              fill="none"
              stroke={junction.isActive ? '#3B5BDB' : '#D5D9E6'}
              strokeWidth={1.5}
            />
            {junction.targets.map((t, i) => (
              <path
                key={t.key}
                d={bezierH(JUNCTION_X, junctionY, TARGET_X, targetYs[i])}
                fill="none"
                stroke={t.isActive ? '#3B5BDB' : '#D5D9E6'}
                strokeWidth={1.5}
              />
            ))}
            <circle cx={JUNCTION_X} cy={junctionY} r={4} fill={junction.isActive ? '#3B5BDB' : '#8890AD'} />
          </g>
        ))}
      </svg>

      {/* Center node -- the agent itself. */}
      <div
        className="absolute flex flex-col items-center justify-center gap-1 rounded-lg bg-brand-50 px-3 text-center"
        style={{ left: 0, top: layout.centerY - CENTER_H / 2, width: CENTER_W, height: CENTER_H }}
      >
        <div className="truncate text-xs font-bold text-ink" title={agentName}>{agentName}</div>
      </div>

      {layout.rows.map(({ junction, junctionY, targetYs }) => (
        <div key={junction.key}>
          <div
            className={`absolute rounded-full bg-white px-2 py-0.5 text-2xs font-semibold ${junction.isActive ? 'text-brand-600' : 'text-ink-faint'}`}
            style={{ left: JUNCTION_X - 60, top: junctionY - 26 }}
          >
            {junction.label}
          </div>
          {junction.targets.map((t, i) => {
            const Icon = t.icon
            const active = t.isActive
            const isSelected = selected?.kind === junction.key && selected.id === t.id
            return (
              <button
                key={t.key}
                type="button"
                disabled={!t.clickable}
                onClick={t.onSelect}
                aria-pressed={isSelected}
                className={`absolute flex items-center gap-2.5 rounded-lg bg-white px-3.5 text-left transition-transform ${
                  t.clickable ? 'cursor-pointer hover:opacity-90' : 'cursor-not-allowed opacity-60'
                } ${active ? 'border-[1.5px] border-brand-500 shadow-l3 -translate-y-0.5' : 'shadow-l1'} ${
                  isSelected && !active ? 'ring-2 ring-brand-300' : ''
                }`}
                style={{ left: TARGET_X, top: targetYs[i] - TARGET_H / 2, width: TARGET_W, height: TARGET_H }}
              >
                <div className={`flex h-[30px] w-[30px] flex-shrink-0 items-center justify-center rounded-md ${active ? 'bg-brand-50' : 'bg-gray-100'}`}>
                  <Icon className={`h-[15px] w-[15px] ${active ? 'text-brand-600' : 'text-gray-500'}`} />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-xs font-semibold text-ink">{t.title}</div>
                  {t.subtitle && <div className="truncate text-2xs text-ink-faint">{t.subtitle}</div>}
                </div>
              </button>
            )
          })}
        </div>
      ))}
    </div>
  )
}
