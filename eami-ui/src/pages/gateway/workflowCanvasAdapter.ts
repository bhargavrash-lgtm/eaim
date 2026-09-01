// workflowCanvasAdapter.ts -- Workflow canvas rebuild, Brief 1 (B-131/
// B-1xx, read-only rendering only). Converts EAMI's real Workflow/
// WorkflowStep shape (useWorkflows.ts) into sequential-workflow-
// designer's Definition{properties, sequence: Step[]} model.
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
import type { Definition, PropertyValue } from 'sequential-workflow-designer'
import type { Workflow } from '@/hooks/useWorkflows'

export const CONNECTOR_ACTION_STEP_TYPE = 'eami-connector-action'

// Properties must be JSON-serializable (sequential-workflow-model's own
// contract) -- WorkflowStep's fields already are.
export interface ConnectorActionStepProperties {
  workflowStepId: string
  gatewayToolId: string
  toolName: string
  action: string
  hasInputMapping: boolean
  [key: string]: PropertyValue
}

export function workflowToDefinition(workflow: Workflow): Definition {
  const steps = workflow.steps ?? []
  return {
    properties: {
      workflowId: workflow.id,
      workflowName: workflow.name,
      workflowStatus: workflow.status,
    },
    sequence: steps.map((step) => {
      const toolName = step.tool_name || '(deleted connector)'
      const properties: ConnectorActionStepProperties = {
        workflowStepId: step.id,
        gatewayToolId: step.gateway_tool_id,
        toolName,
        action: step.action,
        hasInputMapping: !!step.input_mapping && Object.keys(step.input_mapping).length > 0,
      }
      return {
        id: step.id,
        componentType: 'task',
        type: CONNECTOR_ACTION_STEP_TYPE,
        name: `${toolName}.${step.action}`,
        properties,
      }
    }),
  }
}
