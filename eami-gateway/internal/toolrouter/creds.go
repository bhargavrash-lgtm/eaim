package toolrouter

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
)

// keySize is the required raw key length for AES-256 (32 bytes = 64 hex chars).
const keySize = 32

// Cipher decrypts gateway_tools credential blobs encrypted by
// eami-api/internal/toolcreds.Cipher (B-022) -- same key
// (TOOL_CREDENTIALS_ENCRYPTION_KEY), same AES-256-GCM scheme, decrypt-only:
// eami-gateway only ever reads credentials another process already wrote,
// never encrypts/stores them itself.
//
// Duplicated rather than imported for the same module-boundary reason as
// dial.go's safeDialContext -- see that file's doc comment.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a hex-encoded 32-byte key. Returns an
// error on empty/malformed/wrong-length input, matching toolcreds.NewCipher's
// fail-loud contract -- never a Cipher that would silently no-op or panic
// later.
func NewCipher(hexKey string) (*Cipher, error) {
	if hexKey == "" {
		return nil, errors.New("toolrouter: encryption key is empty")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("toolrouter: encryption key is not valid hex: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("toolrouter: encryption key must decode to %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("toolrouter: init AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("toolrouter: init GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Decrypt opens a blob previously produced by toolcreds.Cipher.Encrypt
// (nonce||ciphertext). Returns an error if the blob is truncated, was
// sealed with a different key, or has been tampered with (GCM
// authentication failure) -- callers must treat this as a clean rejection,
// never a panic.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(blob) < nonceSize {
		return nil, errors.New("toolrouter: ciphertext shorter than nonce")
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("toolrouter: decrypt: %w", err)
	}
	return plaintext, nil
}

// Credentials mirrors eami-api/internal/api.ToolCredentials -- the shape
// gateway_tools.credentials_encrypted decrypts to (B-022's documented
// openapi.yaml ToolCreate.credentials shape). Duplicated for the same
// module-boundary reason as Cipher above.
type Credentials struct {
	APIKey            string `json:"api_key,omitempty"`
	OAuthClientID     string `json:"oauth_client_id,omitempty"`
	OAuthClientSecret string `json:"oauth_client_secret,omitempty"`
	ConnectionString  string `json:"connection_string,omitempty"`
}
