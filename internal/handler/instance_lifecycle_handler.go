package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// decommissionInstance drains the instance's gateway traffic and tears its
// containers down, recording DesiredState=stopped. Returns 202 with the teardown
// DeployLog (its steps run asynchronously); poll GET /deploy-logs/:logID for
// progress, exactly like a deploy.
func (s *Server) decommissionInstance(c *gin.Context) {
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
	log, err := s.instanceLifecycleApp.Decommission(c.Request.Context(), appID, targetID)
	if err != nil {
		// A concurrent deploy/decommission of the same instance holds the lock;
		// surface that as a conflict rather than a server error.
		if errors.Is(err, service.ErrDeployLocked) {
			FailWith(c, http.StatusConflict, err)
			return
		}
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusAccepted, log)
}

// recommissionInstance clears the stopped intent so the instance rejoins the
// deploy set and health monitoring. It does not start containers — the operator
// triggers a normal deploy to bring the instance back.
func (s *Server) recommissionInstance(c *gin.Context) {
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
	target, err := s.instanceLifecycleApp.Recommission(c.Request.Context(), appID, targetID)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, target)
}
