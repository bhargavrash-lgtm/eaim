package api

import (
	"net/http"
	"strconv"

	"github.com/eami/api/internal/store"
)

// ListMemoryEpisodes handles GET /v1/memory/episodes.
// Supports ?outcome=success|blocked|failed|partial and ?page / ?per_page.
func (s *Server) ListMemoryEpisodes(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()

	page, perPage := 1, 25
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := q.Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			perPage = n
		}
	}
	outcome := q.Get("outcome") // "" = all

	episodes, total, err := s.queries.ListEpisodes(r.Context(), store.ListEpisodesParams{
		OrgID:   uc.OrgID,
		Outcome: outcome,
		Limit:   perPage,
		Offset:  (page - 1) * perPage,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "list episodes: "+err.Error())
		return
	}

	type meta struct {
		Total   int64 `json:"total"`
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": episodes,
		"meta": meta{Total: total, Page: page, PerPage: perPage},
	})
}

// SearchMemoryEpisodes handles GET /v1/memory/episodes/search?q=<text>.
// Performs case-insensitive text search on the task column.
// Vector similarity search is deferred until ADR-009 resolves.
func (s *Server) SearchMemoryEpisodes(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "q is required")
		return
	}

	episodes, err := s.queries.SearchEpisodes(r.Context(), uc.OrgID, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "search episodes: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": episodes})
}
