// reader_test.go — eami-gateway/internal/episode
// Unit tests for Reader against a hand-rolled fake store (no Postgres),
// mirroring the audit package's WriterDB/fakeDB seam pattern.
package episode_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/episode"
)

// fakeStore is a hand-rolled episodeStore double. episodeStore itself is
// unexported, but Go's structural typing means fakeStore satisfies it
// without this package ever needing to name the interface.
type fakeStore struct {
	listEpisodes []episode.Episode
	listTotal    int64
	listErr      error
	gotOutcome   string
	gotLimit     int
	gotOffset    int

	getEpisode *episode.Episode
	getErr     error

	searchEpisodes []episode.Episode
	searchErr      error
	gotQuery       string
}

func (f *fakeStore) ListByOrg(_ context.Context, _ uuid.UUID, outcome string, limit, offset int) ([]episode.Episode, int64, error) {
	f.gotOutcome, f.gotLimit, f.gotOffset = outcome, limit, offset
	return f.listEpisodes, f.listTotal, f.listErr
}

func (f *fakeStore) GetByID(_ context.Context, _, _ uuid.UUID) (*episode.Episode, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getEpisode, nil
}

func (f *fakeStore) SearchByOrg(_ context.Context, _ uuid.UUID, query string) ([]episode.Episode, error) {
	f.gotQuery = query
	return f.searchEpisodes, f.searchErr
}

func TestReader_List_ZeroLimit_UsesDefault(t *testing.T) {
	store := &fakeStore{}
	r := episode.NewReaderWithStore(store)

	if _, err := r.List(context.Background(), uuid.New(), "", 0, 0); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.gotLimit != 25 {
		t.Errorf("gotLimit = %d, want default 25", store.gotLimit)
	}
}

func TestReader_List_LimitAboveMax_ClampedToMax(t *testing.T) {
	store := &fakeStore{}
	r := episode.NewReaderWithStore(store)

	if _, err := r.List(context.Background(), uuid.New(), "", 500, 0); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.gotLimit != 100 {
		t.Errorf("gotLimit = %d, want clamped max 100", store.gotLimit)
	}
}

func TestReader_List_NegativeOffset_ClampedToZero(t *testing.T) {
	store := &fakeStore{}
	r := episode.NewReaderWithStore(store)

	if _, err := r.List(context.Background(), uuid.New(), "", 10, -5); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.gotOffset != 0 {
		t.Errorf("gotOffset = %d, want clamped 0", store.gotOffset)
	}
}

func TestReader_List_PassesOutcomeFilterThrough(t *testing.T) {
	store := &fakeStore{}
	r := episode.NewReaderWithStore(store)

	if _, err := r.List(context.Background(), uuid.New(), "blocked", 10, 0); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.gotOutcome != "blocked" {
		t.Errorf("gotOutcome = %q, want %q", store.gotOutcome, "blocked")
	}
}

func TestReader_List_ReturnsAppliedPaginationInResult(t *testing.T) {
	store := &fakeStore{listTotal: 42}
	r := episode.NewReaderWithStore(store)

	result, err := r.List(context.Background(), uuid.New(), "", 500, -5)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if result.Limit != 100 || result.Offset != 0 {
		t.Errorf("result = %+v, want clamped Limit=100 Offset=0", result)
	}
	if result.Total != 42 {
		t.Errorf("result.Total = %d, want 42", result.Total)
	}
}

func TestReader_Get_StoreReturnsErrNotFound_Propagates(t *testing.T) {
	store := &fakeStore{getErr: episode.ErrNotFound}
	r := episode.NewReaderWithStore(store)

	_, err := r.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, episode.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestReader_Get_Found_ReturnsEpisodeWithSteps(t *testing.T) {
	steps := json.RawMessage(`[{"tool_name":"salesforce","action":"read","decision":"allowed"}]`)
	store := &fakeStore{getEpisode: &episode.Episode{
		ID:        uuid.New(),
		Steps:     steps,
		Outcome:   "success",
		CreatedAt: time.Now(),
	}}
	r := episode.NewReaderWithStore(store)

	got, err := r.Get(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Steps) == 0 {
		t.Fatal("Steps is empty — this is the entire point of Brief 1: full episode content must round-trip")
	}
	if string(got.Steps) != string(steps) {
		t.Errorf("Steps = %s, want %s", got.Steps, steps)
	}
}

func TestReader_Search_PassesQueryThrough(t *testing.T) {
	store := &fakeStore{}
	r := episode.NewReaderWithStore(store)

	if _, err := r.Search(context.Background(), uuid.New(), "salesforce"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.gotQuery != "salesforce" {
		t.Errorf("gotQuery = %q, want %q", store.gotQuery, "salesforce")
	}
}
