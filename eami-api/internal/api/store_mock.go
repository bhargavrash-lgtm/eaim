// store_mock.go — eami-api/internal/api
// QA-EAMI — Store interface + MockStore for handler unit tests.
//
// INTEGRATION INSTRUCTIONS for BE-Policy:
//
// 1. Add a Store interface to this package (or a new store.go file):
//
//      type Store interface {
//          GetUserByEmail(ctx context.Context, email string) (store.User, error)
//          GetUserByID(ctx context.Context, id uuid.UUID) (store.User, error)
//          ListAgents(ctx context.Context, orgID uuid.UUID) ([]store.Agent, error)
//          CreateAgent(ctx context.Context, arg store.CreateAgentParams) (store.Agent, error)
//          GetAgent(ctx context.Context, id uuid.UUID) (store.Agent, error)
//          DeleteAgent(ctx context.Context, id uuid.UUID) error
//          ListPolicies(ctx context.Context, orgID uuid.UUID) ([]store.Policy, error)
//          CreatePolicy(ctx context.Context, arg store.CreatePolicyParams) (store.Policy, error)
//          GetPolicy(ctx context.Context, id uuid.UUID) (store.Policy, error)
//          UpdatePolicy(ctx context.Context, arg store.UpdatePolicyParams) (store.Policy, error)
//          DeletePolicy(ctx context.Context, id uuid.UUID) error
//          ReorderPolicies(ctx context.Context, arg store.ReorderPoliciesParams) error
//      }
//
//    *store.Queries already satisfies this interface — no implementation change needed.
//    Handler constructors should accept Store, not *store.Queries directly.
//
// 2. The MockStore below is a test-only implementation that satisfies Store.
//    It lives in a non-test file so test files using `package api_test` can import it.
//    Add a `go:build !production` tag if you don't want it in release builds:
//
//      //go:build !production
//
// 3. Field names in StoreUser / StoreAgent / StorePolicy mirror the expected sqlc
//    output. Adjust to match actual generated types if they differ.
//
// NOTE: If the actual handler package uses its own concrete types instead of
// these stubs, replace StoreUser/StoreAgent/StorePolicy with the real store.* types
// and remove the redundant type declarations here.

package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrAlreadyDecided is returned when DecideApproval is called on an approval
// that already has a decision.
var ErrAlreadyDecided = errors.New("approval already decided")

// ─── Mirrored store types (adjust to match sqlc-generated store package) ────

// StoreUser mirrors store.User from sqlc output.
type StoreUser struct {
	ID           uuid.UUID
	Email        string
	Name         string
	Role         string // "admin" | "operator" | "viewer"
	OrgID        uuid.UUID
	PasswordHash string
}

// StoreAgent mirrors store.Agent from sqlc output.
type StoreAgent struct {
	ID               uuid.UUID
	OrgID            uuid.UUID
	Name             string
	Model            string
	Owner            string
	Scope            string
	RiskTier         string // "low" | "medium" | "high"
	Status           string // "active" | "suspended" | "revoked"
	TokenTTLSeconds  int32
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CreateAgentParams mirrors store.CreateAgentParams.
type CreateAgentParams struct {
	OrgID           uuid.UUID
	Name            string
	Model           string
	Owner           string
	Scope           string
	RiskTier        string
	TokenTTLSeconds int32
}

// StorePolicy mirrors store.Policy from sqlc output.
type StorePolicy struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description string
	Priority    int32
	Conditions  []byte // JSONB stored as raw JSON
	Action      string // "allow" | "deny" | "escalate"
	Alert       bool
	Status      string // "active" | "draft" | "disabled"
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreatePolicyParams mirrors store.CreatePolicyParams.
type CreatePolicyParams struct {
	OrgID       uuid.UUID
	Name        string
	Description string
	Priority    int32
	Conditions  []byte
	Action      string
	Alert       bool
	Status      string
}

// UpdatePolicyParams mirrors store.UpdatePolicyParams.
type UpdatePolicyParams struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description string
	Priority    int32
	Conditions  []byte
	Action      string
	Alert       bool
	Status      string
}

// ReorderPoliciesParams mirrors store.ReorderPoliciesParams.
type ReorderPoliciesParams struct {
	OrgID      uuid.UUID
	PolicyIDs  []uuid.UUID // in desired priority order
}

// ─── Store interface ─────────────────────────────────────────────────────────

// ─── Approval store types ────────────────────────────────────────────────────

