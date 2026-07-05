package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func bearerAuth(token string) gin.HandlerFunc {
	expected := "Bearer " + token
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != expected {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
