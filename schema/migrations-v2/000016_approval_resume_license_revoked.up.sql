-- Widens approval_requests.resume_outcome's CHECK constraint to allow
-- 'license_revoked' (B-169 code review finding, follow-up to migration
-- 000015): an approved escalation whose org's Gateway-module license
-- expired or was revoked while the request sat in the hold window is now
-- refused at resume time (see eami-gateway/internal/approval/router.go's
-- dispatchApproved) rather than silently dispatched -- this is that
-- outcome's own distinct, auditable value, not reused from any existing
-- one ('config_changed' describes a different real condition: the
-- resolved connector's own configuration changed, not the org's
-- licensing status).
ALTER TABLE approval_requests DROP CONSTRAINT approval_requests_resume_outcome_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_resume_outcome_check
    CHECK (resume_outcome IN ('dispatched','config_changed','connector_deleted','static_fallback','license_revoked'));
