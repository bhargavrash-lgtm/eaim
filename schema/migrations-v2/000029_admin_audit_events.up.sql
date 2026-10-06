-- B-269 Slice 0b (decision D2; B-224's first slice): minimal admin audit trail.
--
-- A separate table with its own hash chain per org, deliberately NOT
-- audit_log (B-269_SLICE0B_PART_A_INVESTIGATION.md §1): audit_log's chain is
-- single-writer (the gateway's in-process mutex) and its hash doesn't cover
-- a change summary.
--
-- Written only by eami-api's store.AppendAdminAuditEvent, inside the
-- change's own transaction, under a per-org advisory lock. UNIQUE (org_id,
-- seq) is the backstop: a writer that skipped the lock fails instead of
-- forking the chain. The hash covers every column except hash itself.
--
-- Never deleted by the product (B0b-6): no partitions, no ON DELETE
-- CASCADE, append-only triggers, and the down migration refuses while any
-- row exists. The triggers stop application bugs, not a database
-- superuser; the app role is currently a superuser (B-299).
--
-- summary is TEXT, not JSONB: the bytes hashed are the bytes stored. JSONB
-- would reorder keys and re-render numbers. It is built only by typed Go
-- constructors and never carries values (keys, credentials, raw config,
-- error text or admin-typed text).

CREATE TABLE IF NOT EXISTS admin_audit_events (
    id            UUID        NOT NULL DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id        UUID        NOT NULL REFERENCES orgs(id) ON DELETE RESTRICT,
    seq           BIGINT      NOT NULL CHECK (seq >= 1),
    occurred_at   TIMESTAMPTZ NOT NULL,
    actor_type    TEXT        NOT NULL CHECK (actor_type IN ('user', 'system')),
    actor_user_id UUID,
    actor_role    TEXT        NOT NULL CHECK (actor_role ~ '^[a-z_]{1,32}$'),
    action        TEXT        NOT NULL CHECK (action ~ '^[a-z][a-z_]{0,40}\.[a-z][a-z_]{0,40}$'),
    target_type   TEXT        NOT NULL CHECK (target_type ~ '^[a-z][a-z_]{0,40}$'),
    target_id     TEXT        NOT NULL CHECK (target_id ~ '^[A-Za-z0-9._:-]{1,128}$'),
    summary       TEXT        NOT NULL CHECK (summary IS JSON OBJECT AND octet_length(summary) <= 8192),
    source        TEXT        NOT NULL CHECK (source IN ('api', 'system')),
    request_id    TEXT                 CHECK (request_id ~ '^[A-Za-z0-9._/-]{1,64}$'),
    prev_hash     TEXT        NOT NULL CHECK (prev_hash ~ '^[0-9a-f]{64}$'),
    hash          TEXT        NOT NULL CHECK (hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT admin_audit_events_actor_check CHECK ((actor_type = 'user') = (actor_user_id IS NOT NULL)),
    CONSTRAINT admin_audit_events_org_seq_key UNIQUE (org_id, seq)
);

CREATE INDEX IF NOT EXISTS admin_audit_events_org_time_idx
    ON admin_audit_events (org_id, occurred_at DESC, seq DESC);
CREATE INDEX IF NOT EXISTS admin_audit_events_org_target_idx
    ON admin_audit_events (org_id, target_type, target_id, seq DESC);

CREATE OR REPLACE FUNCTION admin_audit_events_append_only() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'admin_audit_events is append-only (% refused)', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_admin_audit_events_no_update_delete ON admin_audit_events;
CREATE TRIGGER trg_admin_audit_events_no_update_delete
    BEFORE UPDATE OR DELETE ON admin_audit_events
    FOR EACH ROW EXECUTE FUNCTION admin_audit_events_append_only();

DROP TRIGGER IF EXISTS trg_admin_audit_events_no_truncate ON admin_audit_events;
CREATE TRIGGER trg_admin_audit_events_no_truncate
    BEFORE TRUNCATE ON admin_audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION admin_audit_events_append_only();
