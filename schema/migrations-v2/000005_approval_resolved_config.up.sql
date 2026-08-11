-- Closes a real TOCTOU gap found live during the AI Provider Connector
-- brief's own verification: approval.Router's resume logic re-resolved a
-- tool by (org_id, name) at approval time, with no way to detect that its
-- base_url/credentials/provider had changed since the human approver
-- reviewed the request -- a lower-privileged admin/operator role (which
-- cannot itself approve/deny) could edit a pending escalation's target
-- connector during the hold window and silently redirect the approved
-- call to a different destination than what was actually reviewed.
--
-- resolved_tool_id/resolved_config_hash are set once, at Submit() time
-- (escalation), pinning the exact connector + a fingerprint of its
-- security-relevant config (base_url/provider + credentials_encrypted --
-- the encrypted bytes, never plaintext) that was live at that moment.
-- resume_outcome is set once, at resume (approval) time, recording what
-- actually happened: the pinned connector was found unchanged and
-- dispatched to ('dispatched'), or resume was refused because the
-- connector's config changed ('config_changed') or the connector was
-- deleted ('connector_deleted') since escalation, or no dynamic
-- resolution ever applied to this call ('static_fallback', the
-- pre-existing behavior for tool names that never matched a registered
-- connector). All three columns are nullable: a request escalated before
-- this migration, or one whose Tool never resolved dynamically, has none
-- of this to record.
-- ON DELETE SET NULL (deliberately different from this table's other FK
-- columns, e.g. policy_id/agent_id, which have no ON DELETE clause):
-- gateway_tools rows are routinely deleted by admins (DeleteTool), and a
-- historical approval record referencing one must never block that --
-- found live by this brief's own test suite, which tried to delete a
-- referenced tool and got blocked by the naive FK. dispatchApproved's own
-- fail-closed check doesn't depend on this column surviving the cascade
-- anyway: it re-fetches by the id captured in-memory in Request at
-- Submit() time, so a nulled resolved_tool_id here (a live row's audit
-- trail) doesn't weaken the resume-time verification at all.
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS resolved_tool_id UUID REFERENCES gateway_tools(id) ON DELETE SET NULL;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS resolved_config_hash TEXT;
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS resume_outcome TEXT
    CHECK (resume_outcome IN ('dispatched','config_changed','connector_deleted','static_fallback'));
