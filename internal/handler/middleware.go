package handler

import (
	"net/http"

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
