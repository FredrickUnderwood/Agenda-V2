package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func (s *Server) getApplicationEnvironment(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	env := domain.Environment(c.Param("env"))
	row, err := s.envSvc.Get(c.Request.Context(), appID, env)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, row)
}

func (s *Server) updateApplicationEnvironment(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	env := domain.Environment(c.Param("env"))
	var req service.UpsertApplicationEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	row, err := s.envSvc.Upsert(c.Request.Context(), appID, env, req)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, row)
}
