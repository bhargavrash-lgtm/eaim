package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// parseDateParam parses a query param as RFC3339 or date-only (YYYY-MM-DD).
// Returns zero time and an error string on failure.
func parseDateParam(r *http.Request, key string) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return time.Time{}, fmt.Errorf("%s is required", key)
	}
	// Try RFC3339 first, then date-only.
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD", key)
}

// FinOpsSummary handles GET /v1/finops/summary?from=DATE&to=DATE
func (s *Server) FinOpsSummary(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	from, err := parseDateParam(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	to, err := parseDateParam(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !to.After(from) {
		writeError(w, http.StatusBadRequest, "bad_request", "to must be after from")
		return
	}

	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "finops store is not configured")
		return
	}

	orgID := pgtype.UUID{Bytes: uc.OrgID, Valid: true}
	fromTS := pgtype.Timestamptz{Time: from, Valid: true}
	toTS := pgtype.Timestamptz{Time: to, Valid: true}
	db := s.queries.DB()
	ctx := r.Context()

	// ── Totals ────────────────────────────────────────────────────────────────
	const totalQ = `
SELECT
  COALESCE(SUM(CASE WHEN cost_usd IS NOT NULL THEN cost_usd
                    ELSE (tokens_in  * mp.cost_per_1k_in  / 1000.0)
                       + (tokens_out * mp.cost_per_1k_out / 1000.0)
               END), 0)         AS total_cost_usd,
  COALESCE(SUM(tokens_in),  0)  AS total_tokens_in,
  COALESCE(SUM(tokens_out), 0)  AS total_tokens_out
FROM token_usage tu
LEFT JOIN model_pricing mp ON mp.model = tu.model
WHERE tu.org_id = $1
  AND tu.recorded_at >= $2
  AND tu.recorded_at <  $3`

	var totalCost float64
	var totalIn, totalOut int64
	row := db.QueryRow(ctx, totalQ, orgID, fromTS, toTS)
	if err := row.Scan(&totalCost, &totalIn, &totalOut); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// ── By agent ──────────────────────────────────────────────────────────────
	const agentQ = `
SELECT tu.agent_id, tu.agent_name,
  COALESCE(SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN tu.cost_usd
                    ELSE (tu.tokens_in  * mp.cost_per_1k_in  / 1000.0)
                       + (tu.tokens_out * mp.cost_per_1k_out / 1000.0)
               END), 0)         AS cost_usd,
  COALESCE(SUM(tu.tokens_in),  0) AS tokens_in,
  COALESCE(SUM(tu.tokens_out), 0) AS tokens_out,
  COUNT(*)                        AS request_count
FROM token_usage tu
LEFT JOIN model_pricing mp ON mp.model = tu.model
WHERE tu.org_id = $1
  AND tu.recorded_at >= $2
  AND tu.recorded_at <  $3
GROUP BY tu.agent_id, tu.agent_name
ORDER BY cost_usd DESC`

	agentRows, err := db.Query(ctx, agentQ, orgID, fromTS, toTS)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer agentRows.Close()

	byAgent := make([]AgentSpend, 0)
	for agentRows.Next() {
		var agID pgtype.UUID
		var agName string
		var cost float64
		var tIn, tOut, cnt int64
		if err := agentRows.Scan(&agID, &agName, &cost, &tIn, &tOut, &cnt); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		var agIDStr string
		if agID.Valid {
			agIDStr = uuid.UUID(agID.Bytes).String()
		}
		byAgent = append(byAgent, AgentSpend{
			AgentID:      agIDStr,
			AgentName:    agName,
			CostUSD:      cost,
			TokensIn:     tIn,
			TokensOut:    tOut,
			RequestCount: cnt,
		})
	}
	if err := agentRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// ── By team ───────────────────────────────────────────────────────────────
	// Team is derived from gateway_agents.owner via a join, not read from
	// token_usage.team directly (that column exists in the schema but is
	// never populated by the write path — see B-097).
	//
	// GROUP BY ga.owner, NOT the "team" alias (B-097): token_usage has its
	// own real "team" column, and Postgres's GROUP BY name resolution
	// prefers a matching INPUT column over an OUTPUT alias of the same
	// name — "GROUP BY team" silently grouped by the wrong (always-empty)
	// tu.team column instead of the alias, leaving ga.owner ungrouped and
	// producing a real 500 (SQLSTATE 42803) on every call. Grouping by
	// ga.owner directly is unambiguous and lets COALESCE(ga.owner,
	// 'unknown') appear in the SELECT list validly, since it's a pure
	// function of the grouped column.
	const teamQ = `
SELECT COALESCE(ga.owner, 'unknown') AS team,
  COALESCE(SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN tu.cost_usd
                    ELSE (tu.tokens_in  * mp.cost_per_1k_in  / 1000.0)
                       + (tu.tokens_out * mp.cost_per_1k_out / 1000.0)
               END), 0)           AS cost_usd,
  COALESCE(SUM(tu.tokens_in),  0) AS tokens_in,
  COALESCE(SUM(tu.tokens_out), 0) AS tokens_out
FROM token_usage tu
LEFT JOIN gateway_agents ga ON ga.id = tu.agent_id
LEFT JOIN model_pricing   mp ON mp.model = tu.model
WHERE tu.org_id = $1
  AND tu.recorded_at >= $2
  AND tu.recorded_at <  $3
GROUP BY ga.owner
ORDER BY cost_usd DESC`

	teamRows, err := db.Query(ctx, teamQ, orgID, fromTS, toTS)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer teamRows.Close()

	byTeam := make([]TeamSpend, 0)
	for teamRows.Next() {
		var t TeamSpend
		if err := teamRows.Scan(&t.Team, &t.CostUSD, &t.TokensIn, &t.TokensOut); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		byTeam = append(byTeam, t)
	}
	if err := teamRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// ── By model ──────────────────────────────────────────────────────────────
	const modelQ = `
SELECT tu.model,
  COALESCE(SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN tu.cost_usd
                    ELSE (tu.tokens_in  * mp.cost_per_1k_in  / 1000.0)
                       + (tu.tokens_out * mp.cost_per_1k_out / 1000.0)
               END), 0)           AS cost_usd,
  COALESCE(SUM(tu.tokens_in),  0) AS tokens_in,
  COALESCE(SUM(tu.tokens_out), 0) AS tokens_out
FROM token_usage tu
LEFT JOIN model_pricing mp ON mp.model = tu.model
WHERE tu.org_id = $1
  AND tu.recorded_at >= $2
  AND tu.recorded_at <  $3
GROUP BY tu.model
ORDER BY cost_usd DESC`

	modelRows, err := db.Query(ctx, modelQ, orgID, fromTS, toTS)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer modelRows.Close()

	byModel := make([]ModelSpend, 0)
	for modelRows.Next() {
		var m ModelSpend
		if err := modelRows.Scan(&m.Model, &m.CostUSD, &m.TokensIn, &m.TokensOut); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		byModel = append(byModel, m)
	}
	if err := modelRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, TokenSpendSummary{
		PeriodStart:    from,
		PeriodEnd:      to,
		TotalCostUSD:   totalCost,
		TotalTokensIn:  totalIn,
		TotalTokensOut: totalOut,
		ByAgent:        byAgent,
		ByTeam:         byTeam,
		ByModel:        byModel,
	})
}

