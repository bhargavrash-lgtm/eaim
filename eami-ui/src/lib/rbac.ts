// B-252 C0: the UI's mirror of B-253's server-side role rules ("operators
// contain; admins expand or destroy" -- B-253_VERIFICATION.md §1, router.go,
// rbac_fields.go). The server is the enforcement point; these only decide
// which write controls to render, so a role never sees a button the API
// would refuse with 403. Keep in step with router.go when a route moves.
import { useAuthStore } from '@/stores/authStore'

type Role = string | undefined

const admin = (r: Role) => r === 'admin'
const adminOrOperator = (r: Role) => r === 'admin' || r === 'operator'

export const can = {
  // Agents
  createAgent: admin, // POST /v1/gateway/agents
  configureAgent: adminOrOperator, // GET/PUT /v1/gateway/agents/{id}/config
  suspendAgent: adminOrOperator, // PATCH status -> suspended/revoked (containment)
  reactivateAgent: admin, // PATCH status -> active
  deleteAgent: admin, // DELETE /v1/gateway/agents/{id}
  // GET /v1/gateway/agents/{id}/connections (admin, operator, viewer)
  viewAgentConnections: (r: Role) => r === 'admin' || r === 'operator' || r === 'viewer',
  // Agent Detail's Actions tab: approvers see Overview only (B-253 Q-E);
  // viewers keep the tab with its read-only notice.
  viewAgentActionsTab: (r: Role) => r === 'admin' || r === 'operator' || r === 'viewer',
  // GET /v1/gateway/agents/{id}/lineage (admin, operator, viewer): audit
  // reads exclude approvers.
  viewAgentLineage: (r: Role) => r === 'admin' || r === 'operator' || r === 'viewer',
  // CMDB (B-252 C1): reads are admin/operator/viewer (approvers excluded);
  // taxonomy and per-asset classification writes are admin-only.
  viewCMDB: (r: Role) => r === 'admin' || r === 'operator' || r === 'viewer', // GET /v1/cmdb/*
  classifyAsset: admin, // PATCH /v1/cmdb/assets/{kind}/{id}/classification
  // Tools
  createTool: admin, // POST /v1/gateway/tools (incl. OpenAPI discovery in the Add panel)
  editToolFully: admin, // PATCH of any admin-only tool field
  editToolNote: adminOrOperator, // PATCH data_handling_note only (ai_provider tools)
  testTool: adminOrOperator, // POST /v1/gateway/tools/{id}/test
  deleteTool: admin, // DELETE /v1/gateway/tools/{id}
  // Endpoints
  // GET /v1/endpoints[/{id}] (admin, operator, viewer): B-253 gives
  // approvers no endpoint reads, so Endpoint Detail shows them a role state.
  viewEndpoints: (r: Role) => r === 'admin' || r === 'operator' || r === 'viewer',
  linkEndpointAgent: admin, // PATCH /v1/endpoints/{id}/link-agent
}

export function useOrgRole(): Role {
  return useAuthStore((s) => s.user?.role)
}