// StoreApproval mirrors the approval_requests row returned by store queries.
type StoreApproval struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	AgentID       uuid.UUID
	AgentName     string
	ToolName      string
	Action        string
	Justification string
	RiskLevel     string
	Status        string // "pending" | "approved" | "denied" | "expired"
	DecidedBy     *string
	Reason        *string
	ExpiresAt     time.Time
	CreatedAt     time.Time
	DecidedAt     *time.Time
}

// CreateApprovalParams for MockStore.CreateApproval.
type MockCreateApprovalParams struct {
	OrgID         uuid.UUID
	AgentID       uuid.UUID
	AgentName     string
	ToolName      string
	Action        string
	Justification string
	RiskLevel     string
	ExpiresAt     time.Time
}

// ListApprovalsParams for MockStore.ListApprovals.
type MockListApprovalsParams struct {
	OrgID   uuid.UUID
	Status  *string
	AgentID *uuid.UUID
	Limit   int32
	Offset  int32
}

// DecideApprovalParams for MockStore.DecideApproval.
type MockDecideApprovalParams struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Decision  string // "approved" | "denied"
	DecidedBy string
	Reason    string
}

// ─── Store interface ─────────────────────────────────────────────────────────

// Store abstracts *store.Queries for handler dependency injection.
// The real *store.Queries must satisfy this interface (it will if generated correctly).
type Store interface {
	// Auth
	GetUserByEmail(ctx context.Context, email string) (StoreUser, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (StoreUser, error)

	// Agents
	ListAgents(ctx context.Context, orgID uuid.UUID) ([]StoreAgent, error)
	CreateAgent(ctx context.Context, arg CreateAgentParams) (StoreAgent, error)
	GetAgent(ctx context.Context, id uuid.UUID) (StoreAgent, error)
	DeleteAgent(ctx context.Context, id uuid.UUID) error

	// Policies
	ListPolicies(ctx context.Context, orgID uuid.UUID) ([]StorePolicy, error)
	CreatePolicy(ctx context.Context, arg CreatePolicyParams) (StorePolicy, error)
	GetPolicy(ctx context.Context, id uuid.UUID) (StorePolicy, error)
	UpdatePolicy(ctx context.Context, arg UpdatePolicyParams) (StorePolicy, error)
	DeletePolicy(ctx context.Context, id uuid.UUID) error
	ReorderPolicies(ctx context.Context, arg ReorderPoliciesParams) error

	// Approvals
	CreateApproval(ctx context.Context, arg MockCreateApprovalParams) (StoreApproval, error)
	GetApproval(ctx context.Context, id uuid.UUID, orgID uuid.UUID) (StoreApproval, error)
	ListApprovals(ctx context.Context, arg MockListApprovalsParams) ([]StoreApproval, error)
	CountApprovals(ctx context.Context, arg MockListApprovalsParams) (int64, error)
	DecideApproval(ctx context.Context, arg MockDecideApprovalParams) (StoreApproval, error)
}

// ErrNotFound is returned by MockStore when a resource doesn't exist.
var ErrNotFound = errors.New("not found")

// ─── MockStore ───────────────────────────────────────────────────────────────

// MockStore is a thread-safe in-memory implementation of Store for use in tests.
// Configure error fields before each test case; reset between sub-tests.
//
// Usage:
//
//	ms := &MockStore{}
//	ms.Users["alice@example.com"] = StoreUser{
//	    ID: uuid.New(), Email: "alice@example.com",
//	    PasswordHash: mustBcrypt("secret"), Role: "admin",
//	    OrgID: testOrgID,
//	}
//	h := NewHandler(ms, authSvc)
type MockStore struct {
	mu sync.RWMutex

	// Seeded data
	Users     map[string]StoreUser // keyed by email
	UsersByID map[uuid.UUID]StoreUser
	Agents    map[uuid.UUID]StoreAgent
	Policies  map[uuid.UUID]StorePolicy
	Approvals map[uuid.UUID]StoreApproval

	// Errors to return (override per test)
	GetUserByEmailErr error
	GetUserByIDErr    error
	ListAgentsErr     error
	CreateAgentErr    error
	GetAgentErr       error
	DeleteAgentErr    error
	ListPoliciesErr   error
	CreatePolicyErr   error
	GetPolicyErr      error
	UpdatePolicyErr   error
	DeletePolicyErr   error
	ReorderErr        error
	CreateApprovalErr error
	GetApprovalErr    error
	ListApprovalsErr  error
	DecideApprovalErr error

	// Call counts for assertion
	CreateAgentCalls    int
	DeleteAgentCalls    int
	CreatePolicyCalls   int
	ReorderCalls        int
	CreateApprovalCalls int
	DecideApprovalCalls int
}

// NewMockStore returns a zeroed MockStore ready to configure.
func NewMockStore() *MockStore {
	return &MockStore{
		Users:     make(map[string]StoreUser),
		UsersByID: make(map[uuid.UUID]StoreUser),
		Agents:    make(map[uuid.UUID]StoreAgent),
		Policies:  make(map[uuid.UUID]StorePolicy),
		Approvals: make(map[uuid.UUID]StoreApproval),
	}
}

// SeedApproval adds an approval.
func (m *MockStore) SeedApproval(a StoreApproval) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Approvals[a.ID] = a
}

