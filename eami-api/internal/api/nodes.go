package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/eami/api/internal/store"
)

// ── Response types ────────────────────────────────────────────────────────────

type NodeResp struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	Address        string     `json:"address"`
	Hostname       *string    `json:"hostname,omitempty"`
	Version        *string    `json:"version,omitempty"`
	LastHeartbeat  *time.Time `json:"last_heartbeat,omitempty"`
	CPUPct         *float64   `json:"cpu_pct,omitempty"`
	RequestsPerMin *int32     `json:"requests_per_min,omitempty"`
}

func nodeToResp(n store.GatewayNode) NodeResp {
	r := NodeResp{
		ID: n.ID, Name: n.Name, Role: n.Role,
		Status: n.Status, Address: n.Address,
	}
	if n.Hostname.Valid {
		r.Hostname = &n.Hostname.String
	}
	if n.Version.Valid {
		r.Version = &n.Version.String
	}
	if n.LastHeartbeat.Valid {
		ts := n.LastHeartbeat.Time
		r.LastHeartbeat = &ts
	}
	if n.CPUPct.Valid {
		r.CPUPct = &n.CPUPct.Float64
	}
	if n.RequestsPerMin.Valid {
		r.RequestsPerMin = &n.RequestsPerMin.Int32
	}
	return r
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// ListNodes handles GET /v1/gateway/nodes
func (s *Server) ListNodes(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	nodes, err := s.queries.ListNodes(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := make([]NodeResp, 0, len(nodes))
	for _, n := range nodes {
		resp = append(resp, nodeToResp(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resp})
}

// DeleteNode handles DELETE /v1/gateway/nodes/{nodeId}
func (s *Server) DeleteNode(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	nodeID, err := uuid.Parse(chi.URLParam(r, "nodeId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid node ID")
		return
	}
	found, err := s.queries.DeleteNode(r.Context(), uc.OrgID, nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "node not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
