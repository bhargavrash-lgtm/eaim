// Package identity handles AI token issuance and validation (ADR-006).
//
// Tokens are JWTs signed with RS256. The RSA keypair is generated on first
// start and written to the path configured in config.Token.KeypairPath.
// The public key is exposed at GET /.well-known/gateway-jwks.json.
//
// # Revocation persistence
//
// Revocations are persisted so that a restarted Manager (or a second node
// sharing the same key path) does not re-accept a revoked token.
//
//   - NewManager (no DB): revocations are appended to <keypairPath>.revocations
//     (one JTI per line). Suitable for single-node dev and unit tests.
//   - NewManagerWithDB: revocations are written to the revoked_ai_tokens
//     Postgres table and hydrated from it on startup. Required for production
//     and multi-node deployments.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxTTL = 14400
	minTTL = 60
)

// Claims are the custom JWT claims for an AI token.
type Claims struct {
	jwt.RegisteredClaims
	Scope    string `json:"scope"`
	Task     string `json:"task"`
	Model    string `json:"model"`
	Owner    string `json:"owner"`
	RiskTier string `json:"risk_tier"`
}

// IssueRequest is the request body for POST /v1/gateway/tokens.
type IssueRequest struct {
	AgentID    string `json:"agent_id"`
	Scope      string `json:"scope"`
	Task       string `json:"task"`
	Model      string `json:"model"`
	Owner      string `json:"owner"`
	RiskTier   string `json:"risk_tier"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// IssueResponse is the response body for POST /v1/gateway/tokens.
type IssueResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ─── Revocation store ────────────────────────────────────────────────────────

// revocationStore is the persistence interface for the revocation list.
// Two implementations are provided: fileRevocationStore and dbRevocationStore.
type revocationStore interface {
	// save persists a revoked JTI. expiresAt is advisory; pass time.Time{}
	// when the expiry is unknown (file store ignores it).
	save(ctx context.Context, jti string, expiresAt time.Time) error
	// loadAll returns all currently-valid (non-expired) revoked JTIs.
	loadAll(ctx context.Context) ([]string, error)
}

// fileRevocationStore appends one JTI per line to <keyPath>.revocations.
// It is used by NewManager (no DB) and satisfies the unit-test requirement
// that revocations survive a Manager restart sharing the same key file.
type fileRevocationStore struct {
	path string
	mu   sync.Mutex
}

func (s *fileRevocationStore) save(_ context.Context, jti string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("revocation file open: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, jti)
	return err
}

func (s *fileRevocationStore) loadAll(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("revocation file read: %w", err)
	}
	var jtis []string
	for _, line := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			jtis = append(jtis, t)
		}
	}
	return jtis, nil
}

// dbRevocationStore writes to and reads from the revoked_ai_tokens Postgres table.
// Used by NewManagerWithDB for production / multi-node deployments.
type dbRevocationStore struct {
	pool *pgxpool.Pool
}

func (s *dbRevocationStore) save(ctx context.Context, jti string, expiresAt time.Time) error {
	if expiresAt.IsZero() {
		// Conservative upper bound: no token can live longer than maxTTL.
		expiresAt = time.Now().UTC().Add(maxTTL * time.Second)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO revoked_ai_tokens (jti, expires_at)
		VALUES ($1, $2)
		ON CONFLICT (jti) DO NOTHING
	`, jti, expiresAt)
	if err != nil {
		return fmt.Errorf("revocation db insert: %w", err)
	}
	return nil
}

