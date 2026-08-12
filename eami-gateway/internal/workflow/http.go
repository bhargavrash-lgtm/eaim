package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/registry"
)

// AgentResolver resolves a JWT subject's agent name to its registry
// record. *registry.Registry satisfies this structurally, matching
// episode/http.go's own identical AgentResolver seam -- zero changes to
// the registry package, and a test seam for the bearer-JWT auth branch.
type AgentResolver interface {
	LookupByName(ctx context.Context, name string) (*registry.AgentRecord, error)
}

// HTTPHandler serves POST /v1/gateway/workflows/{workflowId}/run --
// single-path Bearer agent-JWT auth (no service-key dual-auth like
// episode/http.go: triggering a workflow run is an agent action, the same
// trust model as an MCP tool_call, not a server-to-server proxy call).
type HTTPHandler struct {
	identity *identity.Manager
	resolver AgentResolver
	exec     *Executor
}

// NewHTTPHandler returns an HTTPHandler.
func NewHTTPHandler(idm *identity.Manager, resolver AgentResolver, exec *Executor) *HTTPHandler {
	return &HTTPHandler{identity: idm, resolver: resolver, exec: exec}
}

// HandleRun handles POST /v1/gateway/workflows/{workflowId}/run.
// Synchronous: the HTTP response blocks until the run finishes,
// including any escalation Hold() -- the accepted interim behavior for
// this brief (see package doc). Returns the full RunResult as JSON
// regardless of whether the run completed/denied/failed -- only a
// genuine failure to even attempt the run (bad auth, unknown workflow,
// dangling step) produces a non-200 status.
func (h *HTTPHandler) HandleRun(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "missing Authorization: Bearer", http.StatusUnauthorized)
		return
	}
	claims, err := h.identity.Validate(auth[7:])
	if err != nil {
		http.Error(w, "invalid bearer token: "+err.Error(), http.StatusUnauthorized)
		return
	}
	agentName := strings.TrimPrefix(claims.Subject, "agent:")
	agentRec, err := h.resolver.LookupByName(r.Context(), agentName)
	if err != nil {
		http.Error(w, "agent not registered or suspended: "+err.Error(), http.StatusForbidden)
		return
	}

	workflowID, err := uuid.Parse(r.PathValue("workflowId"))
	if err != nil {
		http.Error(w, "invalid workflowId", http.StatusBadRequest)
		return
	}

	env := r.Header.Get("X-Environment")
	if env == "" {
		env = "unknown"
	}
	template := mcp.ActionContext{
		AgentID:     claims.Subject,
		AgentUUID:   agentRec.ID,
		AgentName:   agentName,
		OrgID:       agentRec.OrgID,
		AgentScope:  agentRec.Scope,
		Environment: env,
		ReceivedAt:  time.Now().UTC(),
	}

	// context.WithoutCancel, not r.Context() directly: the same real bug
	// B-039 found and fixed for the MCP async path applies here too, in a
	// variant this brief's own live verification reproduced organically
	// (a killed test client left a workflow_runs row stuck at 'running'
	// forever). Unlike MCP's ServeMessages (which returns immediately
	// after a 202 and dispatches in a detached goroutine), this handler
	// stays synchronous -- Run() genuinely blocks it for the whole
	// duration -- but a canceled r.Context() (client disconnect, proxy
	// timeout, anything) during a step's Hold() would still propagate into
	// every subsequent e.pool.Exec call this package makes, INCLUDING the
	// final status-finalizing UPDATE, silently losing the run's real
	// outcome. WithoutCancel keeps the DB writes (and any later steps)
	// completing correctly regardless of whether the original caller is
	// still there to receive the HTTP response -- writing the response
	// itself to a gone client is a harmless no-op, but losing the actual
	// run's recorded state is not.
	runCtx := context.WithoutCancel(r.Context())
	result, err := h.exec.Run(runCtx, template, workflowID)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, ErrWorkflowNotFound):
			status = http.StatusNotFound
		case errors.Is(err, ErrDanglingStep):
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runResultDTO{
		RunID:  result.RunID.String(),
		Status: result.Status,
		Steps:  stepResultsToDTO(result.Steps),
	})
}

type runResultDTO struct {
	RunID  string          `json:"run_id"`
	Status string          `json:"status"`
	Steps  []stepResultDTO `json:"steps"`
}

type stepResultDTO struct {
	StepOrder          int32           `json:"step_order"`
	ResolvedToolID     string          `json:"resolved_tool_id,omitempty"`
	ResolvedConfigHash string          `json:"resolved_config_hash,omitempty"`
	ProjectedDecision  string          `json:"projected_decision,omitempty"`
	Outcome            string          `json:"outcome"`
	Result             json.RawMessage `json:"result,omitempty"`
	ErrorDetail        string          `json:"error_detail,omitempty"`
	StartedAt          time.Time       `json:"started_at"`
	FinishedAt         time.Time       `json:"finished_at,omitempty"`
}

func stepResultsToDTO(steps []StepResult) []stepResultDTO {
	out := make([]stepResultDTO, 0, len(steps))
	for _, s := range steps {
		dto := stepResultDTO{
			StepOrder:          s.StepOrder,
			ResolvedConfigHash: s.ResolvedConfigHash,
			ProjectedDecision:  s.ProjectedDecision,
			Outcome:            s.Outcome,
			Result:             s.Result,
			ErrorDetail:        s.ErrorDetail,
			StartedAt:          s.StartedAt,
			FinishedAt:         s.FinishedAt,
		}
		if s.ResolvedToolID != uuid.Nil {
			dto.ResolvedToolID = s.ResolvedToolID.String()
		}
		out = append(out, dto)
	}
	return out
}
