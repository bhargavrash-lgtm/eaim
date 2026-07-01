package api

// store_adapter.go — queriesAdapter wraps *store.Queries to satisfy the Store
// interface defined in store_mock.go. Production NewServer sets s.storeIface
// to this adapter so that both the production path (s.queries) and the test
// path (s.storeIface via MockStore) converge on the same interface.
//
// NOTE: All handlers currently call s.queries directly for org-scoped queries
// (agents, policies, etc.) because those methods need orgID that the Store
// interface does not carry.  This adapter exists so that:
//   a) s.storeIface is never nil in production, enabling future migration.
//   b) Auth methods (GetUserByEmail, GetUserByID) can be served through the
//      interface in both production and test code.
//
// Methods that require orgID (GetAgent, DeleteAgent, GetPolicy, DeletePolicy)
// use uuid.Nil — these methods are only exercised through MockStore in tests;
// in production handlers use s.queries directly.

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// queriesAdapter adapts *store.Queries to the Store interface.
type queriesAdapter struct {
	q *store.Queries
}

// ── Auth ─────────────────────────────────────────────────────────────────────

func (a *queriesAdapter) GetUserByEmail(ctx context.Context, email string) (StoreUser, error) {
	u, err := a.q.GetUserByEmail(ctx, email)
	if err != nil {
		return StoreUser{}, err
	}
	return storeUserFromDB(u), nil
}

func (a *queriesAdapter) GetUserByID(ctx context.Context, id uuid.UUID) (StoreUser, error) {
	u, err := a.q.GetUserByID(ctx, id)
	if err != nil {
		return StoreUser{}, err
	}
	return storeUserFromDB(u), nil
}

func storeUserFromDB(u *store.User) StoreUser {
	su := StoreUser{
		ID:    u.ID,
		OrgID: u.OrgID,
		Email: u.Email,
		Role:  u.Role,
	}
	if u.Name.Valid {
		su.Name = u.Name.String
	}
	if u.PasswordHash.Valid {
		su.PasswordHash = u.PasswordHash.String
	}
	return su
}

// ── Agents ───────────────────────────────────────────────────────────────────

func (a *queriesAdapter) ListAgents(ctx context.Context, orgID uuid.UUID) ([]StoreAgent, error) {
	agents, err := a.q.ListAgents(ctx, orgID, nil, nil)
	if err != nil {
		return nil, err
	}
	out := make([]StoreAgent, len(agents))
	for i, ag := range agents {
		out[i] = storeAgentFromDB(ag)
	}
	return out, nil
}

func (a *queriesAdapter) CreateAgent(ctx context.Context, arg CreateAgentParams) (StoreAgent, error) {
	ag, err := a.q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID:           arg.OrgID,
		Name:            arg.Name,
		Model:           arg.Model,
		Owner:           arg.Owner,
		Scope:           arg.Scope,
		RiskTier:        arg.RiskTier,
		TokenTTLSeconds: arg.TokenTTLSeconds,
		// CreatedBy: not available through Store interface; leave as zero UUID.
	})
	if err != nil {
		return StoreAgent{}, err
	}
	return storeAgentFromDB(*ag), nil
}

// GetAgent: orgID not carried by Store interface; passes uuid.Nil.
// In production, handlers use s.queries directly with the real orgID.
func (a *queriesAdapter) GetAgent(ctx context.Context, id uuid.UUID) (StoreAgent, error) {
	ag, err := a.q.GetAgent(ctx, id, uuid.Nil)
	if err != nil {
		if err == pgx.ErrNoRows {
			return StoreAgent{}, ErrNotFound
		}
		return StoreAgent{}, err
	}
	return storeAgentFromDB(*ag), nil
}

// DeleteAgent: orgID not carried by Store interface; passes uuid.Nil.
func (a *queriesAdapter) DeleteAgent(ctx context.Context, id uuid.UUID) error {
	err := a.q.DeleteAgent(ctx, id, uuid.Nil)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	return nil
}

