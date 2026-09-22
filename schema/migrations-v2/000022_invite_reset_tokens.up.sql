-- Real user provisioning (invite acceptance + password reset). Both tables
-- mirror setup_tokens (000003_add_setup_tokens.up.sql) exactly -- only a
-- SHA-256 hash of the raw token is ever stored, consumed_at (nullable) is
-- set exactly once inside the same DB transaction that uses the token
-- (accept-invite / reset-password, internal/api/provisioning.go), and the
-- real concurrency/replay guard is a `SELECT ... FOR UPDATE` row lock at
-- use time, not an application-level check-then-act.
--
-- Kept as two separate tables rather than one shared "action_tokens" table:
-- their lifecycles differ (an invite_tokens row's user_id already carries
-- its real target role, set at CreateInvitedUser time -- there is no role
-- to flip; a reset_tokens row is minted for an already-active user with an
-- existing password_hash) and conflating them would need a discriminator
-- column plus wider WHERE clauses on every read for no real benefit at
-- this scale.
CREATE TABLE IF NOT EXISTS invite_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_invite_tokens_unconsumed
    ON invite_tokens(token_hash) WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS reset_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_reset_tokens_unconsumed
    ON reset_tokens(token_hash) WHERE consumed_at IS NULL;
