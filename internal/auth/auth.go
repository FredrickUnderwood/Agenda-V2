// Package auth is the built-in identity layer shared by the control plane and
// (later) the gateway, replacing the external user-core dependency. Tokens are
// HMAC-signed JWTs verified locally against a shared secret, so a service can
// authenticate a user without calling back to a central identity server.
package auth

import (
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Role is a coarse permission bucket. Fine-grained perms are derived from it via
// Perms and embedded in the token, so a verifier never needs a role table.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// PermAll is the wildcard perm held by admins; it satisfies any Has check.
const PermAll = "*"

// Perms returns the permission set granted to a role. Admins get the wildcard;
// members currently get read access and must be granted writes explicitly by a
// route's RequirePerm gate (which admins pass via the wildcard).
func Perms(role Role) []string {
	switch role {
	case RoleAdmin:
		return []string{PermAll}
	default:
		return []string{}
	}
}

// Identity is the authenticated principal attached to a request.
type Identity struct {
	UserID   int64    `json:"user_id"`
	Username string   `json:"username"`
	Role     Role     `json:"role"`
	Perms    []string `json:"perms"`
	// Service is true when the caller authenticated with the static service
	// token rather than a user JWT (bootstrap / service-to-service).
	Service bool `json:"service"`
}

// Has reports whether the identity holds perm (or the wildcard).
func (id Identity) Has(perm string) bool {
	for _, p := range id.Perms {
		if p == PermAll || p == perm {
			return true
		}
	}
	return false
}

// Claims is the JWT payload.
type Claims struct {
	jwt.RegisteredClaims
	Username string   `json:"username"`
	Role     string   `json:"role"`
	Perms    []string `json:"perms"`
}

// Manager issues and verifies tokens and provides the gin middleware. It is
// safe for concurrent use.
type Manager struct {
	secret      []byte
	staticToken string
	ttl         time.Duration
}

const defaultTTL = 24 * time.Hour

// NewManager builds a Manager. secret signs/verifies user JWTs; staticToken, if
// non-empty, is accepted as an admin service principal (backward compatible with
// the old server.auth_token). A zero ttl defaults to 24h.
func NewManager(secret, staticToken string, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &Manager{secret: []byte(secret), staticToken: staticToken, ttl: ttl}
}

// Enabled reports whether any authentication is configured. When false the
// server leaves the API open (dev mode) — mirroring the previous behavior of an
// empty auth_token.
func (m *Manager) Enabled() bool {
	return m != nil && (len(m.secret) > 0 || m.staticToken != "")
}

// CanIssue reports whether a signing secret is configured (login is possible).
func (m *Manager) CanIssue() bool {
	return m != nil && len(m.secret) > 0
}

// Issue mints a signed token for a user.
func (m *Manager) Issue(userID int64, username string, role Role) (string, error) {
	if len(m.secret) == 0 {
		return "", errors.New("auth.jwt_secret is not configured; cannot issue tokens")
	}
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
		Username: username,
		Role:     string(role),
		Perms:    Perms(role),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Verify parses and validates a user JWT into an Identity.
func (m *Manager) Verify(tokenStr string) (Identity, error) {
	if len(m.secret) == 0 {
		return Identity{}, errors.New("auth.jwt_secret is not configured")
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return Identity{}, err
	}
	if !token.Valid {
		return Identity{}, errors.New("invalid token")
	}
	uid, _ := strconv.ParseInt(claims.Subject, 10, 64)
	return Identity{
		UserID:   uid,
		Username: claims.Username,
		Role:     Role(claims.Role),
		Perms:    claims.Perms,
	}, nil
}

const ctxIdentity = "auth_identity"

// Middleware authenticates each request via the static service token or a user
// JWT, aborting 401 on failure. Downstream handlers read the principal with
// GetIdentity; the numeric user id is also exposed under the "user_id" context
// key for callers that only need the id.
func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := bearerToken(c)
		if tok == "" {
			abort401(c, "missing bearer token")
			return
		}
		if m.staticToken != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(m.staticToken)) == 1 {
			setIdentity(c, Identity{Username: "service", Role: RoleAdmin, Perms: []string{PermAll}, Service: true})
			c.Next()
			return
		}
		id, err := m.Verify(tok)
		if err != nil {
			abort401(c, "invalid token")
			return
		}
		setIdentity(c, id)
		c.Next()
	}
}

// RequirePerm gates a route behind a permission.
func RequirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := GetIdentity(c)
		if !ok || !id.Has(perm) {
			abort403(c, "missing permission: "+perm)
			return
		}
		c.Next()
	}
}

// RequireAdmin gates a route behind admin (wildcard perm).
func RequireAdmin() gin.HandlerFunc {
	return RequirePerm(PermAll)
}

// GetIdentity returns the authenticated principal, if any.
func GetIdentity(c *gin.Context) (Identity, bool) {
	v, ok := c.Get(ctxIdentity)
	if !ok {
		return Identity{}, false
	}
	id, ok := v.(Identity)
	return id, ok
}

func setIdentity(c *gin.Context, id Identity) {
	c.Set(ctxIdentity, id)
	c.Set("user_id", id.UserID)
}

func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if h == "" {
		return ""
	}
	if after, found := strings.CutPrefix(h, "Bearer "); found {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(h)
}

func abort401(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(401, gin.H{"error": msg})
}

func abort403(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(403, gin.H{"error": msg})
}