func storeAgentFromDB(ag store.GatewayAgent) StoreAgent {
	sa := StoreAgent{
		ID:              ag.ID,
		OrgID:           ag.OrgID,
		Name:            ag.Name,
		Model:           ag.Model,
		Owner:           ag.Owner,
		Scope:           ag.Scope,
		RiskTier:        ag.RiskTier,
		Status:          ag.Status,
		TokenTTLSeconds: ag.TokenTTLSeconds,
		CreatedAt:       ag.CreatedAt,
		UpdatedAt:       ag.UpdatedAt,
	}
	return sa
}

// ── Policies ─────────────────────────────────────────────────────────────────

func (a *queriesAdapter) ListPolicies(ctx context.Context, orgID uuid.UUID) ([]StorePolicy, error) {
	rows, err := a.q.ListPolicies(ctx, orgID, nil)
	if err != nil {
		return nil, err
	}
	out := make([]StorePolicy, 0, len(rows))
	for _, r := range rows {
		out = append(out, storePolicyFromRow(r))
	}
	return out, nil
}

func (a *queriesAdapter) CreatePolicy(ctx context.Context, arg CreatePolicyParams) (StorePolicy, error) {
	var desc pgtype.Text
	if arg.Description != "" {
		desc = pgtype.Text{String: arg.Description, Valid: true}
	}
	pol, err := a.q.CreatePolicy(ctx, store.CreatePolicyParams{
		OrgID:       arg.OrgID,
		Name:        arg.Name,
		Description: desc,
		Priority:    arg.Priority,
		Action:      arg.Action,
		Alert:       arg.Alert,
		Status:      arg.Status,
	})
	if err != nil {
		return StorePolicy{}, err
	}
	sp := storePolicyFromPolicy(*pol)
	sp.Conditions = arg.Conditions // pass through raw bytes from caller
	return sp, nil
}

// GetPolicy: orgID not carried by Store interface.
func (a *queriesAdapter) GetPolicy(ctx context.Context, id uuid.UUID) (StorePolicy, error) {
	row, err := a.q.GetPolicy(ctx, id, uuid.Nil)
	if err != nil {
		if err == pgx.ErrNoRows {
			return StorePolicy{}, ErrNotFound
		}
		return StorePolicy{}, err
	}
	return storePolicyFromRow(*row), nil
}

func (a *queriesAdapter) UpdatePolicy(ctx context.Context, arg UpdatePolicyParams) (StorePolicy, error) {
	var desc pgtype.Text
	if arg.Description != "" {
		desc = pgtype.Text{String: arg.Description, Valid: true}
	}
	pol, err := a.q.UpdatePolicy(ctx, store.UpdatePolicyParams{
		ID:          arg.ID,
		OrgID:       arg.OrgID,
		Name:        pgtype.Text{String: arg.Name, Valid: arg.Name != ""},
		Description: desc,
		Priority:    pgtype.Int4{Int32: arg.Priority, Valid: arg.Priority != 0},
		Action:      pgtype.Text{String: arg.Action, Valid: arg.Action != ""},
		Alert:       &arg.Alert,
		Status:      pgtype.Text{String: arg.Status, Valid: arg.Status != ""},
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return StorePolicy{}, ErrNotFound
		}
		return StorePolicy{}, err
	}
	return storePolicyFromPolicy(*pol), nil
}

// DeletePolicy: orgID not carried by Store interface.
func (a *queriesAdapter) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	err := a.q.DeletePolicy(ctx, id, uuid.Nil)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	return nil
}

func (a *queriesAdapter) ReorderPolicies(ctx context.Context, arg ReorderPoliciesParams) error {
	return a.q.ReorderPolicies(ctx, arg.OrgID, arg.PolicyIDs)
}

// ── Type converters ───────────────────────────────────────────────────────────

func storePolicyFromRow(r store.PolicyRow) StorePolicy {
	sp := storePolicyFromPolicy(r.Policy)
	// Serialize condition to JSON for the StorePolicy.Conditions []byte field.
	if b, err := json.Marshal(r.Condition); err == nil {
		sp.Conditions = b
	}
	return sp
}

