-- B-157 epic, Brief 2 of 3. Records "a real license creation, renewal, or
-- expiration is a real, auditable event" per this brief's own contract.
--
-- Deliberately a plain event table, NOT a new audit_log row -- audit_log
-- is hash-chained (eami-gateway/internal/audit/writer.go) and that chain
-- is serialized ONLY by an in-process sync.Mutex inside eami-gateway's
-- own Writer; GetLastHash is a bare `SELECT ... ORDER BY timestamp DESC
-- LIMIT 1` with no row-level lock (no `FOR UPDATE`, no advisory lock).
-- License creation/renewal happens in eami-api (UploadLicense), a
-- SEPARATE PROCESS with no coordination with eami-gateway's mutex --
-- writing there would make eami-api a second, uncoordinated writer
-- against a chain never designed for more than one, and two processes
-- racing to read-then-append the same "last hash" can genuinely fork the
-- chain (both read the same prev_hash, both insert, one row's prev_hash
-- no longer matches any prior row's real successor). This table sidesteps
-- that entirely, matching the established agent_lifecycle_events
-- (migration 000009)/ai_token_events (migration 000010) precedent: a
-- small, simple, non-hash-chained event table, written best-effort and
-- non-fatal to the primary action, for exactly this class of "record
-- that a thing happened" need that isn't a governed dispatch decision.
--
-- One row per (license row, event type): 'created' and 'renewed' are
-- written synchronously by eami-api's UploadLicense at insert time
-- (created = the org's first-ever license row; renewed = every
-- subsequent one). 'expired' has no natural human action to hang off of
-- -- it's written lazily, the first time GetLicense observes a license
-- whose validity window has passed 'now' -- so the UNIQUE constraint
-- below makes that write idempotent (ON CONFLICT DO NOTHING) rather than
-- accumulating one row per repeated status check.
CREATE TABLE license_events (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    license_id   UUID NOT NULL REFERENCES licenses(id) ON DELETE CASCADE,
    event_type   TEXT NOT NULL CHECK (event_type IN ('created','renewed','expired')),
    -- NULL for 'expired' -- detected by the system on read, not performed
    -- by any human. Always set for 'created'/'renewed' (the uploading
    -- admin), same convention as agent_lifecycle_events.performed_by.
    performed_by UUID,
    detail       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (license_id, event_type)
);
CREATE INDEX idx_license_events_org ON license_events(org_id, created_at DESC);
