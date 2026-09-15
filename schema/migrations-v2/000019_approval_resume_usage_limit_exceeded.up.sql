-- Widens approval_requests.resume_outcome's CHECK constraint to allow
-- 'usage_limit_exceeded' and 'usage_limit_contention' (B-172/B-173, follow-
-- up to migration 000016's identical 'license_revoked' addition):
--
-- 'usage_limit_exceeded' -- an approved escalation whose org has exceeded
-- its license's own usage_limits WHILE the request sat in the hold window
-- is now refused at resume time (see eami-gateway/internal/approval/
-- router.go's dispatchApproved) rather than silently dispatched. Distinct
-- from 'license_revoked' (a different real condition: no valid Gateway-
-- module license at all, not "licensed but over its own volume cap").
--
-- 'usage_limit_contention' -- a DIFFERENT, narrower condition (B-173):
-- the org is near (not necessarily over) its usage cap and another
-- dispatch for that org is already in flight, not yet confirmed landed --
-- see license.Store.ReserveIfNearLimit's own doc comment. A transient
-- condition a retry can reasonably resolve, unlike 'usage_limit_exceeded'
-- (a stable fact about real recorded usage) -- kept as its own distinct
-- value for the same reason cmd/gateway/dispatcher.go's
-- rejectOnUsageLimitContention is its own function, not folded into
-- rejectOnUsageLimitExceeded.
ALTER TABLE approval_requests DROP CONSTRAINT approval_requests_resume_outcome_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_resume_outcome_check
    CHECK (resume_outcome IN ('dispatched','config_changed','connector_deleted','static_fallback','license_revoked','usage_limit_exceeded','usage_limit_contention'));
