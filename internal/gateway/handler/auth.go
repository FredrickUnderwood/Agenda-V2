package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	alog "github.com/FredrickUnderwood/agenda-go-sdk/log"
	coreauth "github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	AuthKindUser    = "user"
	AuthKindService = "service"

	HeaderServiceToken = "X-Service-Token"

	ctxAuthKind  = "gateway_auth_kind"
	ctxAuthID    = "gateway_auth_id"
	ctxAuthPerms = "gateway_auth_perms"
)

type serviceTokenEntry struct {
	name  string
	token string
	perms map[string]bool
}

func buildServiceTokens(cfgs []config.ServiceTokenConfig) []serviceTokenEntry {
	out := make([]serviceTokenEntry, 0, len(cfgs))
	for _, cfg := range cfgs {
		if cfg.Name == "" || cfg.Token == "" {
			continue
		}
		perms := make(map[string]bool, len(cfg.Perms))
		for _, perm := range cfg.Perms {
			perms[perm] = true
		}
		out = append(out, serviceTokenEntry{name: cfg.Name, token: cfg.Token, perms: perms})
	}
	return out
}

// matchServiceToken finds the token entry whose secret matches token, using a
// constant-time comparison. Shared by authMiddleware (admin API) and the
// data-plane proxy path (instance-pin gating), which cannot use
// authMiddleware directly since it also serves unauthenticated public traffic.
func matchServiceToken(tokens []serviceTokenEntry, token string) (serviceTokenEntry, bool) {
	for _, item := range tokens {
		if subtle.ConstantTimeCompare([]byte(item.token), []byte(token)) == 1 {
			return item, true
		}
	}
	return serviceTokenEntry{}, false
}

// authMiddleware authenticates admin-API requests. A request presenting a valid
// X-Service-Token is authenticated as a service principal with that token's
// perms; otherwise the request must carry a user JWT minted by the control
// plane's built-in auth, verified locally against the shared secret.
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if token := c.GetHeader(HeaderServiceToken); token != "" {
			if item, ok := matchServiceToken(s.serviceTokens, token); ok {
				c.Set(ctxAuthKind, AuthKindService)
				c.Set(ctxAuthID, item.name)
				c.Set(ctxAuthPerms, item.perms)
				c.Next()
				return
			}
			alog.L().Warn("invalid service token presented")
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorBody{
				Code:    401,
				Message: "invalid service token",
			}})
			return
		}

		tok := bearerToken(c)
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorBody{
				Code:    401,
				Message: "missing credentials",
			}})
			return
		}
		id, err := s.authMgr.Verify(tok)
		if err != nil {
			alog.L().Warn("invalid user token presented", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorBody{
				Code:    401,
				Message: "invalid token",
			}})
			return
		}
		c.Set(ctxAuthKind, AuthKindUser)
		c.Set(ctxAuthID, id.Username)
		c.Set(ctxAuthPerms, permSet(id.Perms))
		c.Next()
	}
}

func requirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, ok := c.Get(ctxAuthPerms); ok {
			if m, _ := v.(map[string]bool); m[perm] || m[coreauth.PermAll] {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, ErrorResponse{Error: ErrorBody{
			Code:    403,
			Message: "missing permission: " + perm,
		}})
	}
}

func operator(c *gin.Context) string {
	if v, ok := c.Get(ctxAuthID); ok {
		if id, _ := v.(string); id != "" {
			return id
		}
	}
	return "unknown"
}

func logCaller(c *gin.Context, action string) {
	alog.L().Info(action,
		zap.String("auth_kind", c.GetString(ctxAuthKind)),
		zap.String("operator", operator(c)),
	)
}

func permSet(perms []string) map[string]bool {
	m := make(map[string]bool, len(perms))
	for _, p := range perms {
		m[p] = true
	}
	return m
}

func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if after, found := strings.CutPrefix(h, "Bearer "); found {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(h)
}