// FinOpsTimeSeries handles GET /v1/finops/timeseries?from=DATE&to=DATE&granularity=day|hour|week&agent_id=UUID
func (s *Server) FinOpsTimeSeries(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	from, err := parseDateParam(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	to, err := parseDateParam(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !to.After(from) {
		writeError(w, http.StatusBadRequest, "bad_request", "to must be after from")
		return
	}

	granularity := r.URL.Query().Get("granularity")
	var bucket string
	switch granularity {
	case "hour":
		bucket = "1 hour"
	case "week":
		bucket = "1 week"
	case "day", "":
		bucket = "1 day"
		granularity = "day"
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "granularity must be day, hour, or week")
		return
	}

	// Optional agent_id filter.
	var agentFilter pgtype.UUID
	if agIDStr := r.URL.Query().Get("agent_id"); agIDStr != "" {
		agID, err := uuid.Parse(agIDStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid agent_id")
			return
		}
		agentFilter = pgtype.UUID{Bytes: agID, Valid: true}
	}

	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "finops store is not configured")
		return
	}

	orgID := pgtype.UUID{Bytes: uc.OrgID, Valid: true}
	fromTS := pgtype.Timestamptz{Time: from, Valid: true}
	toTS := pgtype.Timestamptz{Time: to, Valid: true}
	db := s.queries.DB()
	ctx := r.Context()

	// time_bucket() is a TimescaleDB function; the bucket interval is injected
	// via format (safe: value is controlled by the switch above, not user input).
	q := fmt.Sprintf(`
SELECT time_bucket('%s', tu.recorded_at) AS ts,
  COALESCE(SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN tu.cost_usd
                    ELSE (tu.tokens_in  * mp.cost_per_1k_in  / 1000.0)
                       + (tu.tokens_out * mp.cost_per_1k_out / 1000.0)
               END), 0)                          AS cost_usd,
  COALESCE(SUM(tu.tokens_in + tu.tokens_out), 0) AS tokens
FROM token_usage tu
LEFT JOIN model_pricing mp ON mp.model = tu.model
WHERE tu.org_id = $1
  AND tu.recorded_at >= $2
  AND tu.recorded_at <  $3
  AND ($4::uuid IS NULL OR tu.agent_id = $4)
GROUP BY ts
ORDER BY ts`, bucket)

	rows, err := db.Query(ctx, q, orgID, fromTS, toTS, agentFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	series := make([]SpendPoint, 0)
	for rows.Next() {
		var ts time.Time
		var cost float64
		var tokens int64
		if err := rows.Scan(&ts, &cost, &tokens); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		series = append(series, SpendPoint{
			Timestamp: ts,
			CostUSD:   cost,
			Tokens:    tokens,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SpendTimeSeries{
		Granularity: granularity,
		Series:      series,
	})
}