// SeedUser adds a user by email and ID.
func (m *MockStore) SeedUser(u StoreUser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Users[u.Email] = u
	m.UsersByID[u.ID] = u
}

// SeedAgent adds an agent.
func (m *MockStore) SeedAgent(a StoreAgent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Agents[a.ID] = a
}

// SeedPolicy adds a policy.
func (m *MockStore) SeedPolicy(p StorePolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Policies[p.ID] = p
}

// ─── Store interface implementation ─────────────────────────────────────────

func (m *MockStore) GetUserByEmail(ctx context.Context, email string) (StoreUser, error) {
	if m.GetUserByEmailErr != nil {
		return StoreUser{}, m.GetUserByEmailErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.Users[email]
	if !ok {
		return StoreUser{}, ErrNotFound
	}
	return u, nil
}

func (m *MockStore) GetUserByID(ctx context.Context, id uuid.UUID) (StoreUser, error) {
	if m.GetUserByIDErr != nil {
		return StoreUser{}, m.GetUserByIDErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.UsersByID[id]
	if !ok {
		return StoreUser{}, ErrNotFound
	}
	return u, nil
}

func (m *MockStore) ListAgents(ctx context.Context, orgID uuid.UUID) ([]StoreAgent, error) {
	if m.ListAgentsErr != nil {
		return nil, m.ListAgentsErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []StoreAgent
	for _, a := range m.Agents {
		if a.OrgID == orgID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MockStore) CreateAgent(ctx context.Context, arg CreateAgentParams) (StoreAgent, error) {
	m.mu.Lock()
	m.CreateAgentCalls++
	m.mu.Unlock()
	if m.CreateAgentErr != nil {
		return StoreAgent{}, m.CreateAgentErr
	}
	a := StoreAgent{
		ID:              uuid.New(),
		OrgID:           arg.OrgID,
		Name:            arg.Name,
		Model:           arg.Model,
		Owner:           arg.Owner,
		Scope:           arg.Scope,
		RiskTier:        arg.RiskTier,
		Status:          "active",
		TokenTTLSeconds: arg.TokenTTLSeconds,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	m.mu.Lock()
	m.Agents[a.ID] = a
	m.mu.Unlock()
	return a, nil
}

func (m *MockStore) GetAgent(ctx context.Context, id uuid.UUID) (StoreAgent, error) {
	if m.GetAgentErr != nil {
		return StoreAgent{}, m.GetAgentErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.Agents[id]
	if !ok {
		return StoreAgent{}, ErrNotFound
	}
	return a, nil
}


func (m *MockStore) DeleteAgent(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	m.DeleteAgentCalls++
	m.mu.Unlock()
	if m.DeleteAgentErr != nil {
		return m.DeleteAgentErr
	}
	m.mu.RLock()
	_, ok := m.Agents[id]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	m.mu.Lock()
	delete(m.Agents, id)
	m.mu.Unlock()
	return nil
}

func (m *MockStore) ListPolicies(ctx context.Context, orgID uuid.UUID) ([]StorePolicy, error) {
	if m.ListPoliciesErr != nil {
		return nil, m.ListPoliciesErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []StorePolicy
	for _, p := range m.Policies {
		if p.OrgID == orgID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *MockStore) CreatePolicy(ctx context.Context, arg CreatePolicyParams) (StorePolicy, error) {
	m.mu.Lock()
	m.CreatePolicyCalls++
	m.mu.Unlock()
	if m.CreatePolicyErr != nil {
		return StorePolicy{}, m.CreatePolicyErr
	}
	p := StorePolicy{
		ID:          uuid.New(),
		OrgID:       arg.OrgID,
		Name:        arg.Name,
		Description: arg.Description,
		Priority:    arg.Priority,
		Conditions:  arg.Conditions,
		Action:      arg.Action,
		Alert:       arg.Alert,
		Status:      arg.Status,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	m.mu.Lock()
	m.Policies[p.ID] = p
	m.mu.Unlock()
	return p, nil
}

func (m *MockStore) GetPolicy(ctx context.Context, id uuid.UUID) (StorePolicy, error) {
	if m.GetPolicyErr != nil {
		return StorePolicy{}, m.GetPolicyErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.Policies[id]
	if !ok {
		return StorePolicy{}, ErrNotFound
	}
	return p, nil
}

func (m *MockStore) UpdatePolicy(ctx context.Context, arg UpdatePolicyParams) (StorePolicy, error) {
	if m.UpdatePolicyErr != nil {
		return StorePolicy{}, m.UpdatePolicyErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.Policies[arg.ID]
	if !ok {
		return StorePolicy{}, ErrNotFound
	}
	p.Name = arg.Name
	p.Priority = arg.Priority
	p.Action = arg.Action
	p.Status = arg.Status
	p.UpdatedAt = time.Now().UTC()
	m.Policies[arg.ID] = p
	return p, nil
}

func (m *MockStore) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	if m.DeletePolicyErr != nil {
		return m.DeletePolicyErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Policies[id]; !ok {
		return ErrNotFound
	}
	delete(m.Policies, id)
	return nil
}

func (m *MockStore) ReorderPolicies(ctx context.Context, arg ReorderPoliciesParams) error {
	m.mu.Lock()
	m.ReorderCalls++
	m.mu.Unlock()
	return m.ReorderErr
}

// ─── Approval interface implementation ───────────────────────────────────────

func (m *MockStore) CreateApproval(ctx context.Context, arg MockCreateApprovalParams) (StoreApproval, error) {
	m.mu.Lock()
	m.CreateApprovalCalls++
	m.mu.Unlock()
	if m.CreateApprovalErr != nil {
		return StoreApproval{}, m.CreateApprovalErr
	}
	a := StoreApproval{
		ID:            uuid.New(),
		OrgID:         arg.OrgID,
		AgentID:       arg.AgentID,
		AgentName:     arg.AgentName,
		ToolName:      arg.ToolName,
		Action:        arg.Action,
		Justification: arg.Justification,
		RiskLevel:     arg.RiskLevel,
		Status:        "pending",
		ExpiresAt:     arg.ExpiresAt,
		CreatedAt:     time.Now().UTC(),
	}
	m.mu.Lock()
	m.Approvals[a.ID] = a
	m.mu.Unlock()
	return a, nil
}

func (m *MockStore) GetApproval(ctx context.Context, id uuid.UUID, orgID uuid.UUID) (StoreApproval, error) {
	if m.GetApprovalErr != nil {
		return StoreApproval{}, m.GetApprovalErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.Approvals[id]
	if !ok || a.OrgID != orgID {
		return StoreApproval{}, ErrNotFound
	}
	return a, nil
}

func (m *MockStore) ListApprovals(ctx context.Context, arg MockListApprovalsParams) ([]StoreApproval, error) {
	if m.ListApprovalsErr != nil {
		return nil, m.ListApprovalsErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []StoreApproval
	for _, a := range m.Approvals {
		if a.OrgID != arg.OrgID {
			continue
		}
		if arg.Status != nil && a.Status != *arg.Status {
			continue
		}
		if arg.AgentID != nil && a.AgentID != *arg.AgentID {
			continue
		}
		out = append(out, a)
	}
	// Apply offset/limit.
	offset := int(arg.Offset)
	if offset > len(out) {
		offset = len(out)
	}
	out = out[offset:]
	if arg.Limit > 0 && int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (m *MockStore) CountApprovals(ctx context.Context, arg MockListApprovalsParams) (int64, error) {
	if m.ListApprovalsErr != nil {
		return 0, m.ListApprovalsErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int64
	for _, a := range m.Approvals {
		if a.OrgID != arg.OrgID {
			continue
		}
		if arg.Status != nil && a.Status != *arg.Status {
			continue
		}
		if arg.AgentID != nil && a.AgentID != *arg.AgentID {
			continue
		}
		n++
	}
	return n, nil
}

func (m *MockStore) DecideApproval(ctx context.Context, arg MockDecideApprovalParams) (StoreApproval, error) {
	m.mu.Lock()
	m.DecideApprovalCalls++
	m.mu.Unlock()
	if m.DecideApprovalErr != nil {
		return StoreApproval{}, m.DecideApprovalErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.Approvals[arg.ID]
	if !ok || a.OrgID != arg.OrgID {
		return StoreApproval{}, ErrNotFound
	}
	if a.Status != "pending" {
		return StoreApproval{}, ErrAlreadyDecided
	}
	now := time.Now().UTC()
	a.Status = arg.Decision
	a.DecidedBy = &arg.DecidedBy
	if arg.Reason != "" {
		a.Reason = &arg.Reason
	}
	a.DecidedAt = &now
	m.Approvals[arg.ID] = a
	return a, nil
}
