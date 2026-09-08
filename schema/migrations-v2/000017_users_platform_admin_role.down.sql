-- Reverting this migration with any existing platform_admin row still
-- present would violate the restored 4-tier CHECK immediately -- same
-- class of caveat as any other role/enum narrowing down-migration in
-- this schema. Down migrations here are a rollback aid for a schema that
-- was never populated with the new value in production, not a
-- data-preserving demotion path.
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin','operator','approver','viewer'));
