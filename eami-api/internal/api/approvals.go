package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// ── Request / response types ──────────────────────────────────────────────────

type BlastRadiusResp struct {
	EstimatedRecords *int32   `json:"estimated_records,omitempty"`
	Reversible       *bool    `json:"reversible,omitempty"`
	Environment      *string  `json:"environment,omitempty"`
	DataTypes        []string `json:"data_types,omitempty"`
}

type ApprovalResp struct {
	ID            string          `json:"id"`
	AgentID       string          `json:"agent_id"`
	AgentName     string          `json:"agent_name"`
	ToolName      string          `json:"tool_name"`
	Action        string          `json:"action"`
	Parameters    interface{}     `json:"parameters,omitempty"`
	Justification string          `json:"justification"`
	RiskLevel     string          `json:"risk_level"`
	BlastRadius   BlastRadiusResp `json:"blast_radius"`
	Status        string          `json:"status"`
	PolicyID      *string         `json:"policy_id,omitempty"`
	ApprovedBy    *string         `json:"approved_by,omitempty"`
	DecidedAt     *time.Time      `json:"decided_at,omitempty"`
	ExpiresAt     time.Time       `json:"expires_at"`
	CreatedAt     time.Time       `json:"created_at"`
}

type ApprovalListResp struct {
	Data []ApprovalResp `json:"data"`
	Meta PaginationMeta `json:"meta"`
}

type CreateApprovalRequest struct {
	AgentID            string      `json:"agent_id"`
	AgentName          string      `json:"agent_name"`
	ToolName           string      `json:"tool_name"`
	Action             string      `json:"action"`
	Parameters         interface{} `json:"parameters"`
	Justification      string      `json:"justification"`
	RiskLevel          string      `json:"risk_level"`
	EstimatedRecords   *int        `json:"estimated_records"`
	Reversible         *bool       `json:"reversible"`
	Environment        *string     `json:"environment"`
	DataTypes          []string    `json:"data_types"`
	PolicyRuleID       *string     `json:"policy_rule_id"`
	ExpiresInSeconds   int         `json:"expires_in_seconds"`
	GatewaySessionID   string      `json:"gateway_session_id"`
	GatewayNodeAddress string      `json:"gateway_node_address"`
}

