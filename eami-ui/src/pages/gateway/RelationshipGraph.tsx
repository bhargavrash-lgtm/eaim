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
import { useMemo, useRef, useState, useEffect, useCallback } from 'react'
import { ShieldCheck, Wrench, Workflow as WorkflowIcon, Monitor, Maximize } from 'lucide-react'
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
// B-204: 48, was 40 -- defensive headroom, not a reproduced-clip fix.
// Live testing (4 real agents x 3 viewports) found the topmost node was
// NOT actually clipping with the current code, so this isn't chasing a
// reproduced bug -- it's closing a real structural risk class: a
// computed-height-driven `overflow-hidden` container (below) fails
// SILENTLY if the height formula is ever off by a few px in some future
// junction/target-count edge case -- content just vanishes, no visible
// error. Extra padding on both ends is cheap, real insurance against
// that failure mode.
const PAD = 48

// B-205: pan/zoom constants. MAX_VIEWPORT_H extends B-204's height-cap
// reasoning rather than reversing it -- B-204 hardened against SILENT
// clipping (a computed height that's wrong by a few px, no recovery, no
// visible error). This cap is different in kind: it's paired with a real
// recovery mechanism (pan/zoom + the reset-view control below), and the
// cap itself is set high enough (700px) that it does not engage for any
// currently-real graph (confirmed range across every real agent tested
// this session: 175-643px) -- it only ever activates for a genuinely
// large graph, which is exactly the case this feature exists for.
const MAX_VIEWPORT_H = 700
const MIN_ZOOM = 0.5
const MAX_ZOOM = 2.5
const DRAG_THRESHOLD_PX = 5

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

  // B-205: pan/zoom. `pan`/`zoom` are real React state (drive the visible
  // transform); `dragRef`/`suppressNextClickRef` are transient interaction
  // bookkeeping that must NOT cause re-renders on every mousemove.
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [zoom, setZoom] = useState(1)
  const containerRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef<{ startClientX: number; startClientY: number; startPanX: number; startPanY: number; draggedPastThreshold: boolean } | null>(null)
  const suppressNextClickRef = useRef(false)

  const resetView = useCallback(() => {
    setPan({ x: 0, y: 0 })
    setZoom(1)
  }, [])

  // Left-button mousedown only; starts tracking a *potential* drag without
  // yet committing to pan (that only happens once DRAG_THRESHOLD_PX is
  // crossed -- see handleMouseMove). Window-level listeners (not React
  // props) so a fast drag that leaves the container's own box mid-gesture
  // still resolves correctly on mouseup, matching every standard drag
  // implementation's own pattern.
  const handleMouseDownCapture = useCallback((e: React.MouseEvent) => {
    if (e.button !== 0) return
    const start = { clientX: e.clientX, clientY: e.clientY, panX: pan.x, panY: pan.y }
    dragRef.current = { startClientX: start.clientX, startClientY: start.clientY, startPanX: start.panX, startPanY: start.panY, draggedPastThreshold: false }

    function handleMove(ev: MouseEvent) {
      const current = dragRef.current
      if (!current) return
      const dx = ev.clientX - start.clientX
      const dy = ev.clientY - start.clientY
      if (!current.draggedPastThreshold && Math.hypot(dx, dy) > DRAG_THRESHOLD_PX) {
        current.draggedPastThreshold = true
      }
      if (current.draggedPastThreshold) {
        setPan({ x: start.panX + dx, y: start.panY + dy })
      }
    }
    function handleUp() {
      window.removeEventListener('mousemove', handleMove)
      window.removeEventListener('mouseup', handleUp)
      // B-205: this is the real click/drag disambiguation -- NOT a change
      // to any node's own onClick={t.onSelect} wiring (untouched, below).
      // If this gesture crossed the drag threshold, the very next click
      // event is intercepted and cancelled in the CAPTURE phase (see
      // handleClickCapture), before it ever reaches the target button's
      // own bubble-phase onClick. A genuine click (never crosses the
      // threshold) never sets this flag, so the button fires normally.
      if (dragRef.current?.draggedPastThreshold) {
        suppressNextClickRef.current = true
      }
      dragRef.current = null
    }
    window.addEventListener('mousemove', handleMove)
    window.addEventListener('mouseup', handleUp)
  }, [pan])

  const handleClickCapture = useCallback((e: React.MouseEvent) => {
    if (suppressNextClickRef.current) {
      suppressNextClickRef.current = false
      e.stopPropagation()
    }
  }, [])

  // Native (non-React) wheel listener, required for Ctrl/Cmd+wheel: React
  // attaches its delegated `onWheel` listener as passive, so
  // preventDefault() inside a JSX onWheel handler is silently a no-op --
  // without a real native listener here, Ctrl+wheel would trigger the
  // BROWSER's own page-zoom instead of (or alongside) this graph's zoom.
  // Plain wheel (no Ctrl/Cmd) is deliberately left alone -- no
  // preventDefault, no state change -- so the page scrolls normally past
  // the graph, matching Figma/Miro's convention rather than scroll-
  // jacking the whole page the way "plain wheel = zoom" would.
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    function handleWheel(e: WheelEvent) {
      if (!e.ctrlKey && !e.metaKey) return
      e.preventDefault()
      const rect = el!.getBoundingClientRect()
      const cx = e.clientX - rect.left
      const cy = e.clientY - rect.top
      const factor = Math.exp(-e.deltaY * 0.002)
      setZoom((prevZoom) => {
        const newZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, prevZoom * factor))
        setPan((prevPan) => ({
          x: cx - (cx - prevPan.x) * (newZoom / prevZoom),
          y: cy - (cy - prevPan.y) * (newZoom / prevZoom),
        }))
        return newZoom
      })
    }
    el.addEventListener('wheel', handleWheel, { passive: false })
    return () => el.removeEventListener('wheel', handleWheel)
  }, [])

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
    <div
      ref={containerRef}
      className="relative overflow-hidden rounded-xl bg-white shadow-l2 cursor-grab active:cursor-grabbing"
      style={{ height: Math.min(layout.height, MAX_VIEWPORT_H) }}
      onMouseDownCapture={handleMouseDownCapture}
      onClickCapture={handleClickCapture}
    >
      {/* B-205: reset/fit-to-view -- real recovery once a user has
          panned/zoomed away from the default view. Deliberately a
          sibling of the transformed layer below, not a child of it, so
          this chrome itself never pans/scales. */}
      <button
        type="button"
        onClick={resetView}
        title="Reset pan/zoom"
        className="absolute right-3 top-3 z-10 flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-2.5 py-1.5 text-2xs font-semibold text-ink-faint shadow-l1 hover:bg-gray-50"
      >
        <Maximize className="h-3 w-3" />
        Reset view
      </button>

      {/* B-205: the pan/zoom transform layer -- everything that was
          previously a direct child of the outer div (SVG + center node +
          rows) now lives inside this absolutely-positioned wrapper
          instead, so translate/scale moves the whole graph as one unit
          while the outer div's own overflow-hidden clips it to the
          viewport box above. */}
      <div
        style={{
          position: 'absolute',
          top: 0,
          left: 0,
          // Real width/height, not left implicit: every child inside this
          // wrapper is itself `position: absolute` (the SVG and every
          // node/label), so none of them contribute to a shrink-to-fit
          // auto-size for this wrapper -- without an explicit size here,
          // this wrapper's own containing-block width collapses toward 0,
          // which made the junction labels (the one absolutely-positioned
          // child with no explicit `width` of its own) wrap onto two
          // lines -- a real regression caught in this brief's own live
          // verification, not assumed safe. Matches the SVG's own
          // explicit width/height exactly, since that's the real content
          // extent this layer represents pre-transform.
          width: layout.width,
          height: layout.height,
          transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
          transformOrigin: '0 0',
        }}
      >
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
              // B-203: -34, was -26 -- a live-measured, real-screenshot-confirmed
              // crowding on multi-target junctions (e.g. "dispatches through"
              // fanning to 3 targets), where the old offset left only ~6px of
              // clearance above the converging bezier curves.
              style={{ left: JUNCTION_X - 60, top: junctionY - 34 }}
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
    </div>
  )
}
