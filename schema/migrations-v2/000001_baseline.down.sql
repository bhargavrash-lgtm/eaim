-- Deliberate no-op. Per B-051's contract, this migration runner pairs with
-- B-029's existing backup mechanism (scripts/backup-db.sh) as the real
-- safety net rather than true reversible down-migrations: a genuine down
-- for the full baseline would mean dropping every table in the product,
-- which is never the right response to a failed migration attempt.
--
-- If you need to undo an update: restore the pre-update backup (see
-- RECOVERY.md) rather than running `migrate down`.
SELECT 1;
