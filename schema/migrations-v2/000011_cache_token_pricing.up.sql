-- B-111: caching cost-accounting -- token_usage gains three raw cache
-- token-count columns (base tokens_in/tokens_out already exist -- these
-- are the other 3 of the 5 genuinely distinct pricing tiers: 5-minute
-- cache write, 1-hour cache write, cache read). model_pricing gains the
-- matching 3 rate columns. cost_usd itself is NOT touched here -- cache
-- cost is computed at query time in finops.go, never persisted, so a
-- future rate correction here automatically re-prices historical rows.

ALTER TABLE token_usage
    ADD COLUMN cache_creation_5m_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN cache_creation_1h_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN cache_read_tokens        INTEGER NOT NULL DEFAULT 0;

ALTER TABLE model_pricing
    ADD COLUMN cost_per_1k_cache_write_5m NUMERIC(10,6),
    ADD COLUMN cost_per_1k_cache_write_1h NUMERIC(10,6),
    ADD COLUMN cost_per_1k_cache_read     NUMERIC(10,6);

-- Backfill per-row, from each model's OWN cost_per_1k_in -- never a
-- single shared multiplier value -- so distinct model generations (e.g.
-- a future claude-sonnet-5 row alongside today's claude-sonnet-4-6) each
-- get their own correctly-derived cache rates, not one collapsed rate.
-- Anthropic's documented multipliers of the base input rate, cross-
-- verified against two independent live sources on 2026-08-26: 5-minute
-- cache write = 1.25x, 1-hour cache write = 2x, cache read = 0.1x. Only
-- claude-% models ever produce cache_creation_input_tokens/
-- cache_read_input_tokens in their response (Anthropic Messages
-- API-specific field names) -- non-Claude rows keep NULL cache rates,
-- harmless since their cache token counts will always be 0.
UPDATE model_pricing
SET cost_per_1k_cache_write_5m = cost_per_1k_in * 1.25,
    cost_per_1k_cache_write_1h = cost_per_1k_in * 2.0,
    cost_per_1k_cache_read     = cost_per_1k_in * 0.1
WHERE model LIKE 'claude-%';