func (s *dbRevocationStore) loadAll(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT jti FROM revoked_ai_tokens WHERE expires_at > NOW()`)
	if err != nil {
		return nil, fmt.Errorf("revocation db query: %w", err)
	}
	defer rows.Close()
	var jtis []string
	for rows.Next() {
		var jti string
		if err := rows.Scan(&jti); err != nil {
			return nil, fmt.Errorf("revocation db scan: %w", err)
		}
		jtis = append(jtis, jti)
	}
	return jtis, rows.Err()
}

// ─── Manager ─────────────────────────────────────────────────────────────────

// Manager manages the RSA keypair and issues / validates tokens.
type Manager struct {
	mu         sync.RWMutex
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	issuer     string
	defaultTTL time.Duration

	revokedMu sync.RWMutex
	revoked   map[string]struct{}
	store     revocationStore
}

// NewManager loads or generates the RSA keypair and returns a Manager backed
// by a file-based revocation store (<keypairPath>.revocations).
//
// The file store persists revocations across restarts for single-node use and
// satisfies the unit-test contract without requiring a database.
// For production multi-node deployments use NewManagerWithDB instead.
func NewManager(keypairPath string, defaultTTLSeconds int, issuer string) (*Manager, error) {
	pk, err := loadOrGenerateKey(keypairPath)
	if err != nil {
		return nil, err
	}
	store := &fileRevocationStore{path: keypairPath + ".revocations"}
	return newManager(pk, defaultTTLSeconds, issuer, store)
}

// NewManagerWithDB is identical to NewManager but uses the revoked_ai_tokens
// Postgres table for revocation persistence. Required for production and
// multi-node deployments (ADR-006 / FINDING JWT-002).
func NewManagerWithDB(keypairPath string, defaultTTLSeconds int, issuer string, pool *pgxpool.Pool) (*Manager, error) {
	pk, err := loadOrGenerateKey(keypairPath)
	if err != nil {
		return nil, err
	}
	return newManager(pk, defaultTTLSeconds, issuer, &dbRevocationStore{pool: pool})
}

// newManager is the shared constructor used by both public constructors.
func newManager(pk *rsa.PrivateKey, defaultTTLSeconds int, issuer string, store revocationStore) (*Manager, error) {
	m := &Manager{
		privateKey: pk,
		publicKey:  &pk.PublicKey,
		issuer:     issuer,
		defaultTTL: time.Duration(defaultTTLSeconds) * time.Second,
		revoked:    make(map[string]struct{}),
		store:      store,
	}
	// Hydrate in-memory revocation set from the backing store.
	jtis, err := store.loadAll(context.Background())
	if err != nil {
		slog.Warn("identity: failed to load revocation list on startup", "err", err)
	} else if len(jtis) > 0 {
		for _, jti := range jtis {
			m.revoked[jti] = struct{}{}
		}
		slog.Info("identity: hydrated revocation list", "count", len(jtis))
	}
	return m, nil
}

// Issue mints a signed JWT for the given request.
func (m *Manager) Issue(req IssueRequest) (*IssueResponse, error) {
	if req.AgentID == "" {
		return nil, errors.New("identity: agent_id is required")
	}
	ttl := m.defaultTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if ttl < minTTL*time.Second {
		ttl = minTTL * time.Second
	}
	if ttl > maxTTL*time.Second {
		ttl = maxTTL * time.Second
	}
	now := time.Now().UTC()
	exp := now.Add(ttl)
	jtiRaw := fmt.Sprintf("%s:%d", req.AgentID, now.UnixNano())
	jtiHash := sha256.Sum256([]byte(jtiRaw))
	jti := fmt.Sprintf("%x", jtiHash[:8])

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   req.AgentID,
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{"eami-gateway"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        jti,
		},
		Scope:    req.Scope,
		Task:     req.Task,
		Model:    req.Model,
		Owner:    req.Owner,
		RiskTier: req.RiskTier,
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return nil, fmt.Errorf("identity: sign token: %w", err)
	}
	return &IssueResponse{Token: signed, ExpiresAt: exp}, nil
}

// Validate parses and validates a Bearer token string.
func (m *Manager) Validate(tokenStr string) (*Claims, error) {
	m.mu.RLock()
	pub := m.publicKey
	m.mu.RUnlock()
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("identity: unexpected signing method: %v", t.Header["alg"])
		}
		return pub, nil
	}, jwt.WithAudience("eami-gateway"), jwt.WithIssuer(m.issuer))
	if err != nil {
		return nil, fmt.Errorf("identity: invalid token: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("identity: token claims invalid")
	}
	m.revokedMu.RLock()
	_, isRevoked := m.revoked[claims.ID]
	m.revokedMu.RUnlock()
	if isRevoked {
		return nil, fmt.Errorf("identity: token %s has been revoked", claims.ID)
	}
	return claims, nil
}

// Revoke adds jti to the in-memory revocation set and persists it to the
// backing store (file or DB) so that it survives gateway restarts.
func (m *Manager) Revoke(jti string) {
	m.revokedMu.Lock()
	m.revoked[jti] = struct{}{}
	m.revokedMu.Unlock()

	if err := m.store.save(context.Background(), jti, time.Time{}); err != nil {
		slog.Error("identity: failed to persist revocation", "jti", jti, "err", err)
	}
}

// PublicKeyPEM returns the public key in PEM format.
func (m *Manager) PublicKeyPEM() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pubDER, err := x509.MarshalPKIXPublicKey(m.publicKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), nil
}

// HandleIssue is the HTTP handler for POST /v1/gateway/tokens.
func (m *Manager) HandleIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req IssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := m.Issue(req)
	if err != nil {
		slog.Error("token issuance failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleJWKS serves the public key at GET /.well-known/gateway-jwks.json.
func (m *Manager) HandleJWKS(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	pub := m.publicKey
	m.mu.RUnlock()

	type jwk struct {
		Kty string `json:"kty"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	type jwks struct {
		Keys []jwk `json:"keys"`
	}

	b64url := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	nBytes := pub.N.Bytes()
	eBytes := []byte{byte(pub.E >> 16), byte(pub.E >> 8), byte(pub.E)}
	for len(eBytes) > 1 && eBytes[0] == 0 {
		eBytes = eBytes[1:]
	}
	set := jwks{Keys: []jwk{{Kty: "RSA", Alg: "RS256", Use: "sig",
		N: b64url(nBytes), E: b64url(eBytes)}}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(set)
}

// Middleware validates Bearer token and injects claims into context.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			http.Error(w, "unauthorized: missing Bearer token", http.StatusUnauthorized)
			return
		}
		claims, err := m.Validate(auth[7:])
		if err != nil {
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
	})
}

func loadOrGenerateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("identity: invalid PEM in %s", path)
		}
		pk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("identity: parse key %s: %w", path, err)
		}
		slog.Info("identity: loaded existing RSA keypair", "path", path)
		return pk, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("identity: read key file %s: %w", path, err)
	}
	slog.Info("identity: generating new RSA 2048-bit keypair", "path", path)
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("identity: generate key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("identity: create key dir: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(pk),
	})
	if err := os.WriteFile(path, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("identity: write key %s: %w", path, err)
	}
	slog.Info("identity: RSA keypair written", "path", path)
	return pk, nil
}
