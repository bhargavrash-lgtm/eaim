-- Proves the migration runner mechanism end-to-end (B-051 acceptance
-- criteria) without making an actual schema change, per this brief's own
-- explicit scope boundary (no new tables/columns).
--
-- Deliberately chosen to be genuinely harmless: COMMENT ON TABLE is pure
-- metadata -- no new column, no new constraint, no data touched, no
-- application code reads or depends on it (confirmed: nothing in any of
-- the 5 Go modules queries pg_description/table comments). Safe to leave
-- in place indefinitely and trivially reversible (see .down.sql).
COMMENT ON TABLE orgs IS 'EAMI organizations -- see schema/schema.sql for the full documented shape.';
