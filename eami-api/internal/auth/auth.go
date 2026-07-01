// Package auth handles JWT RS256 signing/verification, password hashing,
// and refresh token lifecycle for the EAMI API.
package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Claims are the EAMI JWT payload fields.
type Claims struct {
	jwt.RegisteredClaims
	OrgID string `json:"org"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Service holds the RSA keypair and token TTL settings.
type Service struct {
	privateKey *rsa.PrivateKey
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewService creates an auth.Service. If keyPath is empty, a fresh 2048-bit
// RSA keypair is generated in memory (suitable for development).
func NewService(keyPath string, accessTTL, refreshTTL time.Duration) (*Service, error) {
	var key *rsa.PrivateKey
	var err error

	if keyPath == "" {
		key, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("auth: generate dev RSA key: %w", err)
		}
	} else {
		key, err = loadPrivateKey(keyPath)
		if err != nil {
			return nil, err
		}
	}

	return &Service{
		privateKey: key,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}, nil
}

// IssueAccessToken creates a signed RS256 JWT for the given user.
func (s *Service) IssueAccessToken(userID, orgID uuid.UUID, email, role string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(s.accessTTL)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.New().String(),
		},
		OrgID: orgID.String(),
		Email: email,
		Role:  role,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(s.privateKey)
	return signed, exp, err
}

// VerifyAccessToken parses and validates a JWT, returning the claims.
func (s *Service) VerifyAccessToken(tokenStr string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method %v", t.Header["alg"])
		}
		return &s.privateKey.PublicKey, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("auth: invalid token claims")
	}
	return claims, nil
}

// IssueRefreshToken generates a cryptographically random refresh token and
// returns both the raw token (to send to the client once) and its SHA-256
// hash (to store in the DB).
func IssueRefreshToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: generate refresh token: %w", err)
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hash = fmt.Sprintf("%x", sum)
	return raw, hash, nil
}

// HashPassword bcrypt-hashes a plaintext password (cost=12).
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(h), nil
}

// CheckPassword compares a plaintext password against a bcrypt hash.
// Returns nil if they match.
func CheckPassword(plain, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}

// APIKeyFromRaw generates a random API key and returns the key string (shown
// once to the user), its 12-char prefix (stored for display), and its SHA-256
// hash (stored in the DB for lookup).
func APIKeyFromRaw() (key, prefix, hash string, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("auth: generate api key: %w", err)
	}
	key = "eami_k_" + hex.EncodeToString(b)
	prefix = key[:12]
	sum := sha256.Sum256([]byte(key))
	hash = fmt.Sprintf("%x", sum)
	return key, prefix, hash, nil
}

// loadPrivateKey reads a PEM-encoded RSA private key from disk.
func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read key file %q: %w", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("auth: no PEM block in %q", path)
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rk, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("auth: PKCS8 key is not RSA")
		}
		return rk, nil
	default:
		return nil, fmt.Errorf("auth: unsupported PEM block type %q", block.Type)
	}
}
