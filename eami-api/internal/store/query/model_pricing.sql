-- name: ListModelPricing :many
SELECT model, cost_per_1k_in, cost_per_1k_out, cost_per_1k_cache_write_5m, cost_per_1k_cache_write_1h, cost_per_1k_cache_read, updated_at
FROM model_pricing
ORDER BY model;

-- name: CreateModelPricing :one
INSERT INTO model_pricing (model, cost_per_1k_in, cost_per_1k_out, cost_per_1k_cache_write_5m, cost_per_1k_cache_write_1h, cost_per_1k_cache_read)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING model, cost_per_1k_in, cost_per_1k_out, cost_per_1k_cache_write_5m, cost_per_1k_cache_write_1h, cost_per_1k_cache_read, updated_at;

-- name: UpdateModelPricing :one
UPDATE model_pricing SET
    cost_per_1k_in             = COALESCE($2, cost_per_1k_in),
    cost_per_1k_out            = COALESCE($3, cost_per_1k_out),
    cost_per_1k_cache_write_5m = COALESCE($4, cost_per_1k_cache_write_5m),
    cost_per_1k_cache_write_1h = COALESCE($5, cost_per_1k_cache_write_1h),
    cost_per_1k_cache_read     = COALESCE($6, cost_per_1k_cache_read),
    updated_at                 = NOW()
WHERE model = $1
RETURNING model, cost_per_1k_in, cost_per_1k_out, cost_per_1k_cache_write_5m, cost_per_1k_cache_write_1h, cost_per_1k_cache_read, updated_at;

-- name: DeleteModelPricing :execrows
DELETE FROM model_pricing WHERE model = $1;
