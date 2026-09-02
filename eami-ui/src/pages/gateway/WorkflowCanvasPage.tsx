// WorkflowCanvasPage.tsx -- Workflow canvas rebuild, Brief 2 (B-131):
// local interactivity -- add/remove/reorder steps, click-to-configure via
// the reused StepConfigPanel (WorkflowsPage.tsx, B-064/065, imported
// unmodified, not forked). Renamed from WorkflowCanvasPreviewPage.tsx
// (Brief 1) now that the page is no longer read-only-only -- the route
// path itself is deliberately left unchanged
// (/gateway/workflows/:id/canvas-preview, bookmark/share stability,
// B-146's own lesson); only this file/component's name and
// WorkflowsPage.tsx's one Eye-icon tooltip changed for this brief.
//
// CRITICAL BOUNDARY, unchanged from this epic's own original Brief 2
// precedent (B-067): zero backend writes. Every add/remove/reorder/
// configure action mutates only local React state (the designer's own
// Definition) -- no useCreateWorkflow/useUpdateWorkflow/
// useDeleteWorkflow/useSetWorkflowStepParams import or call anywhere in
// this file (confirmed by grep, see BUILT.md). The only network calls
// this page makes are reads: the same GetWorkflow Brief 1 already made,
// plus the same per-step static-params read (B-059) WorkflowsPage.tsx's
// EditWorkflowPanel already performs.
//
// WorkflowsPage.tsx (the card editor) is untouched beyond the two small,
// explicitly-approved exceptions this brief needed: exporting
// StepConfigPanel/StepRow/ParamRow/revalidateExtractionRefs/newStepRow/
// newParamRow (zero logic change, reuse-only), and updating the existing
// Eye-icon's tooltip text now that its destination is no longer
// read-only. No cutover decision is made or implied here.
import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { SequentialWorkflowDesigner, wrapDefinition } from 'sequential-workflow-designer-react'
import type { StepsConfiguration, ToolboxGroupConfiguration } from 'sequential-workflow-designer'
import 'sequential-workflow-designer/css/designer.css'
import 'sequential-workflow-designer/css/designer-light.css'
import { PageHeader, LoadingSpinner, EmptyState } from '@/components/common'
import { apiFetch } from '@/api/client'
import { useWorkflow } from '@/hooks/useWorkflows'
import { useTools } from '@/hooks/useTools'
import type { ToolWithActions } from '@/hooks/useTools'
import { StepConfigPanel, revalidateExtractionRefs, newParamRow } from './WorkflowsPage'
import type { StepRow, ParamRow } from './WorkflowsPage'
import {
  buildInitialDefinition,
  rowFromStep,
  stepFromRow,
  CONNECTOR_ACTION_STEP_TYPE,
  CONNECTOR_ACTION_TOOLBOX_STEP,
} from './workflowCanvasAdapter'

// A small inline SVG (a plug/connector glyph) as a data URI -- exercises
// the free-tier steps.iconUrlProvider config path with a real per-type
// icon rather than falling back to null/no-icon. Unchanged from Brief 1.
const CONNECTOR_ACTION_ICON =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="%23ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v3"/><path d="M4 17v3a1 1 0 0 0 1 1h4a1 1 0 0 0 1-1v-3"/><path d="M8 7h8v10H8z"/><path d="M14 4v3M14 17v3M20 10h2M20 14h2M2 10h2M2 14h2"/></svg>'
  )

const stepsConfiguration: StepsConfiguration = {
  iconUrlProvider: (_componentType, type) =>
    type === CONNECTOR_ACTION_STEP_TYPE ? CONNECTOR_ACTION_ICON : null,
}

// One toolbox group, one draggable template -- an empty, unconfigured
// step (workflowCanvasAdapter.ts's CONNECTOR_ACTION_TOOLBOX_STEP). The
// library's own free-tier Toolbox handles the actual drag-to-add
// interaction and assigns the new Step a real id on drop -- nothing
// custom-built here, per this brief's own investigation ask.
const toolboxGroups: ToolboxGroupConfiguration[] = [{ name: 'Steps', steps: [CONNECTOR_ACTION_TOOLBOX_STEP] }]

// Stable fallback for the one render window before the initial seed has
// run (sequence is always [] then too, so this is never actually read
// for a real step -- just satisfies rowFromStep's non-nullable param).
const EMPTY_STEP_IDS: ReadonlySet<string> = new Set()

