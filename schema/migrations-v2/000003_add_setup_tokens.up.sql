-- First-boot setup wizard (B-053 follow-up): a console-displayed, single-use
-- token that gates the org/admin bootstrap endpoint. Only a SHA-256 hash is
-- ever stored -- the raw token exists only on the VM console output and in
-- the setup wizard's browser session, matching the hashing convention
-- already used for refresh tokens / API keys (eami-api/internal/auth/auth.go).
--
-- consumed_at (nullable) is set exactly once, inside the same DB transaction
-- that creates the first org+admin -- see eami-api/internal/api/bootstrap.go.
-- The bootstrap endpoint locks a row here with SELECT ... FOR UPDATE, which
-- is the real concurrency guard for "exactly one setup attempt can win a
-- race" -- not an application-level check-then-act.
CREATE TABLE IF NOT EXISTS setup_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

-- Partial index: only unconsumed tokens are ever looked up by hash (the
-- lookup in bootstrap.go always filters WHERE consumed_at IS NULL), so a
-- full index isn't needed once a token is spent.
CREATE INDEX IF NOT EXISTS idx_setup_tokens_unconsumed
    ON setup_tokens(token_hash) WHERE consumed_at IS NULL;
