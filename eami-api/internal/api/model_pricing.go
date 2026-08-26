// model_pricing.go -- admin CRUD for the model_pricing table (B-112).
//
// model_pricing has no org_id column -- it's a genuinely global,
// provider-price reference table ("maintained by DevOps/admin" per its
// own schema comment), not a per-org resource like agents/policies/tools.
// No org anywhere in this codebase has ever had its own negotiated rate
// (confirmed via investigation: no org_id column, no mention in
// ARCHITECTURE.md/ROADMAP.md), so these endpoints intentionally take no
// org-scoping parameter, unlike every other CRUD handler in this file's
// sibling files.
//
// Write access is gated admin-only (router.go), stricter than the
// admin+operator gating agents/policies/tools use -- because unlike those,
// a change here affects every org's cost reporting, not just the calling
// org's own resources. This surfaces a real gap this brief discloses
// rather than silently ignores: EAMI has exactly one role tier ("admin")
// and no separate platform-admin concept, so any org's admin can still
// change pricing that affects every other org's FinOps numbers. Building
// a platform-admin tier is a much larger, unrequested RBAC feature --
// logged as its own B-ID (see BACKLOG.md), not fixed here.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

func modelPricingRowToResp(m store.ModelPricingRow) ModelPricingResp {
	resp := ModelPricingResp{
		Model:        m.Model,
		CostPer1kIn:  m.CostPer1kIn,
		CostPer1kOut: m.CostPer1kOut,
		UpdatedAt:    m.UpdatedAt,
	}
	if m.CostPer1kCacheWrite5m.Valid {
		v := m.CostPer1kCacheWrite5m.Float64
		resp.CostPer1kCacheWrite5m = &v
	}
	if m.CostPer1kCacheWrite1h.Valid {
		v := m.CostPer1kCacheWrite1h.Float64
		resp.CostPer1kCacheWrite1h = &v
	}
	if m.CostPer1kCacheRead.Valid {
		v := m.CostPer1kCacheRead.Float64
		resp.CostPer1kCacheRead = &v
	}
	return resp
}

// optionalFloat8 converts an optional request field (nil = "not provided")
// into a pgtype.Float8 (Valid:false = "leave unchanged" for an UPDATE,
// "no rate configured" for a CREATE).
func optionalFloat8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

// validateNonNegativeRates checks every non-nil rate field in the map is
// >= 0, returning the first offending field's name for the error message.
// Shared by Create/Update since both accept the same 5 rate fields.
func validateNonNegativeRates(fields map[string]*float64) string {
	for name, v := range fields {
		if v != nil && *v < 0 {
			return name
		}
	}
	return ""
}

// ListModelPricing handles GET /v1/admin/model-pricing
func (s *Server) ListModelPricing(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListModelPricing(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := make([]ModelPricingResp, 0, len(rows))
	for _, m := range rows {
		resp = append(resp, modelPricingRowToResp(m))
	}
	writeJSON(w, http.StatusOK, ModelPricingListResponse{Data: resp})
}

// CreateModelPricing handles POST /v1/admin/model-pricing
func (s *Server) CreateModelPricing(w http.ResponseWriter, r *http.Request) {
	var req CreateModelPricingRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "model is required")
		return
	}
	if req.CostPer1kIn < 0 || req.CostPer1kOut < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "cost_per_1k_in and cost_per_1k_out must not be negative")
		return
	}
	if bad := validateNonNegativeRates(map[string]*float64{
		"cost_per_1k_cache_write_5m": req.CostPer1kCacheWrite5m,
		"cost_per_1k_cache_write_1h": req.CostPer1kCacheWrite1h,
		"cost_per_1k_cache_read":     req.CostPer1kCacheRead,
	}); bad != "" {
		writeError(w, http.StatusBadRequest, "bad_request", bad+" must not be negative")
		return
	}

	m, err := s.queries.CreateModelPricing(r.Context(), store.CreateModelPricingParams{
		Model:                 req.Model,
		CostPer1kIn:           req.CostPer1kIn,
		CostPer1kOut:          req.CostPer1kOut,
		CostPer1kCacheWrite5m: optionalFloat8(req.CostPer1kCacheWrite5m),
		CostPer1kCacheWrite1h: optionalFloat8(req.CostPer1kCacheWrite1h),
		CostPer1kCacheRead:    optionalFloat8(req.CostPer1kCacheRead),
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "conflict", "pricing for this model already exists -- use PATCH to update it")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, modelPricingRowToResp(m))
}

// UpdateModelPricing handles PATCH /v1/admin/model-pricing/{model}
func (s *Server) UpdateModelPricing(w http.ResponseWriter, r *http.Request) {
	model := chi.URLParam(r, "model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid model")
		return
	}
	var req UpdateModelPricingRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if bad := validateNonNegativeRates(map[string]*float64{
		"cost_per_1k_in":             req.CostPer1kIn,
		"cost_per_1k_out":            req.CostPer1kOut,
		"cost_per_1k_cache_write_5m": req.CostPer1kCacheWrite5m,
		"cost_per_1k_cache_write_1h": req.CostPer1kCacheWrite1h,
		"cost_per_1k_cache_read":     req.CostPer1kCacheRead,
	}); bad != "" {
		writeError(w, http.StatusBadRequest, "bad_request", bad+" must not be negative")
		return
	}

	m, err := s.queries.UpdateModelPricing(r.Context(), store.UpdateModelPricingParams{
		Model:                 model,
		CostPer1kIn:           optionalFloat8(req.CostPer1kIn),
		CostPer1kOut:          optionalFloat8(req.CostPer1kOut),
		CostPer1kCacheWrite5m: optionalFloat8(req.CostPer1kCacheWrite5m),
		CostPer1kCacheWrite1h: optionalFloat8(req.CostPer1kCacheWrite1h),
		CostPer1kCacheRead:    optionalFloat8(req.CostPer1kCacheRead),
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "no pricing configured for this model")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, modelPricingRowToResp(m))
}

// DeleteModelPricing handles DELETE /v1/admin/model-pricing/{model}
func (s *Server) DeleteModelPricing(w http.ResponseWriter, r *http.Request) {
	model := chi.URLParam(r, "model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid model")
		return
	}
	n, err := s.queries.DeleteModelPricing(r.Context(), model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "not_found", "no pricing configured for this model")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
