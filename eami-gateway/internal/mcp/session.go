// Session manager for MCP SSE persistent connections.
//
// MCP SSE transport lifecycle:
//
//  1. Agent connects GET /v1/mcp/sse (Bearer token in header)
//     -> gateway creates Session, sends "endpoint" event
//
//  2. Agent sends JSON-RPC via POST /v1/mcp/messages?sessionId=<id>
//     -> gateway processes, sends response as SSE "message" event
//
//  3. Token TTL expires or agent disconnects -> session cancelled
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
)

// sseEvent is a single Server-Sent Event.
type sseEvent struct {
	Event string
	Data  string
}

// Session represents one live SSE connection from an AI agent.
type Session struct {
	ID     string
	Claims *identity.Claims
	Agent  *registry.AgentRecord

	events chan sseEvent
	ctx    context.Context
	cancel context.CancelFunc
}

// Send queues an SSE event. Blocks until the SSE loop reads it or session is cancelled.
func (s *Session) Send(event sseEvent) error {
	select {
	case s.events <- event:
		return nil
	case <-s.ctx.Done():
		return fmt.Errorf("session %s closed", s.ID)
	}
}

// Done returns a channel closed when the session is cancelled.
func (s *Session) Done() <-chan struct{} {
	return s.ctx.Done()
}

// SessionManager tracks live SSE sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager creates an empty SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]*Session)}
}

// Create allocates a new Session that auto-expires at tokenExpiry.
func (m *SessionManager) Create(
	claims *identity.Claims,
	agent *registry.AgentRecord,
	tokenExpiry time.Time,
) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, fmt.Errorf("session: generate id: %w", err)
	}
	ttl := time.Until(tokenExpiry)
	if ttl <= 0 {
		return nil, fmt.Errorf("session: token already expired")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ttl)
	s := &Session{
		ID:     id,
		Claims: claims,
		Agent:  agent,
		events: make(chan sseEvent, 32),
		ctx:    ctx,
		cancel: cancel,
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.remove(id)
	}()
	return s, nil
}

// Get retrieves a session by ID. Returns nil if not found.
func (m *SessionManager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// Close cancels the session and removes it.
func (m *SessionManager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
		s.cancel()
	}
}

// Count returns the number of live sessions.
func (m *SessionManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

func (m *SessionManager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func newSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