export function WorkflowCanvasPage() {
  const { id } = useParams<{ id: string }>()
  const { data: workflow, isLoading, error } = useWorkflow(id ?? null)
  const { data: toolsData, isSuccess: toolsReady, isError: toolsErrored } = useTools()
  // Typed the same way WorkflowsPage.tsx's own StepsEditor does.
  const tools: ToolWithActions[] = (toolsData as any)?.data ?? []

  // Same B-059 per-step static-params read EditWorkflowPanel already
  // performs (WorkflowsPage.tsx), so StepConfigPanel opens with each
  // step's REAL configured params (AC4), not just connector/action --
  // a read, never a write.
  const stepIds = (workflow?.steps ?? []).map((s) => s.id)
  const paramsQueries = useQueries({
    queries: stepIds.map((sid) => ({
      queryKey: ['workflow-step-params', sid],
      queryFn: () => apiFetch<Record<string, unknown>>(`/v1/gateway/workflow-steps/${sid}/params`),
    })),
  })
  const paramsReady = stepIds.length === 0 || paramsQueries.every((q) => q.isSuccess || q.isError)

  const [definition, setDefinition] = useState<ReturnType<typeof wrapDefinition> | null>(null)
  // The set of step ids genuinely persisted at load time -- see
  // workflowCanvasAdapter.ts's rowFromStep for why this matters: a
  // canvas-added step must never look like a valid extraction SOURCE
  // before it's actually been saved (this brief never saves), exactly
  // StepRow's own established invariant from the card editor. Captured
  // ONCE, alongside `definition` below, in the same one-time seed --
  // NOT recomputed from `workflow` on every render/refetch (a real bug
  // caught by review: `useWorkflow`'s default staleTime is 30s and
  // refetchOnReconnect isn't disabled, so a background refetch after a
  // real network blip could silently shrink this set mid-session if
  // another tab/session removed a step via the card editor meanwhile --
  // reclassifying an already-in-canvas-use step as "not real" with no
  // user action and no visible cause).
  const [realStepIds, setRealStepIds] = useState<ReadonlySet<string> | null>(null)
  const [selectedStepId, setSelectedStepId] = useState<string | null>(null)

  // Initialize exactly ONCE, when the workflow, every step's static
  // params, AND the tools list have all first loaded -- guarded on
  // `definition === null`, the same render-body-conditional-setState
  // pattern WorkflowsPage.tsx's own EditWorkflowPanel already relies on
  // for its identical `rows` one-time seed. Brief 1 re-seeded from a
  // useEffect keyed on `initialDefinition` (a fresh object every
  // render), which is harmless when read-only -- but Brief 2 has real
  // local, unsaved edits (add/remove/reorder/configure) that a stray
  // background TanStack Query refetch must never be able to silently
  // discard. Caught in review before building, not after.
  //
  // Waiting on `toolsReady` too (not just `workflow`/`paramsReady`)
  // closes a real race the review also caught: commit() always converts
  // EVERY row back through stepFromRow(row, tools) to resolve each
  // step's display name/toolName, so if the designer became interactive
  // (and the user reordered/added a step) before `tools` had loaded even
  // once, every step's label would be wrongly rebuilt from an empty
  // list -- and, unlike the card editor (which resolves tool?.name fresh
  // on every render), that wrong label would persist in the committed
  // Step until some later, unrelated edit happened to refresh it.
  // Delaying the designer's first mount until tools has resolved (success
  // OR error -- a real tools-fetch failure shouldn't wedge the canvas
  // forever) means every commit(), from the very first, always has real
  // tools data to work with.
  if (workflow && paramsReady && (toolsReady || toolsErrored) && definition === null) {
    const staticParamsByStepId: Record<string, Record<string, unknown>> = {}
    stepIds.forEach((sid, idx) => {
      staticParamsByStepId[sid] = (paramsQueries[idx]?.data ?? {}) as Record<string, unknown>
    })
    setRealStepIds(new Set(stepIds))
    setDefinition(wrapDefinition(buildInitialDefinition(workflow, staticParamsByStepId)))
  }

  const sequence = definition?.value.sequence ?? []
  const rows: StepRow[] = sequence.map((s) => rowFromStep(s, realStepIds ?? EMPTY_STEP_IDS))

  // commit mirrors WorkflowsPage.tsx's StepsEditor.commit() exactly:
  // revalidate extraction refs first, on EVERY mutation, no exceptions
  // -- then convert back to Steps and replace the designer's
  // definition. Every local mutation path funnels through this one
  // function: StepConfigPanel's own callbacks below, AND the library's
  // native add/remove/reorder (via handleDefinitionChange) -- so there
  // is exactly one place this rule could be forgotten, matching the
  // reused original's own stated intent (revalidateExtractionRefs
  // itself is imported unchanged, not reimplemented).
  //
  // REAL BUG, found by live testing, fixed here: sequential-workflow-
  // designer-react decides whether to fully destroy and rebuild its
  // internal Designer purely by REFERENCE identity -- confirmed by
  // reading its actual source (lib/esm/index.js): `definition.value ===
  // designerRef.current.getDefinition()`. It also echoes its own current
  // definition back to us once after EVERY Designer.create() via
  // onReady -- not just after a real user edit. Before this fix, commit()
  // unconditionally built a brand-new object graph on every call, so
  // that echo always looked like a fresh change: setDefinition -> a
  // rebuilt Designer -> another onReady echo -> setDefinition again --
  // a self-sustaining loop, live-confirmed as continuous flicker and
  // broken toolbox drag-and-drop (the whole canvas DOM was being torn
  // down and rebuilt at rest, independent of any user action). The real
  // fix is a genuine CONTENT comparison, not the library's own reference
  // check (which this code was defeating): if the round-tripped result
  // is byte-identical to what's already committed, skip setDefinition
  // entirely, so `definition`'s reference truly doesn't change and the
  // library's own check correctly treats it as unchanged, breaking the
  // loop at its source rather than patching a symptom. JSON.stringify is
  // sufficient here (not a lodash-style deep-equal, no new dependency
  // needed) because every value in a Definition is already required to
  // be JSON-serializable (sequential-workflow-model's own PropertyValue
  // contract) and stepFromRow/revalidateExtractionRefs always build
  // objects with the same deterministic key order, so equal content
  // always serializes identically.
  function commit(newRows: StepRow[]) {
    if (!definition) return
    const revalidated = revalidateExtractionRefs(newRows)
    const newSequence = revalidated.map((r) => stepFromRow(r, tools))
    const nextValue = { ...definition.value, sequence: newSequence }
    if (JSON.stringify(nextValue) === JSON.stringify(definition.value)) return
    setDefinition(wrapDefinition(nextValue))
  }

  function updateRow(i: number, patch: Partial<StepRow>) {
    commit(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function updateParam(stepIdx: number, paramIdx: number, patch: Partial<ParamRow>) {
    commit(
      rows.map((r, i) =>
        i !== stepIdx ? r : { ...r, params: r.params.map((p, pi) => (pi !== paramIdx ? p : { ...p, ...patch })) }
      )
    )
  }
  function addParam(stepIdx: number) {
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: [...r.params, newParamRow()] })))
  }
  function removeParam(stepIdx: number, paramIdx: number) {
    commit(rows.map((r, i) => (i !== stepIdx ? r : { ...r, params: r.params.filter((_, pi) => pi !== paramIdx) })))
  }

  // Native library interactions (toolbox drag-add, context-menu/keyboard
  // delete, drag-to-reorder) arrive here already structurally mutated by
  // the library itself (DefinitionChangedEvent.changeType: stepInserted/
  // stepDeleted/stepMoved, confirmed against the library's own real
  // .d.ts, not guessed) -- this handler's only job is running every
  // change through the SAME commit() every StepConfigPanel edit already
  // goes through, so an extraction ref invalidated by a native
  // remove/reorder is caught exactly like a card-editor remove/reorder
  // already is. Always round-tripped, never branched by changeType --
  // revalidateExtractionRefs is idempotent and cheap, matching the
  // reused commit()'s own "always run it" discipline.
  function handleDefinitionChange(next: ReturnType<typeof wrapDefinition>) {
    const nextRows = next.value.sequence.map((s) => rowFromStep(s, realStepIds ?? EMPTY_STEP_IDS))
    commit(nextRows)
  }

  return (
    <div>
      <PageHeader
        title={workflow ? workflow.name : 'Workflow canvas'}
        subtitle="Brief 2: add, remove, reorder, and configure steps -- entirely local, nothing is saved yet. The card editor at Gateway / Workflows remains the real save surface."
      />
      {/* h-[75vh] + grid, not h-full/flex-1 + flex -- both required per
          Brief 1's own real, live-debugged finding (see BUILT.md's B-145
          entry): sequential-workflow-designer's shipped CSS depends
          entirely on its container having a genuinely definite size in
          BOTH axes, and this AppShell/main chain has no min-height:0.
          Unchanged from Brief 1 -- nothing about adding interactivity
          changes this constraint. */}
      <div className="grid h-[75vh] w-full overflow-hidden">
        {/* Covers both "still loading the workflow itself" AND "workflow
            loaded, still waiting on params/tools before the one-time
            definition seed can run" (the same toolsReady gate above that
            closes the mislabel race) -- without the second half, that
            window would render neither this spinner nor the designer
            nor the not-found state below: a blank flash. */}
        {(isLoading || (!error && workflow && definition === null)) && (
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
            onDefinitionChange={handleDefinitionChange}
            selectedStepId={selectedStepId}
            onSelectedStepIdChanged={setSelectedStepId}
            stepsConfiguration={stepsConfiguration}
            toolboxConfiguration={{ groups: toolboxGroups }}
            controlBar
            contextMenu
            keyboard
            rootEditor={false}
            stepEditor={false}
          />
        )}
      </div>
      {/* StepConfigPanel (WorkflowsPage.tsx, B-064/065) reused exactly,
          unmodified -- driven by the library's own selectedStepId/
          onSelectedStepIdChanged (confirmed real props against the
          library's .d.ts) rather than its stepEditor slot (left false
          above), so this stays the SAME component/props contract the
          card editor already uses, not an adaptation of it. */}
      {selectedStepId &&
        (() => {
          const idx = rows.findIndex((r) => r.localId === selectedStepId)
          if (idx === -1) return null
          return (
            <StepConfigPanel
              row={rows[idx]}
              index={idx}
              rows={rows}
              tools={tools}
              onClose={() => setSelectedStepId(null)}
              updateRow={updateRow}
              updateParam={updateParam}
              addParam={addParam}
              removeParam={removeParam}
            />
          )
        })()}
    </div>
  )
}
