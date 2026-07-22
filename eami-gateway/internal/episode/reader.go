package episode

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultLimit = 25
	maxLimit     = 100
)

// ListResult is the return value of Reader.List: episodes plus the
// pagination values actually applied (post-clamping), so callers can report
// accurate metadata without duplicating the clamping logic.
type ListResult struct {
	Episodes []Episode
	Total    int64
	Limit    int
	Offset   int
}

// Reader serves read-only episode queries for the gateway's HTTP surface.
// Pagination defaulting/clamping lives here rather than in the HTTP handler
// so it's unit-testable against a fake store without an HTTP round trip.
type Reader struct {
	store episodeStore
}

// NewReader returns a Reader backed by pool.
func NewReader(pool *pgxpool.Pool) *Reader {
	return &Reader{store: &pgxEpisodeStore{pool: pool}}
}

// NewReaderWithStore returns a Reader backed by an arbitrary episodeStore —
// the test constructor, mirroring audit.NewWithDB.
func NewReaderWithStore(store episodeStore) *Reader {
	return &Reader{store: store}
}

// List returns paginated episodes for orgID. limit <= 0 defaults to
// defaultLimit; limit > maxLimit is clamped to maxLimit; offset < 0 is
// clamped to 0.
func (r *Reader) List(ctx context.Context, orgID uuid.UUID, outcome string, limit, offset int) (ListResult, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	episodes, total, err := r.store.ListByOrg(ctx, orgID, outcome, limit, offset)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Episodes: episodes, Total: total, Limit: limit, Offset: offset}, nil
}

// Get returns the episode with the given id, scoped to orgID.
func (r *Reader) Get(ctx context.Context, id, orgID uuid.UUID) (*Episode, error) {
	return r.store.GetByID(ctx, id, orgID)
}

// Search does a text search on the task column, scoped to orgID.
func (r *Reader) Search(ctx context.Context, orgID uuid.UUID, query string) ([]Episode, error) {
	return r.store.SearchByOrg(ctx, orgID, query)
}
