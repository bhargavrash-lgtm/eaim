-- B-284: "latest report" is now chosen by the server's receive time
-- (received_at, set by the API on insert), not the agent-reported
-- collected_at. A skewed or deliberately set agent clock could otherwise make
-- an older scan -- or a future-dated one, permanently -- count as latest.
-- This index serves that per-endpoint LIMIT 1 pick.
CREATE INDEX IF NOT EXISTS idx_reports_endpoint_received
    ON endpoint_reports (endpoint_id, received_at DESC);
