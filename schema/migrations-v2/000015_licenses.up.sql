-- Modular licensing & entitlement system, Brief 1 of 3 (B-157 epic, built
-- as B-169). Per the completed investigation: an on-prem, potentially
-- air-gapped appliance (ADR-020 Model A explicitly states "zero data
-- ever reaches EAMI-operated infrastructure") cannot use a live
-- "check a billing API" model -- licenses are cryptographically signed
-- (RS256 JWT) and verified entirely offline, by both eami-api (at
-- upload/write time) and eami-gateway (independently, at dispatch/read
-- time -- see eami-gateway/internal/license and eami-api/internal/license,
-- deliberately duplicated small verification packages, not shared
-- infrastructure, since the two are separate Go modules and this is
-- exactly the class of small security-relevant helper this codebase's
-- own established precedent duplicates rather than builds shared
-- infrastructure for).
--
-- raw_license is the actual signed JWT and the real source of truth;
-- every other column is a DECODED, DENORMALIZED cache of its own claims,
-- re-derived from raw_license on every write, never independently
-- editable -- mirrors audit_log.data_handling_designation's own
-- "snapshot, not a live value" precedent.
--
-- Append-only by APPLICATION discipline, matching audit_log's own
-- convention (B-121/B-124/B-125): a renewal or module change is a NEW row
-- (a new INSERT via CreateLicense), never an UPDATE -- no UpdateLicense
-- exists anywhere. Correction to this table's own earlier doc comment
-- (security review finding, this brief): this is NOT a DB-level
-- guarantee, for the same reason audit_log's own commented-out
-- `REVOKE UPDATE, DELETE ON audit_log FROM eami_app` (schema.sql,
-- migrations-v2/000001) has never actually been applied to this
-- deployment's real Postgres either -- `eami_app` is the table-owning
-- role here (confirmed empirically during B-168: a plain REVOKE against
-- one's own owned table is a no-op), so an active REVOKE would not
-- change anything without a separate, unimplemented ownership/role
-- split. Enforcement (Store.ModuleLicensed/requireModuleLicensed) is
-- correctly designed to not depend on this table being tamper-proof
-- regardless: every read independently re-verifies raw_license's
-- signature AND its signed org_id against the caller's own org (the
-- second check added by this same security review pass), so a
-- direct-DB row edit can at most self-inflict a downgrade on the
-- editor's OWN org, never fabricate entitlement.
--
-- The most recent row (by created_at, id DESC as a tiebreaker -- created_at
-- has no uniqueness guarantee) whose validity window covers "now" is the
-- effective license for an org; enforcement queries always resolve it
-- fresh, never cache it in this table itself.
--
-- modules is validated at the application layer (both eami-api's upload
-- handler and eami-gateway's independent re-verification), not a DB
-- CHECK -- matches gateway_tools.redaction_rules' own precedent for an
-- array/JSONB column whose real shape constraint lives in code, not SQL.
--
-- usage_limits (JSONB, nullable) is a deliberately SEPARATE, OPTIONAL
-- axis from modules -- feature-gating (which modules) and usage-metering
-- (how much of a module) are different concerns, per the epic's own
-- explicit item 1. NOT enforced by this brief (Brief 2's scope) -- the
-- column exists now so Brief 2 doesn't need a second migration just to
-- add it.
CREATE TABLE licenses (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    raw_license  TEXT NOT NULL,
    modules      TEXT[] NOT NULL,
    valid_from   TIMESTAMPTZ NOT NULL,
    valid_until  TIMESTAMPTZ NOT NULL,
    usage_limits JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Enforcement's own hot-path query is always "the most recent row for
-- this org, newest first" (to find the current effective license) --
-- this index serves that directly.
CREATE INDEX idx_licenses_org_created ON licenses(org_id, created_at DESC);
