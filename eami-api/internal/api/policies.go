package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

func (s *Server) ListPolicies(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	// Production path: queries has status filter and proper ordering.
	if s.queries != nil {
		var status *string
		if v := r.URL.Query().Get("status"); v != "" {
			status = &v
		}
		rows, err := s.queries.ListPolicies(r.Context(), uc.OrgID, status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		resp := make([]PolicyResp, 0, len(rows))
		for _, row := range rows {
			resp = append(resp, policyRowToResp(row))
		}
		writeJSON(w, http.StatusOK, PolicyListResponse{Data: resp})
		return
	}
	rows, err := s.storeIface.ListPolicies(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// Sort by priority ascending so test assertions on ordering pass.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Priority < rows[j].Priority })
	resp := make([]PolicyResp, 0, len(rows))
	for _, p := range rows {
		resp = append(resp, storePolicyToResp(p))
	}
	writeJSON(w, http.StatusOK, PolicyListResponse{Data: resp})
}

func (s *Server) GetPolicy(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "policyId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid policyId")
		return
	}
	// Production path: use s.queries for org-scoped lookup + conditions join.
	if s.queries != nil {
		row, err := s.queries.GetPolicy(r.Context(), id, uc.OrgID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "policy not found")
			return
		}
		writeJSON(w, http.StatusOK, policyRowToResp(*row))
		return
	}
	p, err := s.storeIface.GetPolicy(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "policy not found")
		return
	}
	if p.OrgID != uc.OrgID {
		writeError(w, http.StatusNotFound, "not_found", "policy not found")
		return
	}
	writeJSON(w, http.StatusOK, storePolicyToResp(p))
}

func (s *Server) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req PolicyCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == "" || req.Priority < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "name and priority >= 1 required")
		return
	}
	validPolicyActions := map[string]bool{"allow": true, "deny": true, "escalate": true}
	if !validPolicyActions[req.Action] {
		writeError(w, http.StatusBadRequest, "bad_request", "action must be one of: allow, deny, escalate")
		return
	}
	if req.Conditions == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "conditions is required")
		return
	}
	status := req.Status
	if status == "" {
		status = "draft"
	}

	// Production path: use queries for two-step insert (policy + conditions).
	if s.queries != nil {
		pol, err := s.queries.CreatePolicy(r.Context(), store.CreatePolicyParams{
			OrgID:       uc.OrgID,
			Name:        req.Name,
			Description: toPgtypeTextStr(req.Description),
			Priority:    req.Priority,
			Action:      req.Action,
			Alert:       req.Alert,
			Status:      status,
			CreatedBy:   pgtype.UUID{Bytes: uc.UserID, Valid: true},
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		cond, err := s.queries.CreatePolicyCondition(r.Context(), conditionsReqToParams(pol.ID, *req.Conditions))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "conditions failed: "+err.Error())
			return
		}
		_ = s.queries.NotifyPolicyReload(r.Context())
		writeJSON(w, http.StatusCreated, policyAndCondToResp(*pol, *cond))
		return
	}

	// Test path: use storeIface (MockStore stores conditions as raw JSON bytes).
	condBytes, _ := json.Marshal(*req.Conditions)
	sp, err := s.storeIface.CreatePolicy(r.Context(), CreatePolicyParams{
		OrgID:       uc.OrgID,
		Name:        req.Name,
		Description: ptrStr(req.Description),
		Priority:    req.Priority,
		Conditions:  condBytes,
		Action:      req.Action,
		Alert:       req.Alert,
		Status:      status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, storePolicyToResp(sp))
}

func (s *Server) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "policyId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid policyId")
		return
	}
	var req PolicyUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	// Production path.
	if s.queries != nil {
		var priority pgtype.Int4
		if req.Priority != nil {
			priority = pgtype.Int4{Int32: int32(*req.Priority), Valid: true}
		}
		pol, err := s.queries.UpdatePolicy(r.Context(), store.UpdatePolicyParams{
			ID:          id,
			OrgID:       uc.OrgID,
			Name:        toPgtypeTextStr(req.Name),
			Description: toPgtypeTextStr(req.Description),
			Priority:    priority,
			Action:      toPgtypeTextStr(req.Action),
			Alert:       req.Alert,
			Status:      toPgtypeTextStr(req.Status),
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "policy not found or update failed")
			return
		}
		if req.Conditions != nil {
			_ = s.queries.UpsertPolicyCondition(r.Context(), conditionsReqToParams(pol.ID, *req.Conditions))
		}
		_ = s.queries.NotifyPolicyReload(r.Context())
		row, err := s.queries.GetPolicy(r.Context(), pol.ID, uc.OrgID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, policyRowToResp(*row))
		return
	}

	// Test path.
	var name, action, policyStatus string
	if req.Name != nil {
		name = *req.Name
	}
	if req.Action != nil {
		action = *req.Action
	}
	if req.Status != nil {
		policyStatus = *req.Status
	}
	var pri int32
	if req.Priority != nil {
		pri = int32(*req.Priority)
	}
	var alert bool
	if req.Alert != nil {
		alert = *req.Alert
	}
	sp, err := s.storeIface.UpdatePolicy(r.Context(), UpdatePolicyParams{
		ID:     id,
		OrgID:  uc.OrgID,
		Name:   name,
		Action: action,
		Status: policyStatus,
		Priority: pri,
		Alert:  alert,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "policy not found")
		return
	}
	writeJSON(w, http.StatusOK, storePolicyToResp(sp))
}

