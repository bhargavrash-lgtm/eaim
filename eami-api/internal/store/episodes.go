package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Episode mirrors the episodes table row.
type Episode struct {
	ID         uuid.UUID       `json:"id"`
	OrgID      uuid.UUID       `json:"org_id"`
	AgentID    *uuid.UUID      `json:"agent_id,omitempty"`
	AgentName  string          `json:"agent_name"`
	Task       string          `json:"task"`
	Steps      json.RawMessage `json:"steps"`   // JSONB array of EpisodeStep
	Outcome    string          `json:"outcome"` // success | blocked | failed | partial
	TokenTotal int             `json:"token_total"`
	ApprovedBy *string         `json:"approved_by,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ListEpisodesParams holds filters and pagination for ListEpisodes.
type ListEpisodesParams struct {
	OrgID   uuid.UUID
	Outcome string // empty = all outcomes
	Limit   int
	Offset  int
}

// ListEpisodes returns paginated episodes for an org, newest first.
// Returns the slice (never nil), total count across all pages, and any error.
func (q *Queries) ListEpisodes(ctx context.Context, p ListEpisodesParams) ([]Episode, int64, error) {
	db := q.DB()
	pgOrg := pgtype.UUID{Bytes: p.OrgID, Valid: true}

	const countSQL = `
		SELECT COUNT(*) FROM episodes
		WHERE org_id = $1 AND ($2 = '' OR outcome = $2)
	`
	var total int64
	if err := db.QueryRow(ctx, countSQL, pgOrg, p.Outcome).Scan(&total); err != nil {
		return nil, 0, err
	}

	const listSQL = `
		SELECT id, org_id, agent_id, agent_name, task, steps, outcome,
		       token_total, approved_by, created_at
		FROM episodes
		WHERE org_id = $1 AND ($2 = '' OR outcome = $2)
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`
	rows, err := db.Query(ctx, listSQL, pgOrg, p.Outcome, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	episodes := []Episode{}
	for rows.Next() {
		e, scanErr := scanEpisodeRow(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		episodes = append(episodes, e)
	}
	return episodes, total, rows.Err()
}

// SearchEpisodes does a case-insensitive text search on the task column.
// Returns up to 20 matching episodes, newest first.
// Vector similarity search is deferred until ADR-009 (LLM endpoint) resolves.
func (q *Queries) SearchEpisodes(ctx context.Context, orgID uuid.UUID, query string) ([]Episode, error) {
	db := q.DB()
	pgOrg := pgtype.UUID{Bytes: orgID, Valid: true}

	const sql = `
		SELECT id, org_id, agent_id, agent_name, task, steps, outcome,
		       token_total, approved_by, created_at
		FROM episodes
		WHERE org_id = $1 AND task ILIKE '%' || $2 || '%'
		ORDER BY created_at DESC
		LIMIT 20
	`
	rows, err := db.Query(ctx, sql, pgOrg, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	episodes := []Episode{}
	for rows.Next() {
		e, scanErr := scanEpisodeRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		episodes = append(episodes, e)
	}
	return episodes, rows.Err()
}

// scanEpisodeRow scans one row from a pgx.Rows into an Episode.
func scanEpisodeRow(rows pgx.Rows) (Episode, error) {
	var e Episode
	var id, orgID pgtype.UUID
	var agentID pgtype.UUID
	var approvedBy pgtype.Text
	var stepsRaw []byte

	if err := rows.Scan(
		&id, &orgID, &agentID, &e.AgentName, &e.Task,
		&stepsRaw, &e.Outcome, &e.TokenTotal, &approvedBy, &e.CreatedAt,
	); err != nil {
		return e, err
	}
	e.ID = uuid.UUID(id.Bytes)
	e.OrgID = uuid.UUID(orgID.Bytes)
	if agentID.Valid {
		aid := uuid.UUID(agentID.Bytes)
		e.AgentID = &aid
	}
	if approvedBy.Valid {
		s := approvedBy.String
		e.ApprovedBy = &s
	}
	if len(stepsRaw) > 0 {
		e.Steps = json.RawMessage(stepsRaw)
	} else {
		e.Steps = json.RawMessage("[]")
	}
	return e, nil
}
