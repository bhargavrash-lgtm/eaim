-- name: GetModelPricing :one
SELECT cost_per_1k_in, cost_per_1k_out
FROM model_pricing
WHERE model = $1;

-- name: InsertTokenUsage :exec
INSERT INTO token_usage
    (org_id, agent_id, agent_name, model, tokens_in, tokens_out, cost_usd, recorded_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
