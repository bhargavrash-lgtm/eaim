-- B-157 epic, Brief 2 of 3 (usage limits + platform-admin tier + B-113
-- fix). Adds a genuinely distinct 5th role tier, 'platform_admin',
-- alongside the existing 4-tier admin/operator/approver/viewer set
-- (schema.sql:36, unchanged since baseline) -- NOT a relabeling or
-- superset of 'admin'.
--
-- Deliberately NOT wired into eami-api's own self-service role-assignment
-- endpoints (InviteUser/UpdateUserRole, users.go) -- see that file's own
-- comment for why. A platform_admin user is provisioned only by a direct
-- SQL statement against this column, the same trust class as the
-- appliance's own RSA signing keys (internal/license's own doc comment):
-- deliberately harder to obtain than ordinary admin, closing B-113 for
-- real rather than symbolically (an ordinary org admin has no path, API
-- or otherwise short of direct DB access, to grant itself or a teammate
-- this tier).
--
-- The constraint name below (users_role_check) is Postgres's own default
-- name for an unnamed single-column CHECK, confirmed against this
-- database's real pg_constraint catalog before writing this migration
-- (not assumed) -- the same convention migration 000016's own
-- approval_requests_resume_outcome_check already relies on.
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin','operator','approver','viewer','platform_admin'));
