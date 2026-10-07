-- B-301: an approved escalation is re-checked for liveness at resume time.
-- If the governed agent was suspended, revoked or deleted, or the call's
-- token was revoked, while the call waited for approval, the approved call
-- is NOT executed and resume_outcome records 'agent_not_active'.
ALTER TABLE approval_requests DROP CONSTRAINT approval_requests_resume_outcome_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_resume_outcome_check
    CHECK (resume_outcome IN ('dispatched','config_changed','connector_deleted','static_fallback','license_revoked','usage_limit_exceeded','usage_limit_contention','agent_not_active'));
