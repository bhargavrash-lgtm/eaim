-- Rollback for B-269 Slice 0b. Refuses while the trail holds any row: a
-- routine `migrate down` must never destroy audit history (B0b-6). Removing
-- a non-empty trail is a deliberate, documented operator procedure (export
-- first), not a migration step.
-- The ACCESS EXCLUSIVE lock closes the window between the check and the
-- DROP: no insert can commit in between.
DO $$
BEGIN
    IF to_regclass('admin_audit_events') IS NOT NULL THEN
        LOCK TABLE admin_audit_events IN ACCESS EXCLUSIVE MODE;
    END IF;
    IF to_regclass('admin_audit_events') IS NOT NULL
       AND EXISTS (SELECT 1 FROM admin_audit_events) THEN
        RAISE EXCEPTION 'admin_audit_events is not empty; refusing to drop the admin audit trail';
    END IF;
END;
$$;

DROP TABLE IF EXISTS admin_audit_events;
DROP FUNCTION IF EXISTS admin_audit_events_append_only();
