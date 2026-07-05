package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func (s *Server) listApplications(c *gin.Context) {
	apps, err := s.appSvc.List(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": apps, "total": len(apps)})
}

func (s *Server) createApplication(c *gin.Context) {
	var req service.CreateApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	app, err := s.appSvc.Create(c.Request.Context(), req)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Created(c, app)
}

func (s *Server) getApplication(c *gin.Context) {
	id, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	app, err := s.appSvc.Get(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, app)
}

func (s *Server) updateApplication(c *gin.Context) {
	id, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	var req service.UpdateApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	app, err := s.appSvc.Update(c.Request.Context(), id, req)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, app)
}

func (s *Server) deleteApplication(c *gin.Context) {
	id, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	if err := s.appSvc.Delete(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	NoContent(c)
}

func (s *Server) listApplicationInstances(c *gin.Context) {
	id, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	targets, err := s.appSvc.ListTargetsByApplication(c.Request.Context(), id, domain.Environment(c.Query("env")))
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": targets, "total": len(targets)})
}

func (s *Server) getApplicationInstanceHealth(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	targetID, ok := paramInt64(c, "targetID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid instance ID")
		return
	}
	health, err := s.healthSvc.GetTargetHealthForApplication(c.Request.Context(), appID, targetID)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	if health == nil {
		Success(c, gin.H{"status": "unknown"})
		return
	}
	Success(c, health)
}

func (s *Server) checkApplicationInstanceHealth(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	targetID, ok := paramInt64(c, "targetID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid instance ID")
		return
	}
	health, err := s.healthSvc.CheckTargetForApplication(c.Request.Context(), appID, targetID)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, health)
}
