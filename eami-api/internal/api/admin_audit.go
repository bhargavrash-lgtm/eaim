package api

// Admin audit trail read API (B-269 Slice 0b; B0b-2): org admins only, gated
// in router.go's admin-only group. Placed under /v1/audit (the one-spine
// rule: admin changes belong to Audit), but NOT in /v1/audit's
// admin/operator/viewer read group.
//
// The org always comes from the JWT; there is no org parameter. Every filter
// is allowlisted or format-checked, and a bad one is a 400 with a fixed
// message that never echoes the value (API_CONVENTION.md principle 4, §8).

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/eami/api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// AdminAuditEventResp is one admin audit event.
type AdminAuditEventResp struct {
	ID          string          `json:"id"`
	Seq         int64           `json:"seq"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ActorType   string          `json:"actor_type"`
	ActorUserID *string         `json:"actor_user_id"`
	ActorEmail  *string         `json:"actor_email"`
	ActorRole   string          `json:"actor_role"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	Summary     json.RawMessage `json:"summary"`
	Source      string          `json:"source"`
	RequestID   *string         `json:"request_id"`
	PrevHash    string          `json:"prev_hash"`
	Hash        string          `json:"hash"`
}

// AdminAuditListResponse is GET /v1/audit/admin-events.
type AdminAuditListResponse struct {
	Data []AdminAuditEventResp `json:"data"`
	Meta PaginationMeta        `json:"meta"`
}

// AdminAuditVerifyResponse is GET /v1/audit/admin-events/verify. Guarantee
// states the limit in the response itself (B0b-7).
type AdminAuditVerifyResponse struct {
	Valid       bool   `json:"valid"`
	Checked     int64  `json:"checked"`
	HeadSeq     int64  `json:"head_seq"`
	HeadHash    string `json:"head_hash"`
	FirstBadSeq *int64 `json:"first_bad_seq"`
	Reason      string `json:"reason,omitempty"`
	Guarantee   string `json:"guarantee"`
}

const adminAuditGuarantee = "Detects edits, interior deletions, reordering and cross-org splicing. " +
	"Does not detect removal of the newest events or a full recompute by a database administrator."

var adminAuditListParams = map[string]bool{
	"action": true, "target_type": true, "target_id": true, "actor_user_id": true,
	"from": true, "to": true, "page": true, "per_page": true, "sort": true, "order": true,
}

func adminAuditBadRequest(w http.ResponseWriter, field, msg string) {
	writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "bad_request", Message: msg, Field: field})
}