func storePolicyFromPolicy(pol store.Policy) StorePolicy {
	sp := StorePolicy{
		ID:        pol.ID,
		OrgID:     pol.OrgID,
		Name:      pol.Name,
		Priority:  pol.Priority,
		Action:    pol.Action,
		Alert:     pol.Alert,
		Status:    pol.Status,
		CreatedAt: pol.CreatedAt,
		UpdatedAt: pol.UpdatedAt,
	}
	if pol.Description.Valid {
		sp.Description = pol.Description.String
	}
	return sp
}

// ── Approvals ─────────────────────────────────────────────────────────────────

func (a *queriesAdapter) CreateApproval(ctx context.Context, arg MockCreateApprovalParams) (StoreApproval, error) {
	ap, err := a.q.CreateApproval(ctx, store.CreateApprovalParams{
		OrgID:     arg.OrgID,
		AgentID:   arg.AgentID,
		AgentName: arg.AgentName,
		ToolName:  arg.ToolName,
		Action:    arg.Action,
		Justification: arg.Justification,
		RiskLevel: arg.RiskLevel,
		ExpiresAt: arg.ExpiresAt,
	})
	if err != nil {
		return StoreApproval{}, err
	}
	return storeApprovalFromDB(*ap), nil
}

func (a *queriesAdapter) GetApproval(ctx context.Context, id uuid.UUID, orgID uuid.UUID) (StoreApproval, error) {
	ap, err := a.q.GetApproval(ctx, id, orgID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return StoreApproval{}, ErrNotFound
		}
		return StoreApproval{}, err
	}
	return storeApprovalFromDB(*ap), nil
}

func (a *queriesAdapter) ListApprovals(ctx context.Context, arg MockListApprovalsParams) ([]StoreApproval, error) {
	rows, err := a.q.ListApprovals(ctx, store.ListApprovalsParams{
		OrgID:  arg.OrgID,
		Status: arg.Status,
		AgentID: arg.AgentID,
		Limit:  arg.Limit,
		Offset: arg.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]StoreApproval, len(rows))
	for i, r := range rows {
		out[i] = storeApprovalFromDB(r)
	}
	return out, nil
}

func (a *queriesAdapter) CountApprovals(ctx context.Context, arg MockListApprovalsParams) (int64, error) {
	return a.q.CountApprovals(ctx, store.ListApprovalsParams{
		OrgID:   arg.OrgID,
		Status:  arg.Status,
		AgentID: arg.AgentID,
		Limit:   arg.Limit,
		Offset:  arg.Offset,
	})
}

func (a *queriesAdapter) DecideApproval(ctx context.Context, arg MockDecideApprovalParams) (StoreApproval, error) {
	ap, err := a.q.DecideApproval(ctx, store.DecideApprovalParams{
		ID:             arg.ID,
		OrgID:          arg.OrgID,
		Status:         arg.Decision,
		DecisionReason: pgtype.Text{String: arg.Reason, Valid: arg.Reason != ""},
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return StoreApproval{}, ErrAlreadyDecided
		}
		return StoreApproval{}, err
	}
	return storeApprovalFromDB(*ap), nil
}

func storeApprovalFromDB(a store.ApprovalRequest) StoreApproval {
	sa := StoreApproval{
		ID:            a.ID,
		OrgID:         a.OrgID,
		AgentID:       a.AgentID,
		AgentName:     a.AgentName,
		ToolName:      a.ToolName,
		Action:        a.Action,
		Justification: a.Justification,
		RiskLevel:     a.RiskLevel,
		Status:        a.Status,
		ExpiresAt:     a.ExpiresAt,
		CreatedAt:     a.CreatedAt,
	}
	if a.ApprovedBy.Valid {
		s := uuid.UUID(a.ApprovedBy.Bytes).String()
		sa.DecidedBy = &s
	}
	if a.DecisionReason.Valid {
		sa.Reason = &a.DecisionReason.String
	}
	if a.DecidedAt.Valid {
		sa.DecidedAt = &a.DecidedAt.Time
	}
	return sa
}
