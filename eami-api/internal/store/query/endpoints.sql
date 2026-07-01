-- name: UpsertEndpoint :exec
INSERT INTO discovered_endpoints
    (org_id, source_host, method, host, path, port, tls, tags, raw_headers, last_seen)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (org_id, source_host, method, host, path) DO UPDATE SET
    last_seen   = NOW(),
    hit_count   = discovered_endpoints.hit_count + 1,
    tags        = EXCLUDED.tags,
    raw_headers = EXCLUDED.raw_headers;

-- name: ListEndpoints :many
SELECT id, org_id, source_host, method, host, path, port, tls, tags,
       raw_headers, hit_count, last_seen, created_at
FROM discovered_endpoints
WHERE org_id = $1
  AND ($2::text IS NULL OR host        ILIKE '%' || $2 || '%')
  AND ($3::text IS NULL OR source_host ILIKE '%' || $3 || '%')
  AND ($4::text IS NULL OR $4 = ANY(tags))
ORDER BY last_seen DESC
LIMIT $5 OFFSET $6;

-- name: CountEndpoints :one
SELECT COUNT(*) FROM discovered_endpoints
WHERE org_id = $1
  AND ($2::text IS NULL OR host        ILIKE '%' || $2 || '%')
  AND ($3::text IS NULL OR source_host ILIKE '%' || $3 || '%')
  AND ($4::text IS NULL OR $4 = ANY(tags));

-- name: GetEndpoint :one
SELECT id, org_id, source_host, method, host, path, port, tls, tags,
       raw_headers, hit_count, last_seen, created_at
FROM discovered_endpoints
WHERE id = $1 AND org_id = $2;
