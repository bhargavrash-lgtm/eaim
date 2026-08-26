-- name: GetModelPricing :one
SELECT cost_per_1k_in, cost_per_1k_out
FROM model_pricing
WHERE model = $1;

-- name: InsertTokenUsage :exec
INSERT INTO token_usage
    (org_id, agent_id, agent_name, model, tool_name, tokens_in, tokens_out, cost_usd,
     cache_creation_5m_tokens, cache_creation_1h_tokens, cache_read_tokens, recorded_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);
