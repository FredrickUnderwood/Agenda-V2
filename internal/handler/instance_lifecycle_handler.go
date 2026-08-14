package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/application"
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

// deleteInstance permanently removes a decommissioned instance's record. Unlike
// decommission it is synchronous — there is nothing to run on a machine, only
// rows to remove — so it answers 204 rather than 202 with a DeployLog.
func (s *Server) deleteInstance(c *gin.Context) {
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
	err := s.instanceLifecycleApp.DeleteInstance(c.Request.Context(), appID, targetID)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, application.ErrInstanceNotStopped):
		// The instance is still running: a precondition the caller can fix by
		// decommissioning first, not a server fault.
		FailWith(c, http.StatusConflict, err)
	case errors.Is(err, service.ErrDeployLocked):
		FailWith(c, http.StatusConflict, err)
	default:
		FailWith(c, http.StatusInternalServerError, err)
	}
}
