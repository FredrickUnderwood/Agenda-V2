package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func (s *Server) listUsers(c *gin.Context) {
	users, err := s.userSvc.List(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": users, "total": len(users)})
}

func (s *Server) createUser(c *gin.Context) {
	var req service.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.userSvc.Create(c.Request.Context(), req)
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	Created(c, u)
}

func (s *Server) deleteUser(c *gin.Context) {
	id, ok := paramInt64(c, "userID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid user ID")
		return
	}
	if err := s.userSvc.Delete(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	NoContent(c)
}
