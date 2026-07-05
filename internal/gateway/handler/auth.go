package handler

import (
	"crypto/subtle"
	"net/http"

	alog "github.com/FredrickUnderwood/agenda-go-sdk/log"
	usercore "github.com/FredrickUnderwood/user-core-go-sdk"
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

func (s *Server) authMiddleware() gin.HandlerFunc {
	ucMW := s.uc.Middleware()
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
		c.Set(ctxAuthKind, AuthKindUser)
		ucMW(c)
	}
}

func requirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, ok := c.Get(ctxAuthPerms); ok {
			if m, _ := v.(map[string]bool); m[perm] {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden, ErrorResponse{Error: ErrorBody{
				Code:    403,
				Message: "service token lacks perm: " + perm,
			}})
			return
		}
		if usercore.HasPerm(c, perm) {
			c.Next()
			return
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
	if id := usercore.UID(c); id != "" {
		return id
	}
	return "unknown"
}

func logCaller(c *gin.Context, action string) {
	alog.L().Info(action,
		zap.String("auth_kind", c.GetString(ctxAuthKind)),
		zap.String("operator", operator(c)),
	)
}
