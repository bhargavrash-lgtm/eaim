ALTER TABLE approval_requests DROP CONSTRAINT approval_requests_resume_outcome_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_resume_outcome_check
    CHECK (resume_outcome IN ('dispatched','config_changed','connector_deleted','static_fallback'));
