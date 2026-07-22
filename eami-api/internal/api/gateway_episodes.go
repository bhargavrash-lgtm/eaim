// gateway_episodes.go -- proxy layer for full episode content (B-002 Brief 2).
//
// Per ADR-019, full episode content (tool calls, arguments, results) stays
// on-prem in eami-gateway's Postgres -- eami-api never stores or queries it
// directly. These handlers proxy UI requests to eami-gateway's episode read
// endpoint (eami-gateway/internal/episode/http.go, Brief 1, frozen -- called
// here, never modified) via its service-key auth path.
//
// eami-gateway's service-key path enforces NO authorization on the org_id it
// is given -- that check is this file's entire reason for existing. The org
// sent to the gateway is always the authenticated caller's own org
// (claimsFromContext(r).OrgID), never a value read from the request for that
// purpose. An optional org_id query param is accepted purely as a tamper
// check: if present and it doesn't match the session's org, the request is
// rejected with 403 before the gateway is ever called.
//
// This file is purely additive -- it does not modify memory.go's existing
// (still ADR-010-violating) routes or behavior. Brief 3 retires those.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// -- Wire types ---------------------------------------------------------------

// GatewayEpisode mirrors eami-gateway/internal/episode.Episode's JSON shape.
// Duplicated, not imported: eami-api and eami-gateway are separate Go
// modules with no shared internal/ package, same reason Brief 1 duplicated
// eami-api's own Episode shape.
type GatewayEpisode struct {
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

type GatewayListResult struct {
	Episodes []GatewayEpisode
	Total    int64
}

// GatewayError represents a non-2xx HTTP response FROM eami-gateway. Carries
// only the status code -- gateway's plain-text response body is logged
// server-side, never forwarded to the caller (that's the whole point: don't
// leak the upstream's raw error format to the frontend).
type GatewayError struct {
	StatusCode int
}

func (e *GatewayError) Error() string {
	return fmt.Sprintf("gateway returned status %d", e.StatusCode)
}

// -- Client interface (test seam) ---------------------------------------------

// GatewayEpisodeClient is the interface HTTP handlers call through.
// Exported (along with GatewayEpisode/GatewayListResult/GatewayError) only
// so gateway_episodes_test.go's fake -- in package api_test, alongside
// agents_test.go's shared test helpers it reuses -- can implement it and
// construct its error values; not intended as a public API for external
// callers. Production uses httpGatewayEpisodeClient; tests inject a
// hand-rolled fake -- same seam pattern as this package's own Store
// interface (store_mock.go) and eami-gateway's episodeStore/AgentResolver
// from Brief 1.
type GatewayEpisodeClient interface {
	ListEpisodes(ctx context.Context, orgID uuid.UUID, outcome string, limit, offset int) (GatewayListResult, error)
	SearchEpisodes(ctx context.Context, orgID uuid.UUID, query string) ([]GatewayEpisode, error)
	GetEpisode(ctx context.Context, orgID, episodeID uuid.UUID) (*GatewayEpisode, error)
}

// httpGatewayEpisodeClient is the production GatewayEpisodeClient, calling
// eami-gateway's real HTTP endpoint.
type httpGatewayEpisodeClient struct {
	baseURL    string
	serviceKey string
	httpClient *http.Client
}

func newHTTPGatewayEpisodeClient(baseURL, serviceKey string) *httpGatewayEpisodeClient {
	return &httpGatewayEpisodeClient{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *httpGatewayEpisodeClient) ListEpisodes(ctx context.Context, orgID uuid.UUID, outcome string, limit, offset int) (GatewayListResult, error) {
	q := url.Values{}
	q.Set("org_id", orgID.String())
	if outcome != "" {
		q.Set("outcome", outcome)
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))

	var resp struct {
		Data []GatewayEpisode `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := c.get(ctx, "/v1/gateway/episodes", q, &resp); err != nil {
		return GatewayListResult{}, err
	}
	return GatewayListResult{Episodes: resp.Data, Total: resp.Meta.Total}, nil
}

func (c *httpGatewayEpisodeClient) SearchEpisodes(ctx context.Context, orgID uuid.UUID, query string) ([]GatewayEpisode, error) {
	q := url.Values{}
	q.Set("org_id", orgID.String())
	q.Set("q", query)

	var resp struct {
		Data []GatewayEpisode `json:"data"`
	}
	if err := c.get(ctx, "/v1/gateway/episodes/search", q, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *httpGatewayEpisodeClient) GetEpisode(ctx context.Context, orgID, episodeID uuid.UUID) (*GatewayEpisode, error) {
	q := url.Values{}
	q.Set("org_id", orgID.String())

	var ep GatewayEpisode
	if err := c.get(ctx, "/v1/gateway/episodes/"+episodeID.String(), q, &ep); err != nil {
		return nil, err
	}
	return &ep, nil
}

// get issues a GET request to eami-gateway and decodes a 200 JSON body into
// out. Non-2xx responses become *GatewayError{StatusCode}; anything that
// prevents the round-trip (bad URL, network failure) is returned as a plain
// wrapped error, distinguishable from *GatewayError via errors.As.
func (c *httpGatewayEpisodeClient) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.baseURL + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("gateway: build request: %w", err)
	}
	req.Header.Set("X-Service-Key", c.serviceKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gateway: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &GatewayError{StatusCode: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("gateway: decode response: %w", err)
	}
	return nil
}

// -- HTTP handlers --------------------------------------------------------------

// checkOrgID enforces the tamper check described in the package doc comment.
// Returns false (and has already written the 403 response) if the caller
// supplied an org_id that doesn't match their own session -- the caller must
// stop and not proceed to call the gateway. Absent or matching org_id: true.
func checkOrgID(w http.ResponseWriter, r *http.Request, uc userClaims) bool {
	raw := r.URL.Query().Get("org_id")
	if raw == "" {
		return true
	}
	requested, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "org_id must be a valid UUID")
		return false
	}
	if requested != uc.OrgID {
		writeError(w, http.StatusForbidden, "forbidden", "org_id does not match the authenticated session")
		return false
	}
	return true
}

// writeGatewayError translates a GatewayEpisodeClient error into eami-api's
// JSON error envelope. Only a 404 from the gateway is meaningful to the
// caller (a real "not found" -- including a cross-org GetEpisode lookup,
// which the gateway itself already renders indistinguishable from a
// genuinely missing id). Every other gateway error status, and any
// transport/network failure reaching the gateway at all, becomes a generic
// 502 -- eami-api's own credential/config problem, not the caller's fault,
// and not detail the caller needs (or should get) to see. The real error is
// logged server-side.
func writeGatewayError(w http.ResponseWriter, err error) {
	var gwErr *GatewayError
	if errors.As(err, &gwErr) && gwErr.StatusCode == http.StatusNotFound {
		writeError(w, http.StatusNotFound, "not_found", "episode not found")
		return
	}
	slog.Error("gateway episode proxy: upstream call failed", "err", err)
	writeError(w, http.StatusBadGateway, "upstream_error", "gateway request failed")
}

// gatewayNotConfigured returns true (and has written a 502 response) if the
// gateway proxy has no usable configuration -- config.Gateway.URL/
// EpisodeReadServiceKey are optional at eami-api startup (see
// internal/config), so this is the request-time equivalent of the required-
// field check eami-gateway does at its own startup.
func (s *Server) gatewayNotConfigured(w http.ResponseWriter) bool {
	if s.cfg == nil || s.cfg.Gateway.URL == "" || s.cfg.Gateway.EpisodeReadServiceKey == "" {
		writeError(w, http.StatusBadGateway, "upstream_error", "gateway proxy is not configured")
		return true
	}
	return false
}

// ListGatewayEpisodes handles GET /v1/gateway/episodes.
// Supports ?outcome=, ?page=, ?per_page= (translated to the gateway's
// limit/offset convention) and the optional org_id tamper-check param.
func (s *Server) ListGatewayEpisodes(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	if !checkOrgID(w, r, uc) {
		return
	}
	if s.gatewayNotConfigured(w) {
		return
	}

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
	outcome := q.Get("outcome")

	result, err := s.gatewayClient.ListEpisodes(r.Context(), uc.OrgID, outcome, perPage, (page-1)*perPage)
	if err != nil {
		writeGatewayError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": result.Episodes,
		"meta": PaginationMeta{Total: result.Total, Page: page, PerPage: perPage},
	})
}

// SearchGatewayEpisodes handles GET /v1/gateway/episodes/search?q=<text>.
func (s *Server) SearchGatewayEpisodes(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	if !checkOrgID(w, r, uc) {
		return
	}
	if s.gatewayNotConfigured(w) {
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "q is required")
		return
	}

	episodes, err := s.gatewayClient.SearchEpisodes(r.Context(), uc.OrgID, query)
	if err != nil {
		writeGatewayError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": episodes})
}

// GetGatewayEpisode handles GET /v1/gateway/episodes/{episodeId}.
func (s *Server) GetGatewayEpisode(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	if !checkOrgID(w, r, uc) {
		return
	}
	if s.gatewayNotConfigured(w) {
		return
	}

	id, err := parseUUIDParam(r, "episodeId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "episodeId must be a valid UUID")
		return
	}

	ep, err := s.gatewayClient.GetEpisode(r.Context(), uc.OrgID, id)
	if err != nil {
		writeGatewayError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ep)
}
