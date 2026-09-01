package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func (s *Server) listReleases(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	f := service.ListReleasesFilter{
		ApplicationID: appID,
		Env:           domain.Environment(c.Query("env")),
		InstanceName:  c.Query("instance_name"),
		Status:        domain.ReleaseStatus(c.Query("status")),
		Limit:         queryInt(c, "limit", 20),
		Offset:        queryInt(c, "offset", 0),
	}
	releases, err := s.releaseSvc.List(c.Request.Context(), f)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": releases, "total": len(releases)})
}

func (s *Server) createRelease(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	var req service.CreateReleaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	rel, err := s.releaseSvc.Create(c.Request.Context(), appID, req)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Created(c, rel)
}

func (s *Server) getRelease(c *gin.Context) {
	id, ok := paramInt64(c, "releaseID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid release ID")
		return
	}
	rel, err := s.releaseSvc.Get(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	resp := gin.H{"release": rel}
	if rel.DeployLogID > 0 {
		if log, err := s.logSvc.GetWithSteps(c.Request.Context(), rel.DeployLogID); err == nil {
			resp["deploy_log"] = log
		}
	}
	Success(c, resp)
}

// deploy triggers the pipeline for a draft/failed release. Returns 202 with
// the created DeployLog (including the initial step list) immediately; the
// pipeline runs asynchronously in a goroutine owned by ReleaseApplication.
func (s *Server) deployRelease(c *gin.Context) {
	id, ok := paramInt64(c, "releaseID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid release ID")
		return
	}
	log, err := s.releaseApp.Deploy(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusAccepted, log)
}

type retryReleaseRequest struct {
	FromStep int `json:"from_step"`
}

func (s *Server) retryRelease(c *gin.Context) {
	id, ok := paramInt64(c, "releaseID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid release ID")
		return
	}
	var req retryReleaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	log, err := s.releaseApp.Retry(c.Request.Context(), id, req.FromStep)
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusAccepted, log)
}

func (s *Server) pauseRelease(c *gin.Context) {
	id, ok := paramInt64(c, "releaseID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid release ID")
		return
	}
	if err := s.releaseApp.Pause(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	Success(c, gin.H{"paused": true})
}

func (s *Server) resumeRelease(c *gin.Context) {
	id, ok := paramInt64(c, "releaseID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid release ID")
		return
	}
	log, err := s.releaseApp.Resume(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusAccepted, log)
}

func (s *Server) verifyRelease(c *gin.Context) {
	id, ok := paramInt64(c, "releaseID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid release ID")
		return
	}
	rel, err := s.releaseSvc.Verify(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	Success(c, rel)
}

type rollbackReleaseRequest struct {
	TargetReleaseID int64 `json:"target_release_id"`
}

func (s *Server) rollbackRelease(c *gin.Context) {
	id, ok := paramInt64(c, "releaseID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid release ID")
		return
	}
	var req rollbackReleaseRequest
	_ = c.ShouldBindJSON(&req)
	rel, err := s.releaseApp.Rollback(c.Request.Context(), id, req.TargetReleaseID, currentOperator(c))
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	Created(c, rel)
}