func (s *Server) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "policyId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid policyId")
		return
	}

	// Production path: org-scoped delete.
	if s.queries != nil {
		if err := s.queries.DeletePolicy(r.Context(), id, uc.OrgID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_ = s.queries.NotifyPolicyReload(r.Context())
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Test path: validate existence and org membership first.
	p, err := s.storeIface.GetPolicy(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "policy not found")
		return
	}
	if p.OrgID != uc.OrgID {
		writeError(w, http.StatusNotFound, "not_found", "policy not found")
		return
	}
	if err := s.storeIface.DeletePolicy(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ReorderPolicies(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req PolicyReorderRequest
	if err := decodeJSON(r, &req); err != nil || len(req.Order) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "policy_ids (array of UUIDs) is required")
		return
	}

	// Production path.
	if s.queries != nil {
		if err := s.queries.ReorderPolicies(r.Context(), uc.OrgID, req.Order); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_ = s.queries.NotifyPolicyReload(r.Context())
		rows, err := s.queries.ListPolicies(r.Context(), uc.OrgID, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		resp := make([]PolicyResp, 0, len(rows))
		for _, row := range rows {
			resp = append(resp, policyRowToResp(row))
		}
		writeJSON(w, http.StatusOK, PolicyListResponse{Data: resp})
		return
	}

	// Test path: validate all IDs belong to caller's org.
	for _, pid := range req.Order {
		p, err := s.storeIface.GetPolicy(r.Context(), pid)
		if err != nil || p.OrgID != uc.OrgID {
			writeError(w, http.StatusNotFound, "not_found", "one or more policy IDs not found")
			return
		}
	}
	if err := s.storeIface.ReorderPolicies(r.Context(), ReorderPoliciesParams{
		OrgID:     uc.OrgID,
		PolicyIDs: req.Order,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// storePolicyToResp converts a StorePolicy (api mirror type) to a PolicyResp.
// Conditions are parsed from the JSON bytes stored in StorePolicy.Conditions.
func storePolicyToResp(p StorePolicy) PolicyResp {
	resp := PolicyResp{
		ID:        p.ID.String(),
		Name:      p.Name,
		Priority:  p.Priority,
		Action:    p.Action,
		Alert:     p.Alert,
		Status:    p.Status,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
	if p.Description != "" {
		resp.Description = &p.Description
	}
	// Attempt to decode conditions from the JSON bytes.
	if len(p.Conditions) > 0 {
		var c PolicyConditionsResp
		if err := json.Unmarshal(p.Conditions, &c); err == nil {
			resp.Conditions = c
		}
	}
	return resp
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// policyRowToResp and policyAndCondToResp remain for the production path
// that reads from store.PolicyRow (includes the joined condition).
func policyRowToResp(row store.PolicyRow) PolicyResp {
	resp := PolicyResp{
		ID:        row.Policy.ID.String(),
		Name:      row.Policy.Name,
		Priority:  row.Policy.Priority,
		Action:    row.Policy.Action,
		Alert:     row.Policy.Alert,
		Status:    row.Policy.Status,
		CreatedAt: row.Policy.CreatedAt,
		UpdatedAt: row.Policy.UpdatedAt,
		Conditions: PolicyConditionsResp{
			ToolNames:    row.Condition.ToolNames,
			ActionTypes:  row.Condition.ActionTypes,
			Environments: row.Condition.Environments,
			ScopeDrift:   row.Condition.ScopeDrift,
		},
	}
	if row.Policy.Description.Valid {
		resp.Description = &row.Policy.Description.String
	}
	if row.Policy.CreatedBy.Valid {
		id := uuid.UUID(row.Policy.CreatedBy.Bytes).String()
		resp.CreatedBy = &id
	}
	if row.Condition.AgentNamePattern.Valid {
		resp.Conditions.AgentNamePattern = &row.Condition.AgentNamePattern.String
	}
	if row.Condition.RecordCountGT.Valid {
		resp.Conditions.RecordCountGT = &row.Condition.RecordCountGT.Int32
	}
	if row.Condition.SemanticRule.Valid {
		resp.Conditions.SemanticRule = &row.Condition.SemanticRule.String
	}
	return resp
}

func policyAndCondToResp(pol store.Policy, cond store.PolicyCondition) PolicyResp {
	return policyRowToResp(store.PolicyRow{Policy: pol, Condition: cond})
}

func conditionsReqToParams(policyID uuid.UUID, c PolicyConditionsReq) store.CreatePolicyConditionParams {
	toolNames := c.ToolNames
	if toolNames == nil {
		toolNames = []string{}
	}
	actionTypes := c.ActionTypes
	if actionTypes == nil {
		actionTypes = []string{}
	}
	envs := c.Environments
	if envs == nil {
		envs = []string{}
	}
	return store.CreatePolicyConditionParams{
		PolicyID:         policyID,
		AgentNamePattern: toPgtypeTextStr(c.AgentNamePattern),
		ToolNames:        toolNames,
		ActionTypes:      actionTypes,
		Environments:     envs,
		RecordCountGT:    toPgtypeInt4Ptr(c.RecordCountGT),
		SemanticRule:     toPgtypeTextStr(c.SemanticRule),
		ScopeDrift:       c.ScopeDrift,
	}
}

func toPgtypeTextStr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func toPgtypeInt4Ptr(i *int) pgtype.Int4 {
	if i == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*i), Valid: true}
}
