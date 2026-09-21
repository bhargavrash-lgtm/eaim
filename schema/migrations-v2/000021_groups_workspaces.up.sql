-- B-207: Groups (general primitive, B-196's CMDB need) + Workspaces (a
-- specially-flagged Group, B-197's delegated sub-org tier) -- the first
-- real increment of both epics, per B-197's own investigation (this
-- session, B-197 investigation report): Groups is the general many-to-many
-- classification primitive; a Workspace is a Group that additionally gets
-- exclusive agent/endpoint/policy membership (via a plain nullable FK
-- column, not group_memberships -- a column trivially enforces "at most
-- one workspace," which is exactly the invariant Workspace membership
-- needs and Group membership deliberately does not) plus real-user RBAC
-- membership and policy-floor-inheritance semantics.
--
-- Every workspace_id column below is nullable, NULL = org-wide/global --
-- confirmed least disruptive to existing data (every current row in every
-- one of these tables has zero workspace concept today; NULL requires no
-- backfill and changes no existing row's real behavior), matching the
-- real Freshservice-confirmed pattern already cited in BACKLOG.md's B-197
-- entry (Global Settings doesn't exist until a second workspace does).

-- ─────────────────────────────────────────────────────────
-- GROUPS: general primitive (B-196)
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS groups (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    created_by  UUID REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_groups_org ON groups(org_id);

DROP TRIGGER IF EXISTS trg_groups_updated_at ON groups;
CREATE TRIGGER trg_groups_updated_at
    BEFORE UPDATE ON groups
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Many-to-many: an agent can belong to any number of Groups simultaneously
-- (e.g. "Engineering" + "Production" + "High-Risk" at once) -- the real
-- structural property that distinguishes Groups from Workspace membership
-- below (exclusive, at most one). Endpoint membership and other CI types
-- (B-196's broader taxonomy) are deliberately NOT added here -- this
-- increment scopes Group membership to gateway_agents only, the minimal
-- real primitive needed to prove out Workspace-as-a-Group; CI-type
-- polymorphism is B-196's own, separate, not-yet-scoped work.
CREATE TABLE IF NOT EXISTS group_memberships (
    group_id   UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    agent_id   UUID NOT NULL REFERENCES gateway_agents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, agent_id)
);
CREATE INDEX IF NOT EXISTS idx_group_memberships_agent ON group_memberships(agent_id);

-- ─────────────────────────────────────────────────────────
-- WORKSPACES: a specially-flagged Group (B-197)
-- ─────────────────────────────────────────────────────────
-- One-to-one with a Group row (every Workspace IS-A Group; not every
-- Group is a Workspace) -- group_id gives a workspace the same
-- CI-relationship/assignment surface Groups already has, without
-- duplicating that mechanism or requiring group_memberships to also
-- represent workspace membership (which needs exclusivity, not
-- many-to-many -- handled below via plain FK columns instead).
CREATE TABLE IF NOT EXISTS workspaces (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    group_id   UUID NOT NULL UNIQUE REFERENCES groups(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_workspaces_org ON workspaces(org_id);

DROP TRIGGER IF EXISTS trg_workspaces_updated_at ON workspaces;
CREATE TRIGGER trg_workspaces_updated_at
    BEFORE UPDATE ON workspaces
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Workspace-scoped RBAC: a genuinely separate dimension from users.role
-- (org-level), not derived from it -- a user can be an ordinary org-level
-- operator while also being workspace_admin of exactly one specific
-- workspace (B-197's own confirmed "org role vs. store role" pattern).
-- Deliberately NOT carried on the JWT (B-197 investigation, Part A.2):
-- resolved fresh per request instead, so a membership change takes effect
-- immediately rather than waiting for the bearer's existing token to
-- expire, and so the already-twice-hardened Claims struct (B-128/B-141)
-- never needs to change for this.
CREATE TABLE IF NOT EXISTS workspace_memberships (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('workspace_admin','workspace_member')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, workspace_id)
);
CREATE INDEX IF NOT EXISTS idx_workspace_memberships_user ON workspace_memberships(user_id);

-- ─────────────────────────────────────────────────────────
-- workspace_id: nullable FK, scoped to exactly the tables this
-- increment's mechanism needs (gateway_agents/endpoints for the
-- delegated-administration tier itself, policies for the policy-floor-
-- inheritance mechanism). audit_log/token_usage's own denormalized
-- workspace_id (mirroring their existing non-FK org_id convention) is
-- deliberately NOT added here -- real, natural next-increment work once
-- workspace-scoped dispatch is live, not silently included or dropped.
-- ─────────────────────────────────────────────────────────
-- gateway_agents/endpoints: ON DELETE SET NULL is correct and safe here --
-- losing workspace MEMBERSHIP when a workspace is deleted is benign, the
-- agent/endpoint just reverts to org-wide/unassigned.
ALTER TABLE gateway_agents ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE SET NULL;
ALTER TABLE endpoints      ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE SET NULL;

-- policies: ON DELETE CASCADE, deliberately NOT SET NULL -- a real, severe
-- finding from this brief's own mandatory security review, caught before
-- shipping. NULL is not "no membership" for a policy the way it is for an
-- agent/endpoint -- it is the evaluator's own signal for "global, always
-- evaluated first, ahead of every workspace-scoped rule" (evaluator.go's
-- NewEvaluator comparator; structural.go's matchesRule). SET NULL here
-- would mean deleting a workspace silently PROMOTES every policy that was
-- scoped to it into an org-wide floor policy -- including a deliberately
-- permissive ALLOW carve-out authored for that one workspace (a legitimate,
-- expected use of per-workspace priority numbering), which would then
-- outrank real org-floor DENY/ESCALATE rules for every agent in the org,
-- with no policy author ever intending an org-wide change. CASCADE removes
-- the row entirely instead -- a workspace-scoped policy has no meaning
-- once its workspace is gone, and this closes the promotion path at the
-- schema level rather than trusting an application-layer safeguard to
-- catch every future workspace-delete code path.
ALTER TABLE policies ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_gateway_agents_workspace ON gateway_agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_endpoints_workspace ON endpoints(workspace_id);
CREATE INDEX IF NOT EXISTS idx_policies_workspace ON policies(workspace_id);

-- Cross-table tenant-isolation invariant, also from this brief's own
-- mandatory security review: nothing above stops a future write path from
-- setting workspace_id to a workspace owned by a DIFFERENT org than the
-- row's own org_id (a plain FK to workspaces(id) alone can't express that
-- composite constraint). matchesRule's existing double-check (OrgID AND
-- WorkspaceID must both match) means such a corrupted row wouldn't itself
-- cause a cross-org policy match today, but this is exactly the class of
-- missing invariant that produced this codebase's two prior real
-- cross-tenant incidents (B-128, B-141) -- closed here at the schema
-- level, not left to every future write path to get right independently.
CREATE OR REPLACE FUNCTION check_workspace_org_match() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.workspace_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM workspaces w WHERE w.id = NEW.workspace_id AND w.org_id = NEW.org_id
        ) THEN
            RAISE EXCEPTION 'workspace_id % does not belong to org_id %', NEW.workspace_id, NEW.org_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_gateway_agents_workspace_org_match ON gateway_agents;
CREATE TRIGGER trg_gateway_agents_workspace_org_match
    BEFORE INSERT OR UPDATE OF workspace_id, org_id ON gateway_agents
    FOR EACH ROW EXECUTE FUNCTION check_workspace_org_match();

DROP TRIGGER IF EXISTS trg_endpoints_workspace_org_match ON endpoints;
CREATE TRIGGER trg_endpoints_workspace_org_match
    BEFORE INSERT OR UPDATE OF workspace_id, org_id ON endpoints
    FOR EACH ROW EXECUTE FUNCTION check_workspace_org_match();

DROP TRIGGER IF EXISTS trg_policies_workspace_org_match ON policies;
CREATE TRIGGER trg_policies_workspace_org_match
    BEFORE INSERT OR UPDATE OF workspace_id, org_id ON policies
    FOR EACH ROW EXECUTE FUNCTION check_workspace_org_match();

-- Real constraint name confirmed against this database's live pg_constraint
-- catalog before writing this migration (policies_org_id_priority_key),
-- matching migration 000017's own established discipline -- not assumed.
-- Widened to include workspace_id, AND switched to NULLS NOT DISTINCT
-- (PG15+, this stack confirmed on PG16.14): plain UNIQUE would NOT
-- actually enforce uniqueness among org-floor rows (workspace_id IS NULL)
-- sharing a priority number, since Postgres treats every NULL as distinct
-- from every other NULL by default in a unique constraint -- a real,
-- non-obvious gap the B-197 investigation traced specifically because it
-- feeds directly into the policy-ordering mechanism below.
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_org_id_priority_key;
ALTER TABLE policies ADD CONSTRAINT policies_org_workspace_priority_key
    UNIQUE NULLS NOT DISTINCT (org_id, workspace_id, priority) DEFERRABLE INITIALLY DEFERRED;
