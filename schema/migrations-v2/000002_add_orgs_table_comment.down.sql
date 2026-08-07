-- Clean, complete reversal -- COMMENT ON TABLE ... IS NULL removes the
-- comment entirely, restoring the exact pre-migration state. Unlike
-- 000001's baseline down, this one is a genuine, safe, real rollback,
-- since the up-migration itself was chosen to be trivially reversible.
COMMENT ON TABLE orgs IS NULL;
