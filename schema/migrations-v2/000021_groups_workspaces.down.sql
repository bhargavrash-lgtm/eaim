-- Column dropped BEFORE the old constraint is restored (security-review
-- finding, Finding 3): the up-migration's whole point is letting
-- different workspaces (and the org floor) reuse the same priority
-- number independently -- exactly the real, expected post-upgrade state
-- that would violate the old org-only UNIQUE(org_id, priority) if it were
-- restored first, aborting the rollback with a unique-violation.
ALTER TABLE policies DROP COLUMN IF EXISTS workspace_id;

ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_org_workspace_priority_key;
ALTER TABLE policies ADD CONSTRAINT policies_org_id_priority_key
    UNIQUE (org_id, priority) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE endpoints      DROP COLUMN IF EXISTS workspace_id;
ALTER TABLE gateway_agents DROP COLUMN IF EXISTS workspace_id;

DROP TRIGGER IF EXISTS trg_policies_workspace_org_match ON policies;
DROP TRIGGER IF EXISTS trg_endpoints_workspace_org_match ON endpoints;
DROP TRIGGER IF EXISTS trg_gateway_agents_workspace_org_match ON gateway_agents;
DROP FUNCTION IF EXISTS check_workspace_org_match();

DROP TABLE IF EXISTS workspace_memberships;
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS group_memberships;
DROP TABLE IF EXISTS groups;
