package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/auth"
)

// requireAdmin gates a route behind the admin role. When authentication is not
// configured (dev mode), it allows the request through — matching the API's
// "no auth configured → open" behavior so a fresh install is usable.
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.auth.Enabled() {
			c.Next()
			return
		}
		id, ok := auth.GetIdentity(c)
		if !ok || !id.Has(auth.PermAll) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin privileges required"})
			return
		}
		c.Next()
	}
}

// observabilityScrapeTokenSettingKey holds the shared bearer token Prometheus
// presents to /api/v1/observability/*. Unlike requireAdmin, this gate fails
// closed when unconfigured: these endpoints expose real app metrics + app/
// env/instance topology (a bigger disclosure surface than the gateway's
// aggregate-only /-/metrics), so "open until an operator sets a token" is not
// an acceptable default — Prometheus (not a browser) is the only caller.
const observabilityScrapeTokenSettingKey = "observability.scrape_token"

// requireScrapeToken gates the Prometheus-facing observability endpoints
// behind a static bearer token stored as a Setting, checked with the same
// constant-time comparison agenda-node's own tokenAuth uses for agent_token.
func (s *Server) requireScrapeToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := s.settingSvc.Get(observabilityScrapeTokenSettingKey)
		if !ok || token == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": observabilityScrapeTokenSettingKey + " is not configured"})
			return
		}
		got := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid scrape token"})
			return
		}
		c.Next()
	}
}
