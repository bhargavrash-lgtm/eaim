// workflowCanvasAdapter.ts -- Workflow canvas rebuild, Brief 1 (read-only
// rendering) and Brief 2 (B-131, local interactivity, zero backend
// writes). Converts EAMI's real Workflow/WorkflowStep shape
// (useWorkflows.ts) into sequential-workflow-designer's
// Definition{properties, sequence: Step[]} model, and back.
//
// Deliberately linear only -- one Step per WorkflowStep, componentType
// 'task' for every step. No Container/Switch/branching: the investigation
// (BACKLOG.md B-131) confirmed workflow_steps' flat step_order schema
// cannot represent branches today, and this brief builds no backend
// support for it -- rendering a Switch/branches shape here would imply a
// capability that doesn't exist.
//
// type is a real custom step type ('eami-connector-action'), not one of
// the library's own built-in types -- this is exactly the free-tier
// custom-step-type question Brief 1 exists to settle empirically (see
// BUILT.md for the resolution).
import type { Definition, PropertyValue, Step } from 'sequential-workflow-designer'
import type { Workflow, WorkflowStep } from '@/hooks/useWorkflows'
import type { ToolWithActions } from '@/hooks/useTools'
import type { StepRow, ParamRow } from './WorkflowsPage'

export const CONNECTOR_ACTION_STEP_TYPE = 'eami-connector-action'

// Properties must be JSON-serializable (sequential-workflow-model's own
// contract, PropertyValue = ... | object) -- ParamRow[] is a plain array
// of plain objects, so it's a legitimate property value, not a workaround.
// Brief 2 extends Brief 1's properties bag with the FULL data
// StepConfigPanel needs (params), replacing the read-only-era
// `hasInputMapping` boolean summary and the redundant `workflowStepId`
// (Step.id already carries this at the top level).
export interface ConnectorActionStepProperties {
  gatewayToolId: string
  toolName: string
  action: string
  toolDeleted: boolean
  params: ParamRow[]
  [key: string]: PropertyValue
}

function stepLabel(toolName: string, action: string): string {
  return action ? `${toolName}.${action}` : toolName
}

// stepFromWorkflowStep seeds one Step from a real, already-persisted
// WorkflowStep (+ that step's real static params, fetched separately --
// B-059's own endpoint, the same read WorkflowsPage.tsx's
// EditWorkflowPanel already performs). Mirrors EditWorkflowPanel's own
// static/extract row-building rules exactly (same shapes, same
// toolDeleted rule: !gateway_tool_id) so StepConfigPanel behaves
// identically here and in the card editor.
function stepFromWorkflowStep(step: WorkflowStep, staticParams: Record<string, unknown>): Step {
  const toolDeleted = !step.gateway_tool_id
  const staticRows: ParamRow[] = Object.entries(staticParams).map(([key, value]) => ({
    key,
    mode: 'static',
    value: typeof value === 'string' ? value : JSON.stringify(value),
    fromStepLocalId: '',
    path: '',
  }))
  const extractRows: ParamRow[] = Object.entries(step.input_mapping ?? {}).map(([key, ref]) => ({
    key,
    mode: 'extract',
    value: '',
    fromStepLocalId: ref.from_step,
    path: ref.path,
  }))
  const toolName = step.tool_name || (toolDeleted ? '(deleted connector)' : '(connector)')
  const properties: ConnectorActionStepProperties = {
    gatewayToolId: step.gateway_tool_id,
    toolName,
    action: step.action,
    toolDeleted,
    params: [...staticRows, ...extractRows],
  }
  return {
    id: step.id,
    componentType: 'task',
    type: CONNECTOR_ACTION_STEP_TYPE,
    name: stepLabel(toolName, step.action),
    properties,
  }
}

// buildInitialDefinition is this brief's replacement for Brief 1's
// workflowToDefinition -- same workflow-properties shape, but each step
// now carries everything StepConfigPanel needs (not just a summary),
// via stepFromWorkflowStep above. staticParamsByStepId comes from the
// same B-059 read WorkflowCanvasPage.tsx performs on load.
export function buildInitialDefinition(
  workflow: Workflow,
  staticParamsByStepId: Record<string, Record<string, unknown>>,
): Definition {
  const steps = workflow.steps ?? []
  return {
    properties: {
      workflowId: workflow.id,
      workflowName: workflow.name,
      workflowStatus: workflow.status,
    },
    sequence: steps.map((step) => stepFromWorkflowStep(step, staticParamsByStepId[step.id] ?? {})),
  }
}

// A step doesn't carry "was this loaded from the server" as its own
// field, so rowFromStep needs the set of ids that were REALLY persisted
// at load time to correctly leave a canvas-added (this-session-only)
// step's StepRow.id undefined -- exactly StepRow's own established
// invariant (see WorkflowsPage.tsx: undefined id is what makes a step
// ineligible as an extraction SOURCE until it's actually been saved).
// Getting this wrong would let a step added on the canvas this session
// be picked as another step's extraction source even though it has no
// real backend id yet to reference -- the state-management edge case
// this brief's own review pass is specifically asked to check.
export function rowFromStep(step: Step, realStepIds: ReadonlySet<string>): StepRow {
  const props = step.properties as unknown as ConnectorActionStepProperties
  const isReal = realStepIds.has(step.id)
  return {
    localId: step.id,
    id: isReal ? step.id : undefined,
    gatewayToolId: props.gatewayToolId,
    action: props.action,
    toolDeleted: props.toolDeleted,
    params: props.params,
  }
}

// stepFromRow is the inverse -- a StepRow (as mutated by StepConfigPanel,
// or freshly created by the toolbox template below) back into a Step.
// toolName is re-resolved from the live tools list on every conversion
// (never trusted from stale row state) so a connector rename elsewhere
// is reflected immediately, same freshness rule the card editor's own
// StepsEditor already follows via `tools.find(...)`.
export function stepFromRow(row: StepRow, tools: ToolWithActions[]): Step {
  const tool = tools.find((t) => t.id === row.gatewayToolId)
  const toolName = tool?.name ?? (row.toolDeleted ? '(deleted connector)' : '(connector)')
  const properties: ConnectorActionStepProperties = {
    gatewayToolId: row.gatewayToolId,
    toolName,
    action: row.action,
    toolDeleted: !!row.toolDeleted,
    params: row.params,
  }
  return {
    id: row.id ?? row.localId,
    componentType: 'task',
    type: CONNECTOR_ACTION_STEP_TYPE,
    name: stepLabel(toolName, row.action),
    properties,
  }
}

// The toolbox's one draggable template (B-131 Brief 2) -- an empty,
// unconfigured step, deliberately matching WorkflowsPage.tsx's own
// newStepRow() empty-state convention exactly (same three blank fields),
// so a canvas-added step opens in StepConfigPanel exactly like a
// card-editor "+ Add step" row would. The library assigns a real,
// unique id on drop (StepDefinition = Omit<Step, 'id'>) -- never
// generated here, so there's no risk of colliding with either the
// library's own id scheme or a real backend step id.
export const CONNECTOR_ACTION_TOOLBOX_STEP: Omit<Step, 'id'> = {
  componentType: 'task',
  type: CONNECTOR_ACTION_STEP_TYPE,
  name: 'New connector step',
  properties: {
    gatewayToolId: '',
    toolName: '',
    action: '',
    toolDeleted: false,
    params: [],
  } satisfies ConnectorActionStepProperties,
}
