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

// getApplicationEnvironments returns every environment's env var map in one
// response, so the console can render the Key × (prod, stage, test) matrix
// without a request per environment.
func (s *Server) getApplicationEnvironments(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	out, err := s.envSvc.GetAll(c.Request.Context(), appID)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, out)
}

// updateApplicationEnvironments replaces the env var map of every environment
// named in the payload, in one call — a per-environment PUT would leave the
// matrix half-saved if a later request failed.
func (s *Server) updateApplicationEnvironments(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	var req service.UpsertApplicationEnvironmentsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.envSvc.UpsertAll(c.Request.Context(), appID, req)
	if err != nil {
		// Validation rejects bad keys before anything is written, so surface
		// those as 400 rather than a generic server error.
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	Success(c, out)
}