// ListAdminAuditEvents handles GET /v1/audit/admin-events.
func (s *Server) ListAdminAuditEvents(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()
	for k, vs := range q {
		if !adminAuditListParams[k] {
			adminAuditBadRequest(w, "", "unknown query parameter")
			return
		}
		if len(vs) > 1 {
			adminAuditBadRequest(w, k, "duplicate query parameter")
			return
		}
	}

	p := store.ListAdminAuditEventsParams{OrgID: uc.OrgID}
	if v := q.Get("action"); v != "" {
		if !store.IsAdminAuditAction(v) {
			adminAuditBadRequest(w, "action", "unknown action")
			return
		}
		p.Action = &v
	}
	if v := q.Get("target_type"); v != "" {
		if !store.IsAdminAuditTargetType(v) {
			adminAuditBadRequest(w, "target_type", "unknown target_type")
			return
		}
		p.TargetType = &v
	}
	if v := q.Get("target_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			adminAuditBadRequest(w, "target_id", "target_id must be a UUID")
			return
		}
		tid := id.String()
		p.TargetID = &tid
	}
	if v := q.Get("actor_user_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			adminAuditBadRequest(w, "actor_user_id", "actor_user_id must be a UUID")
			return
		}
		p.ActorUserID = &id
	}
	for _, key := range []string{"from", "to"} {
		v := q.Get(key)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			adminAuditBadRequest(w, key, key+" must be RFC3339")
			return
		}
		if key == "from" {
			p.From = &t
		} else {
			p.To = &t
		}
	}
	if p.From != nil && p.To != nil && p.To.Before(*p.From) {
		adminAuditBadRequest(w, "to", "to must not be before from")
		return
	}
	if v := q.Get("sort"); v != "" && v != "seq" {
		adminAuditBadRequest(w, "sort", "sort must be seq")
		return
	}
	switch q.Get("order") {
	case "", "desc":
	case "asc":
		p.Ascending = true
	default:
		adminAuditBadRequest(w, "order", "order must be asc or desc")
		return
	}

	for _, key := range []string{"page", "per_page"} {
		if v := q.Get(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || (key == "per_page" && n > 100) {
				adminAuditBadRequest(w, key, key+" must be a positive integer (per_page at most 100)")
				return
			}
		}
	}
	page, perPage := pagination(q.Get("page"), q.Get("per_page"), 50, 100)
	p.Limit = int32(perPage)
	p.Offset = int32((page - 1) * perPage)

	rows, err := s.queries.ListAdminAuditEvents(r.Context(), p)
	if err != nil {
		slog.Error("admin audit: list failed", "org_id", uc.OrgID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list admin audit events")
		return
	}
	total, err := s.queries.CountAdminAuditEvents(r.Context(), p)
	if err != nil {
		slog.Error("admin audit: count failed", "org_id", uc.OrgID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list admin audit events")
		return
	}

	data := make([]AdminAuditEventResp, 0, len(rows))
	for _, e := range rows {
		resp := AdminAuditEventResp{
			ID: e.ID.String(), Seq: e.Seq, OccurredAt: e.OccurredAt, ActorType: e.ActorType,
			ActorEmail: e.ActorEmail, ActorRole: e.ActorRole, Action: e.Action,
			TargetType: e.TargetType, TargetID: e.TargetID, Summary: json.RawMessage(e.Summary),
			Source: e.Source, RequestID: e.RequestID, PrevHash: e.PrevHash, Hash: e.Hash,
		}
		if e.ActorUserID != nil {
			id := e.ActorUserID.String()
			resp.ActorUserID = &id
		}
		data = append(data, resp)
	}
	writeJSON(w, http.StatusOK, AdminAuditListResponse{
		Data: data,
		Meta: PaginationMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// VerifyAdminAuditChain handles GET /v1/audit/admin-events/verify: re-walks
// the caller's own org chain only.
func (s *Server) VerifyAdminAuditChain(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	if len(r.URL.Query()) > 0 {
		adminAuditBadRequest(w, "", "unknown query parameter")
		return
	}
	if ok, retry := s.adminAuditVerifyLimiter.Allow(uc.OrgID.String()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many verify requests; try again shortly")
		return
	}
	res, err := s.queries.VerifyAdminAuditChain(r.Context(), uc.OrgID)
	if err != nil {
		slog.Error("admin audit: verify failed", "org_id", uc.OrgID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not verify admin audit events")
		return
	}
	writeJSON(w, http.StatusOK, AdminAuditVerifyResponse{
		Valid: res.Valid, Checked: res.Checked, HeadSeq: res.HeadSeq, HeadHash: res.HeadHash,
		FirstBadSeq: res.FirstBadSeq, Reason: res.Reason, Guarantee: adminAuditGuarantee,
	})
}

// adminAuditActor is the audit actor for an authenticated request.
func adminAuditActor(uc userClaims) store.AdminAuditActor {
	return store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: uc.UserID, Role: uc.Role}
}

// adminAuditRequestID is chi's request ID for correlation with local logs.
// store.AppendAdminAuditEvent keeps it only if it passes the strict format
// check (a client can set X-Request-Id, B0b-5); it is never echoed in errors.
func adminAuditRequestID(r *http.Request) string {
	return middleware.GetReqID(r.Context())
}

// writeAdminAuditFailure maps the result of a store.RunAudited change to an
// HTTP error. An audit failure is a fixed 500 code: the change was rolled
// back, and the reason stays in the local log (fail closed, B0b-4). It
// returns false when err is not an audit error, so the handler maps the
// change's own errors itself.
func writeAdminAuditFailure(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, store.ErrAdminAuditWrite) && !errors.Is(err, store.ErrAdminAuditInvalid) {
		return false
	}
	route := ""
	if rc := chi.RouteContext(r.Context()); rc != nil {
		route = rc.RoutePattern()
	}
	slog.Error("admin audit: event not recorded; change rolled back", "route", route, "err", err)
	writeError(w, http.StatusInternalServerError, "audit_write_failed",
		"the change was not applied because its audit event could not be recorded")
	return true
}
