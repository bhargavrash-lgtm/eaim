// WorkflowCanvasPreviewPage.tsx -- Workflow canvas rebuild, Brief 1
// (read-only rendering only, see BACKLOG.md's B-131 investigation and
// this brief's own entry). Renders a real workflow's steps via
// sequential-workflow-designer, entirely read-only.
//
// Deliberately NOT linked from WorkflowsPage.tsx or anywhere else in the
// app nav -- WorkflowsPage.tsx (the card editor) is explicitly out of
// this brief's MAY MODIFY scope and "stays exactly as-is ... throughout
// this entire epic" until a real cutover decision is made (not this
// brief). Reachable directly by URL for now; wiring a real entry point
// into the existing UI is a decision for whichever brief actually
// changes user-facing navigation.
//
// Every interactive surface is explicitly turned off below, not just
// left at defaults -- isReadonly alone is not treated as sufficient for
// a "zero interactivity" requirement.
import { useParams } from 'react-router-dom'
import { useEffect, useMemo, useState } from 'react'
import { SequentialWorkflowDesigner, wrapDefinition } from 'sequential-workflow-designer-react'
import type { StepsConfiguration } from 'sequential-workflow-designer'
import 'sequential-workflow-designer/css/designer.css'
import 'sequential-workflow-designer/css/designer-light.css'
import { PageHeader, LoadingSpinner, EmptyState } from '@/components/common'
import { useWorkflow } from '@/hooks/useWorkflows'
import { workflowToDefinition, CONNECTOR_ACTION_STEP_TYPE } from './workflowCanvasAdapter'

// A small inline SVG (a plug/connector glyph) as a data URI -- exercises
// the free-tier steps.iconUrlProvider config path with a real per-type
// icon rather than falling back to null/no-icon.
const CONNECTOR_ACTION_ICON =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="%23ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v3"/><path d="M4 17v3a1 1 0 0 0 1 1h4a1 1 0 0 0 1-1v-3"/><path d="M8 7h8v10H8z"/><path d="M14 4v3M14 17v3M20 10h2M20 14h2M2 10h2M2 14h2"/></svg>'
  )

const stepsConfiguration: StepsConfiguration = {
  iconUrlProvider: (_componentType, type) =>
    type === CONNECTOR_ACTION_STEP_TYPE ? CONNECTOR_ACTION_ICON : null,
}

export function WorkflowCanvasPreviewPage() {
  const { id } = useParams<{ id: string }>()
  const { data: workflow, isLoading, error } = useWorkflow(id ?? null)

  const initialDefinition = useMemo(() => (workflow ? workflowToDefinition(workflow) : null), [workflow])
  const [definition, setDefinition] = useState<ReturnType<typeof wrapDefinition> | null>(null)

  // The designer owns its committed definition state once mounted (its
  // React-wrapper contract requires onDefinitionChange even in read-only
  // mode, since selection/collapse UI state still flows through it) --
  // sync it in whenever the real workflow data changes, e.g. the initial
  // load completing.
  useEffect(() => {
    if (initialDefinition) {
      setDefinition(wrapDefinition(initialDefinition))
    }
  }, [initialDefinition])

  return (
    <div>
      <PageHeader
        title={workflow ? `${workflow.name} — read-only canvas preview` : 'Workflow canvas preview'}
        subtitle="Brief 1: rendering only. No editing, no saving -- the card editor at Gateway / Workflows remains the real editing surface."
      />
      {/* h-[75vh], not h-full/flex-1: sequential-workflow-designer's own
          shipped CSS is `.sqd-designer { height: 100% }`, and the div its
          React wrapper mounts into carries zero CSS of its own (confirmed
          by grepping designer.css) -- it depends entirely on this
          container having a real, DEFINITE resolved height. AppShell's
          own main/flex chain (shared across every page, not touched here)
          doesn't set min-height:0 at each level, so a pure h-full/flex-1
          percentage cascade here is fragile -- this is the first page in
          the app that ever needed one. A viewport-relative unit sidesteps
          that ancestor-chain question entirely: it always resolves to a
          real pixel value regardless of any parent's computed height.
          `grid` (not `flex`, not `block`) on this wrapper matters too,
          confirmed live via DevTools measurement -- `flex` only
          auto-stretches the CROSS axis (height, via `align-items:stretch`
          by default); the MAIN axis (width, in a row container) defaults
          to content-based sizing (`flex-grow: 0`), and the div the
          library's React wrapper renders (`sqd-designer-react`) carries
          zero CSS of its own and starts empty, so its content-based width
          measured exactly 0 -- propagating all the way down to the real
          `<svg class="sqd-workspace-canvas">` (`width="0"`), a genuine
          mount with genuinely zero width, not a mount failure. CSS Grid's
          default `place-items` is `stretch` in BOTH axes, so an unstyled
          single grid-cell child fills the full width and height with zero
          extra CSS needed on the child -- unlike flex, no per-axis gap. */}
      <div className="grid h-[75vh] w-full overflow-hidden">
        {isLoading && (
          <div className="flex h-full items-center justify-center">
            <LoadingSpinner size="lg" />
          </div>
        )}
        {!isLoading && (error || !workflow) && (
          <EmptyState
            title="Workflow not found"
            description={id ? `No workflow with id "${id}" could be loaded.` : 'No workflow id was provided in the URL.'}
          />
        )}
        {!isLoading && workflow && definition && (
          <SequentialWorkflowDesigner
            definition={definition}
            onDefinitionChange={setDefinition}
            isReadonly
            stepsConfiguration={stepsConfiguration}
            toolboxConfiguration={false}
            controlBar={false}
            contextMenu={false}
            keyboard={false}
            rootEditor={false}
            stepEditor={false}
          />
        )}
      </div>
    </div>
  )
}
