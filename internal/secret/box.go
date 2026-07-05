// Package secret provides authenticated encryption for values stored at rest
// (e.g. secret Settings). A Box is derived from the configured master key and
// uses AES-256-GCM. When no master key is configured the Box is disabled and
// passes values through unchanged — callers are expected to warn in that case.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// encPrefix marks an encrypted blob so decrypt can distinguish it from a value
// that was stored in plaintext (no master key at the time it was written). The
// v1 tag leaves room to rotate the scheme later.
const encPrefix = "enc:v1:"

// Box encrypts and decrypts secret values with a key derived from the master key.
type Box struct {
	key     []byte
	enabled bool
}

// NewBox derives a 32-byte AES key from masterKey (SHA-256). An empty master key
// yields a disabled Box that passes plaintext through unchanged.
func NewBox(masterKey string) *Box {
	mk := strings.TrimSpace(masterKey)
	if mk == "" {
		return &Box{enabled: false}
	}
	sum := sha256.Sum256([]byte(mk))
	return &Box{key: sum[:], enabled: true}
}

// Enabled reports whether a master key was configured.
func (b *Box) Enabled() bool { return b != nil && b.enabled }

// Encrypt returns an "enc:v1:<base64>" blob. With a disabled Box it returns the
// plaintext unchanged (the caller should have warned).
func (b *Box) Encrypt(plaintext string) (string, error) {
	if !b.Enabled() {
		return plaintext, nil
	}
	gcm, err := b.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. A value without the enc prefix is returned as-is
// (it was stored in plaintext). An encrypted value with no key configured is an
// error rather than a silent leak of ciphertext.
func (b *Box) Decrypt(stored string) (string, error) {
	if !IsEncrypted(stored) {
		return stored, nil
	}
	if !b.Enabled() {
		return "", errors.New("value is encrypted but no security.master_key is configured")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", err
	}
	gcm, err := b.gcm()
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (b *Box) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// IsEncrypted reports whether s was produced by Encrypt with a key.
func IsEncrypted(s string) bool { return strings.HasPrefix(s, encPrefix) }
