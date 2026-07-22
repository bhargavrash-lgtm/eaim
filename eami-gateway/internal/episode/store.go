package episode

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Episode mirrors an episodes table row, including full step content.
//
// Field names and JSON tags intentionally match eami-api/internal/store.Episode
// 1:1. Duplicated rather than imported: eami-api and eami-gateway are separate
// Go modules (github.com/eami/api vs github.com/eami/gateway), so a shared
// internal/ package isn't possible without a third module. Brief 3 maps
// between this type and eami-api's own Episode when eami-api proxies to this
// endpoint.
type Episode struct {
	ID         uuid.UUID       `json:"id"`
	OrgID      uuid.UUID       `json:"org_id"`
	AgentID    *uuid.UUID      `json:"agent_id,omitempty"`
	AgentName  string          `json:"agent_name"`
	Task       string          `json:"task"`
	Steps      json.RawMessage `json:"steps"`
	Outcome    string          `json:"outcome"`
	TokenTotal int             `json:"token_total"`
	ApprovedBy *string         `json:"approved_by,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ErrNotFound is returned by episodeStore.GetByID when no row matches the
// given (id, orgID) pair. This includes the case where id exists but belongs
// to a different org — the two cases are indistinguishable on purpose, so a
// cross-org probe can't be used to test whether an id exists at all.
var ErrNotFound = errors.New("episode: not found")

// episodeStore is the read persistence interface. Modeled on
// audit.WriterDB (see internal/audit/writer.go): production wraps
// *pgxpool.Pool (pgxEpisodeStore), tests inject a hand-rolled fake — no
// testcontainers, matching this package's existing convention.
type episodeStore interface {
	// ListByOrg returns paginated episodes for orgID, newest first, plus the
	// total count across all pages. outcome == "" matches any outcome.
	ListByOrg(ctx context.Context, orgID uuid.UUID, outcome string, limit, offset int) ([]Episode, int64, error)

	// GetByID returns the episode with the given id, scoped to orgID.
	// Returns ErrNotFound if no such row exists for that org.
	GetByID(ctx context.Context, id, orgID uuid.UUID) (*Episode, error)

	// SearchByOrg does a case-insensitive text search on the task column,
	// scoped to orgID. Returns up to 20 matches, newest first. Vector
	// similarity search is deferred until ADR-009 resolves (see recorder.go's
	// placeholderEmbedding) — this stays parity with eami-api's current
	// ILIKE-based search, not an improvement on it.
	SearchByOrg(ctx context.Context, orgID uuid.UUID, query string) ([]Episode, error)
}

// pgxEpisodeStore adapts *pgxpool.Pool to episodeStore. Not unit tested
// directly (requires a real Postgres instance) — exercised indirectly
// through Reader's tests via a fake, consistent with audit.pgxpoolDB.
type pgxEpisodeStore struct {
	pool *pgxpool.Pool
}

func (s *pgxEpisodeStore) ListByOrg(ctx context.Context, orgID uuid.UUID, outcome string, limit, offset int) ([]Episode, int64, error) {
	pgOrg := pgtype.UUID{Bytes: orgID, Valid: true}

	const countSQL = `
		SELECT COUNT(*) FROM episodes
		WHERE org_id = $1 AND ($2 = '' OR outcome = $2)
	`
	var total int64
	if err := s.pool.QueryRow(ctx, countSQL, pgOrg, outcome).Scan(&total); err != nil {
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
	rows, err := s.pool.Query(ctx, listSQL, pgOrg, outcome, limit, offset)
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

func (s *pgxEpisodeStore) GetByID(ctx context.Context, id, orgID uuid.UUID) (*Episode, error) {
	pgID := pgtype.UUID{Bytes: id, Valid: true}
	pgOrg := pgtype.UUID{Bytes: orgID, Valid: true}

	const sql = `
		SELECT id, org_id, agent_id, agent_name, task, steps, outcome,
		       token_total, approved_by, created_at
		FROM episodes
		WHERE id = $1 AND org_id = $2
	`
	e, err := scanEpisodeRow(s.pool.QueryRow(ctx, sql, pgID, pgOrg))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (s *pgxEpisodeStore) SearchByOrg(ctx context.Context, orgID uuid.UUID, query string) ([]Episode, error) {
	pgOrg := pgtype.UUID{Bytes: orgID, Valid: true}

	const sql = `
		SELECT id, org_id, agent_id, agent_name, task, steps, outcome,
		       token_total, approved_by, created_at
		FROM episodes
		WHERE org_id = $1 AND task ILIKE '%' || $2 || '%'
		ORDER BY created_at DESC
		LIMIT 20
	`
	rows, err := s.pool.Query(ctx, sql, pgOrg, query)
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

// scanRow is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query), so
// GetByID and the list/search methods share one scan implementation.
type scanRow interface {
	Scan(dest ...any) error
}

func scanEpisodeRow(row scanRow) (Episode, error) {
	var e Episode
	var id, orgID pgtype.UUID
	var agentID pgtype.UUID
	var approvedBy pgtype.Text
	var stepsRaw []byte

	if err := row.Scan(
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
