-- name: BatchInsertPasteEvents :execrows
-- Append-only batch write -- single multi-row INSERT via unnest() over
-- parallel arrays, no ON CONFLICT, no dedup/collapsing.
INSERT INTO paste_events
    (org_id, source_endpoint_id, destination_domain, content_length, content_hash, os_username, occurred_at)
SELECT * FROM unnest(
    $1::uuid[], $2::uuid[], $3::text[], $4::int[], $5::text[], $6::text[], $7::timestamptz[]
);

-- name: ResolvePasteSourceEndpoint :one
-- Find-or-create by (org_id, agent_id), like UpsertAgentEndpoint, but does
-- NOT touch hostname/agent_version/os_info on conflict -- a paste event
-- only ever carries a hostname, and must never blank out richer metadata
-- the full eami-agent scan pipeline already wrote for this machine.
INSERT INTO endpoints (org_id, agent_id, hostname, last_seen)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (org_id, agent_id) DO UPDATE SET
    last_seen = NOW()
RETURNING id;

-- name: ListPasteEvents :many
-- B-038: admin read UI. Domain/from/to all optional. Matches
-- idx_paste_events_org_domain when domain is given, idx_paste_events_org
-- otherwise.
SELECT id, org_id, source_endpoint_id, destination_domain, content_length, content_hash, os_username, occurred_at, received_at
FROM paste_events
WHERE org_id = $1
  AND ($2::text IS NULL OR destination_domain = $2)
  AND ($3::timestamptz IS NULL OR occurred_at >= $3)
  AND ($4::timestamptz IS NULL OR occurred_at <= $4)
ORDER BY occurred_at DESC
LIMIT $5 OFFSET $6;

-- name: CountPasteEvents :one
-- Same filters as ListPasteEvents, for pagination metadata.
SELECT COUNT(*) FROM paste_events
WHERE org_id = $1
  AND ($2::text IS NULL OR destination_domain = $2)
  AND ($3::timestamptz IS NULL OR occurred_at >= $3)
  AND ($4::timestamptz IS NULL OR occurred_at <= $4);
