-- B-173: closes the concurrency race disclosed in B-171's own review
-- (see eami-gateway/internal/license/store.go's WithinUsageLimit/
-- ReserveIfNearLimit doc comments for the full reasoning). token_usage is
-- populated asynchronously (B-099's fire-and-forget goroutine, well after
-- a dispatch completes), so a burst of concurrent/rapid dispatches can
-- each pass WithinUsageLimit's SUM check before any of the prior calls'
-- own usage has landed. This table lets ReserveIfNearLimit track "a
-- dispatch is currently in flight, near this org's cap, and not yet
-- confirmed landed" -- once usage is within a configurable safety margin
-- of the license's limit, at most one in-flight reservation is allowed
-- per org at a time.
--
-- Deliberately NOT sized by an estimated token amount -- see
-- ReserveIfNearLimit's own doc comment for why a per-call estimate was
-- explicitly rejected. A row here is a pure "something is in flight"
-- marker, not a quantity.
--
-- expires_at is a short, configurable TTL (internal/config.Licensing.
-- UsageLimitInflightTTLSeconds), not tied to any specific dispatch
-- lifecycle event -- a crashed/panicked dispatch goroutine that never
-- reaches its own release() defer must not permanently wedge an org's
-- near-cap dispatches. Rows are deleted proactively by ReserveIfNearLimit's
-- release() on the normal path; the TTL is purely the safety net.
CREATE TABLE usage_dispatch_inflight (
    id         UUID NOT NULL DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_usage_dispatch_inflight_org_expires
    ON usage_dispatch_inflight (org_id, expires_at);
