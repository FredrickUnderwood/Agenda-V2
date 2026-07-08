package secret

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateToken returns a fresh 32-byte random token, hex-encoded (64 chars) —
// the same strength as `openssl rand -hex 32`, generated server-side so
// callers never have to hand-roll one.
func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
