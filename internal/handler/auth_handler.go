package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// login authenticates a user and returns a signed JWT. Public (no auth).
func (s *Server) login(c *gin.Context) {
	if !s.auth.CanIssue() {
		FailMessage(c, http.StatusServiceUnavailable, "login is disabled: set auth.jwt_secret in config")
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.userSvc.Authenticate(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			FailMessage(c, http.StatusUnauthorized, "invalid username or password")
			return
		}
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	token, err := s.auth.Issue(u.ID, u.Username, auth.Role(u.Role))
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"token": token, "user": u})
}

// me returns the authenticated principal.
func (s *Server) me(c *gin.Context) {
	id, ok := auth.GetIdentity(c)
	if !ok {
		FailMessage(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	// A static service-token principal has no DB user record.
	if id.Service {
		Success(c, gin.H{"username": id.Username, "role": id.Role, "service": true})
		return
	}
	u, err := s.userSvc.GetByID(c.Request.Context(), id.UserID)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, u)
}
