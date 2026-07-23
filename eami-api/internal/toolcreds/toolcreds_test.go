package toolcreds

import (
	"bytes"
	"strings"
	"testing"
)

const testKeyHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 32 bytes

func TestNewCipher_ValidKey(t *testing.T) {
	if _, err := NewCipher(testKeyHex); err != nil {
		t.Fatalf("NewCipher with a valid 32-byte hex key must succeed: %v", err)
	}
}

func TestNewCipher_EmptyKey(t *testing.T) {
	if _, err := NewCipher(""); err == nil {
		t.Fatal("NewCipher must reject an empty key")
	}
}

func TestNewCipher_NotHex(t *testing.T) {
	if _, err := NewCipher("not-hex-at-all!!"); err == nil {
		t.Fatal("NewCipher must reject a non-hex key")
	}
}

func TestNewCipher_WrongLength(t *testing.T) {
	tooShort := strings.Repeat("ab", 8) // 8 bytes, not 32
	if _, err := NewCipher(tooShort); err == nil {
		t.Fatal("NewCipher must reject a key that decodes to fewer than 32 bytes")
	}
	tooLong := strings.Repeat("ab", 40) // 40 bytes
	if _, err := NewCipher(tooLong); err == nil {
		t.Fatal("NewCipher must reject a key that decodes to more than 32 bytes")
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	c, err := NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plaintext := []byte(`{"api_key":"sk-super-secret-value"}`)

	ciphertext, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext must differ from plaintext")
	}

	got, err := c.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncrypt_NonDeterministic(t *testing.T) {
	// Two encryptions of the same plaintext must differ (random nonce per
	// call) -- otherwise identical secrets would produce identical
	// ciphertext, leaking equality information.
	c, err := NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plaintext := []byte("same secret both times")

	a, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt (1): %v", err)
	}
	b, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt (2): %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("encrypting the same plaintext twice must not produce identical ciphertext")
	}
}

func TestDecrypt_TamperedCiphertext_Fails(t *testing.T) {
	c, err := NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	ciphertext, err := c.Encrypt([]byte("a secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[len(tampered)-1] ^= 0xFF // flip the last byte

	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("Decrypt must fail on tampered ciphertext (GCM authentication)")
	}
}

func TestDecrypt_WrongKey_Fails(t *testing.T) {
	c1, err := NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher (1): %v", err)
	}
	otherKeyHex := strings.Repeat("bb", 32)
	c2, err := NewCipher(otherKeyHex)
	if err != nil {
		t.Fatalf("NewCipher (2): %v", err)
	}

	ciphertext, err := c1.Encrypt([]byte("a secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c2.Decrypt(ciphertext); err == nil {
		t.Fatal("Decrypt with the wrong key must fail")
	}
}

func TestDecrypt_TruncatedCiphertext_Fails(t *testing.T) {
	c, err := NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if _, err := c.Decrypt([]byte("short")); err == nil {
		t.Fatal("Decrypt must fail on a blob shorter than the nonce size")
	}
}