type DecideApprovalRequest struct {
	Decision   string `json:"decision"`    // "approved" | "denied"
	DecidedBy  string `json:"decided_by"`  // user name / email for the record
	Reason     string `json:"reason"`      // optional
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// ListApprovals handles GET /v1/approvals
func (s *Server) ListApprovals(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()

	page, perPage := pagination(q.Get("page"), q.Get("per_page"), 25, 500)

	var agentID *uuid.UUID
	if v := q.Get("agent_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid agent_id")
			return
		}
		agentID = &id
	}

	mp := MockListApprovalsParams{
		OrgID:   uc.OrgID,
		Status:  strPtrFromQuery(q.Get("status")),
		AgentID: agentID,
		Limit:   int32(perPage),
		Offset:  int32((page - 1) * perPage),
	}
	var rows []StoreApproval
	var total int64
	if s.queries != nil {
		p := store.ListApprovalsParams{
			OrgID:   uc.OrgID,
			Status:  mp.Status,
			AgentID: mp.AgentID,
			From:    timePtrFromQuery(q.Get("from")),
			To:      timePtrFromQuery(q.Get("to")),
			Limit:   mp.Limit,
			Offset:  mp.Offset,
		}
		pRows, err := s.queries.ListApprovals(r.Context(), p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		count, err := s.queries.CountApprovals(r.Context(), p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		data := make([]ApprovalResp, 0, len(pRows))
		for _, row := range pRows {
			data = append(data, approvalToResp(row))
		}
		writeJSON(w, http.StatusOK, ApprovalListResp{
			Data: data,
			Meta: PaginationMeta{Total: count, Page: page, PerPage: perPage},
		})
		return
	}
	var err error
	rows, err = s.storeIface.ListApprovals(r.Context(), mp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	total, err = s.storeIface.CountApprovals(r.Context(), mp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	data := make([]ApprovalResp, 0, len(rows))
	for _, row := range rows {
		data = append(data, storeApprovalToResp(row))
	}
	writeJSON(w, http.StatusOK, ApprovalListResp{
		Data: data,
		Meta: PaginationMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// GetApproval handles GET /v1/approvals/{approvalId}
func (s *Server) GetApproval(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "approvalId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid approvalId")
		return
	}
	if s.queries != nil {
		a, err := s.queries.GetApproval(r.Context(), id, uc.OrgID)
		if err != nil {
			if err == pgx.ErrNoRows {
				writeError(w, http.StatusNotFound, "not_found", "approval not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, approvalToResp(*a))
		return
	}
	sa, err := s.storeIface.GetApproval(r.Context(), id, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "approval not found")
		return
	}
	writeJSON(w, http.StatusOK, storeApprovalToResp(sa))
}

// CreateApproval handles POST /v1/approvals (called by the gateway on ESCALATE).
func (s *Server) CreateApproval(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req CreateApprovalRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.AgentID == "" || req.ToolName == "" || req.Action == "" || req.GatewaySessionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "agent_id, tool_name, action, gateway_session_id required")
		return
	}

	agentID, err := uuid.Parse(req.AgentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agent_id")
		return
	}

	expiresIn := req.ExpiresInSeconds
	if expiresIn <= 0 {
		expiresIn = 3600 // default 1 hour
	}
	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)

	// Marshal parameters to JSONB bytes.
	paramsBytes, _ := json.Marshal(req.Parameters)

	dataTypes := req.DataTypes
	if dataTypes == nil {
		dataTypes = []string{}
	}

	p := store.CreateApprovalParams{
		OrgID:              uc.OrgID,
		AgentID:            agentID,
		AgentName:          req.AgentName,
		ToolName:           req.ToolName,
		Action:             req.Action,
		Parameters:         paramsBytes,
		Justification:      req.Justification,
		RiskLevel:          req.RiskLevel,
		DataTypes:          dataTypes,
		ExpiresAt:          expiresAt,
		GatewaySessionID:   req.GatewaySessionID,
		GatewayNodeAddress: req.GatewayNodeAddress,
	}
	if req.EstimatedRecords != nil {
		p.EstimatedRecords = pgtype.Int4{Int32: int32(*req.EstimatedRecords), Valid: true}
	}
	if req.Reversible != nil {
		p.Reversible = pgtype.Bool{Bool: *req.Reversible, Valid: true}
	}
	if req.Environment != nil {
		p.Environment = pgtype.Text{String: *req.Environment, Valid: true}
	}
	if req.PolicyRuleID != nil {
		id, err := uuid.Parse(*req.PolicyRuleID)
		if err == nil {
			p.PolicyID = pgtype.UUID{Bytes: id, Valid: true}
		}
	}

	if s.queries != nil {
		a, err := s.queries.CreateApproval(r.Context(), p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, approvalToResp(*a))
		return
	}
	sa, err := s.storeIface.CreateApproval(r.Context(), MockCreateApprovalParams{
		OrgID:         uc.OrgID,
		AgentID:       agentID,
		AgentName:     req.AgentName,
		ToolName:      req.ToolName,
		Action:        req.Action,
		Justification: req.Justification,
		RiskLevel:     req.RiskLevel,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, storeApprovalToResp(sa))
}

// DecideApproval handles POST /v1/approvals/{approvalId}/decide
func (s *Server) DecideApproval(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "approvalId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid approvalId")
		return
	}

	var req DecideApprovalRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Decision != "approved" && req.Decision != "denied" {
		writeError(w, http.StatusBadRequest, "bad_request", `decision must be "approved" or "denied"`)
		return
	}

	decidedBy := req.DecidedBy
	if decidedBy == "" {
		decidedBy = uc.Email // fall back to the JWT identity
	}

	p := store.DecideApprovalParams{
		ID:             id,
		OrgID:          uc.OrgID,
		Status:         req.Decision,
		ApprovedBy:     pgtype.UUID{Bytes: uc.UserID, Valid: true},
		DecisionReason: pgtype.Text{String: req.Reason, Valid: req.Reason != ""},
	}

	if s.queries != nil {
		a, err := s.queries.DecideApproval(r.Context(), p)
		if err != nil {
			if err == pgx.ErrNoRows {
				writeError(w, http.StatusConflict, "conflict", "approval not found or already decided")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		_ = s.queries.NotifyApprovalDecision(r.Context(), a.ID)
		writeJSON(w, http.StatusOK, approvalToResp(*a))
		return
	}
	sa, err := s.storeIface.DecideApproval(r.Context(), MockDecideApprovalParams{
		ID:        id,
		OrgID:     uc.OrgID,
		Decision:  req.Decision,
		DecidedBy: decidedBy,
		Reason:    req.Reason,
	})
	if err != nil {
		if err == ErrAlreadyDecided || err == ErrNotFound {
			writeError(w, http.StatusConflict, "conflict", "approval not found or already decided")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, storeApprovalToResp(sa))
}

// ── Converters ────────────────────────────────────────────────────────────────

func approvalToResp(a store.ApprovalRequest) ApprovalResp {
	resp := ApprovalResp{
		ID:            a.ID.String(),
		AgentID:       a.AgentID.String(),
		AgentName:     a.AgentName,
		ToolName:      a.ToolName,
		Action:        a.Action,
		Justification: a.Justification,
		RiskLevel:     a.RiskLevel,
		Status:        a.Status,
		ExpiresAt:     a.ExpiresAt,
		CreatedAt:     a.CreatedAt,
		BlastRadius: BlastRadiusResp{
			DataTypes: a.DataTypes,
		},
	}
	if a.EstimatedRecords.Valid {
		v := a.EstimatedRecords.Int32
		resp.BlastRadius.EstimatedRecords = &v
	}
	if a.Reversible.Valid {
		v := a.Reversible.Bool
		resp.BlastRadius.Reversible = &v
	}
	if a.Environment.Valid {
		resp.BlastRadius.Environment = &a.Environment.String
	}
	if a.PolicyID.Valid {
		s := uuid.UUID(a.PolicyID.Bytes).String()
		resp.PolicyID = &s
	}
	if a.ApprovedBy.Valid {
		s := uuid.UUID(a.ApprovedBy.Bytes).String()
		resp.ApprovedBy = &s
	}
	if a.DecidedAt.Valid {
		resp.DecidedAt = &a.DecidedAt.Time
	}
	if len(a.Parameters) > 0 {
		var params interface{}
		if err := json.Unmarshal(a.Parameters, &params); err == nil {
			resp.Parameters = params
		}
	}
	return resp
}

// ── Query helpers ─────────────────────────────────────────────────────────────

func strPtrFromQuery(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func timePtrFromQuery(v string) *time.Time {
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &t
}

func pagination(pageStr, perPageStr string, defaultPerPage, maxPerPage int) (page, perPage int) {
	page = 1
	perPage = defaultPerPage
	if n, err := strconv.Atoi(pageStr); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(perPageStr); err == nil && n > 0 {
		if n > maxPerPage {
			n = maxPerPage
		}
		perPage = n
	}
	return
}

// storeApprovalToResp converts a StoreApproval (api mirror type) to ApprovalResp.
func storeApprovalToResp(a StoreApproval) ApprovalResp {
	resp := ApprovalResp{
		ID:            a.ID.String(),
		AgentID:       a.AgentID.String(),
		AgentName:     a.AgentName,
		ToolName:      a.ToolName,
		Action:        a.Action,
		Justification: a.Justification,
		RiskLevel:     a.RiskLevel,
		Status:        a.Status,
		ExpiresAt:     a.ExpiresAt,
		CreatedAt:     a.CreatedAt,
		BlastRadius:   BlastRadiusResp{},
	}
	if a.DecidedBy != nil {
		resp.ApprovedBy = a.DecidedBy
	}
	if a.DecidedAt != nil {
		resp.DecidedAt = a.DecidedAt
	}
	return resp
}
