ALTER TABLE model_pricing
    DROP COLUMN cost_per_1k_cache_write_5m,
    DROP COLUMN cost_per_1k_cache_write_1h,
    DROP COLUMN cost_per_1k_cache_read;

ALTER TABLE token_usage
    DROP COLUMN cache_creation_5m_tokens,
    DROP COLUMN cache_creation_1h_tokens,
    DROP COLUMN cache_read_tokens;
