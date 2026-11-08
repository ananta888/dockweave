// Package credentialvault implements an encrypted, local credential store.
//
// The design follows the MIT-licensed crypto core of Infisical Agent Vault
// (internal/crypto: AES-256-GCM + Argon2id) at upstream commit
// 16ec8d5a5db38be9d2c77fe9c19c7fd7125246ba, with two hardening changes:
//
//   - each ciphertext is bound to its credential name via AEAD additional
//     data, so values cannot be swapped between entries;
//   - the on-disk envelope is versioned and every write is atomic (0600).
//
// The passphrase never leaves process memory, is never stored, and is
// zeroed on close. See docs/credential-broker.md for the threat model.
package credentialvault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// KDFParams are the Argon2id parameters stored in the vault envelope so
// future releases can raise them without breaking existing vaults.
type KDFParams struct {
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"` // KiB
	Threads uint8  `json:"threads"`
	KeyLen  uint32 `json:"key_len"`
}

// DefaultKDFParams returns conservative interactive-login parameters
// (OWASP-style: 64 MiB, 3 iterations, 4 lanes).
func DefaultKDFParams() KDFParams {
	return KDFParams{Time: 3, Memory: 64 * 1024, Threads: 4, KeyLen: 32}
}

func (p KDFParams) validate() error {
	if p.Time < 1 || p.Memory < 8*1024 || p.Threads < 1 || p.KeyLen != 32 {
		return fmt.Errorf("invalid KDF parameters (time=%d memory=%d threads=%d key_len=%d)",
			p.Time, p.Memory, p.Threads, p.KeyLen)
	}
	return nil
}

// deriveKey derives a 32-byte key from passphrase and salt.
func deriveKey(passphrase, salt []byte, p KDFParams) []byte {
	return argon2.IDKey(passphrase, salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}

// randomBytes returns n cryptographically random bytes.
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("read random: %w", err)
	}
	return b, nil
}

// seal encrypts plaintext with AES-256-GCM using a random nonce.
// aad is authenticated but not encrypted.
func seal(plaintext, key, aad []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("gcm: %w", err)
	}
	nonce, err = randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, aad)
	return nonce, ciphertext, nil
}

// open decrypts ciphertext with AES-256-GCM and verifies aad.
func open(ciphertext, nonce, key, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce length %d", len(nonce))
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt (wrong passphrase or tampered data): %w", err)
	}
	return plaintext, nil
}

// wipe zeroes a byte slice on a best-effort basis. Go may have copied
// data before this call; callers should avoid converting secrets to
// immutable strings where possible.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
