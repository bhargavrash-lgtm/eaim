package api

import "net/http"

// ListMemoryEpisodes handles GET /v1/memory/episodes.
// Stub: the episode recorder is not yet built. Returns an empty paginated list.
func (s *Server) ListMemoryEpisodes(w http.ResponseWriter, r *http.Request) {
	type meta struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	}
	type response struct {
		Data []struct{} `json:"data"`
		Meta meta       `json:"meta"`
	}
	writeJSON(w, http.StatusOK, response{
		Data: []struct{}{},
		Meta: meta{Total: 0, Page: 1, PerPage: 25},
	})
}

// SearchMemoryEpisodes handles GET /v1/memory/episodes/search.
// Stub: returns an empty result set.
func (s *Server) SearchMemoryEpisodes(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Data []struct{} `json:"data"`
	}
	writeJSON(w, http.StatusOK, response{Data: []struct{}{}})
}
