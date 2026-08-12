-- name: ListWorkflows :many
SELECT
    w.id, w.org_id, w.name, w.status, w.created_by, w.created_at, w.updated_at,
    COUNT(ws.id) AS step_count
FROM workflows w
LEFT JOIN workflow_steps ws ON ws.workflow_id = w.id
WHERE w.org_id = $1
GROUP BY w.id
ORDER BY w.name ASC;

-- name: GetWorkflow :one
SELECT id, org_id, name, status, created_by, created_at, updated_at
FROM workflows
WHERE id = $1 AND org_id = $2
LIMIT 1;

-- name: ListWorkflowSteps :many
SELECT
    ws.id, ws.workflow_id, ws.step_order, ws.gateway_tool_id,
    gt.name AS tool_name, ws.action, ws.input_mapping
FROM workflow_steps ws
LEFT JOIN gateway_tools gt ON gt.id = ws.gateway_tool_id
WHERE ws.workflow_id = $1
ORDER BY ws.step_order ASC;

-- name: CreateWorkflow :one
INSERT INTO workflows (org_id, name, status, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id, org_id, name, status, created_by, created_at, updated_at;

-- name: InsertWorkflowStep :exec
INSERT INTO workflow_steps (workflow_id, step_order, gateway_tool_id, action)
VALUES ($1, $2, $3, $4);

-- name: DeleteWorkflowSteps :exec
DELETE FROM workflow_steps WHERE workflow_id = $1;

-- name: UpdateWorkflow :one
UPDATE workflows SET
    name       = COALESCE($3, name),
    status     = COALESCE($4, status),
    updated_at = NOW()
WHERE id = $1 AND org_id = $2
RETURNING id, org_id, name, status, created_by, created_at, updated_at;

-- name: DeleteWorkflow :exec
DELETE FROM workflows WHERE id = $1 AND org_id = $2;

-- name: ToolBelongsToOrg :one
SELECT EXISTS(SELECT 1 FROM gateway_tools WHERE id = $1 AND org_id = $2);
