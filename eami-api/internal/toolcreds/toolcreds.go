// Package toolcreds provides application-level AES-256-GCM encryption for
// gateway tool credentials (api_key, oauth secrets, DB connection strings)
// before they are written to gateway_tools.credentials_encrypted.
//
// Encryption happens here, in the API process, using a key that never
// leaves it -- Postgres only ever sees ciphertext. This is deliberately not
// pgcrypto (which would require sending the key to Postgres as a query
// parameter on every write, so a DB-level compromise exposes the key
// alongside the ciphertext it protects).
package toolcreds

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// keySize is the required raw key length for AES-256 (32 bytes = 64 hex chars).
const keySize = 32

// Cipher encrypts and decrypts tool credential blobs with AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a hex-encoded 32-byte key (e.g. the output
// of `openssl rand -hex 32`). It fails loudly on any misconfiguration --
// empty, malformed, or wrong-length input -- rather than returning a Cipher
// that would silently no-op or panic later.
func NewCipher(hexKey string) (*Cipher, error) {
	if hexKey == "" {
		return nil, errors.New("toolcreds: encryption key is empty")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("toolcreds: encryption key is not valid hex: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("toolcreds: encryption key must decode to %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("toolcreds: init AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("toolcreds: init GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext, returning nonce||ciphertext as a single blob
// suitable for storage in a BYTEA column.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("toolcreds: generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens a blob previously produced by Encrypt. It returns an error
// if the blob is truncated, was sealed with a different key, or has been
// tampered with (GCM authentication failure). Not called from any
// production HTTP path -- credentials are write-only from the API's
// perspective -- but exposed so callers (tests, future admin tooling) can
// prove round-trip correctness.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(blob) < nonceSize {
		return nil, errors.New("toolcreds: ciphertext shorter than nonce")
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("toolcreds: decrypt: %w", err)
	}
	return plaintext, nil
}
