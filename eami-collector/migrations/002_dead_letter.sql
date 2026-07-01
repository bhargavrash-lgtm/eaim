-- dead_letter: reports that could not be delivered and will not be retried.
-- Rows land here on HTTP 4xx from the SaaS API (bad data) or after
-- exceeding the max retry threshold (default 10 attempts).
CREATE TABLE IF NOT EXISTS dead_letter (
    id           TEXT PRIMARY KEY,
    received_at  TEXT NOT NULL,            -- RFC3339 UTC (original ingest time)
    agent_id     TEXT NOT NULL,
    hostname     TEXT NOT NULL,
    report_json  BLOB NOT NULL,            -- gzip-compressed JSON (preserved as-is)
    attempts     INTEGER NOT NULL,
    failed_at    TEXT NOT NULL,            -- RFC3339 UTC (time moved to dead-letter)
    reason       TEXT NOT NULL            -- "http_4xx:<status>", "max_attempts"
);

CREATE INDEX IF NOT EXISTS idx_dead_letter_failed ON dead_letter(failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_dead_letter_agent  ON dead_letter(agent_id);
