-- report_buffer: incoming agent reports waiting to be forwarded to the SaaS API.
-- report_json is gzip-compressed JSON of the full agent Report payload.
CREATE TABLE IF NOT EXISTS report_buffer (
    id           TEXT PRIMARY KEY,         -- UUID v4
    received_at  TEXT NOT NULL,            -- RFC3339 UTC
    agent_id     TEXT NOT NULL,
    hostname     TEXT NOT NULL,
    report_json  BLOB NOT NULL,            -- gzip-compressed JSON
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_attempt TEXT                      -- RFC3339 UTC, NULL until first attempt
);

CREATE INDEX IF NOT EXISTS idx_buffer_received ON report_buffer(received_at ASC);
CREATE INDEX IF NOT EXISTS idx_buffer_attempts ON report_buffer(attempts ASC);
