-- Rollback for B-301's resume outcome. Rows recorded as 'agent_not_active'
-- are set to NULL (no resume outcome) before the old CHECK is restored:
-- no older value means the same thing. Roll back the gateway binary first.
UPDATE approval_requests SET resume_outcome = NULL WHERE resume_outcome = 'agent_not_active';
ALTER TABLE approval_requests DROP CONSTRAINT approval_requests_resume_outcome_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_resume_outcome_check
    CHECK (resume_outcome IN ('dispatched','config_changed','connector_deleted','static_fallback','license_revoked','usage_limit_exceeded','usage_limit_contention'));
