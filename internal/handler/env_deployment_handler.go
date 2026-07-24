package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

type createEnvDeploymentRequest struct {
	Env       domain.Environment `json:"env"        binding:"required"`
	Branch    string             `json:"branch"`
	CommitSHA string             `json:"commit_sha"`
	Operator  string             `json:"operator"`
}

// createEnvDeployment kicks off a whole-environment rollout: one deploy record
// that fans out to every enabled instance of (app, env). Returns the batch with
// its freshly created child releases; their pipelines run asynchronously.
func (s *Server) createEnvDeployment(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	var req createEnvDeploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	batch, err := s.releaseApp.DeployEnv(c.Request.Context(), appID, req.Env, req.Branch, req.CommitSHA, req.Operator)
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusAccepted, batch)
}

func (s *Server) listEnvDeployments(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	batches, err := s.envDeploySvc.List(c.Request.Context(), service.ListEnvDeploymentsFilter{
		ApplicationID: appID,
		Env:           domain.Environment(c.Query("env")),
		Limit:         queryInt(c, "limit", 20),
		Offset:        queryInt(c, "offset", 0),
	})
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": batches, "total": len(batches)})
}

// getEnvDeployment returns the batch with its child per-instance releases —
// the "instance list" for one environment-wide deploy.
func (s *Server) getEnvDeployment(c *gin.Context) {
	id, ok := paramInt64(c, "deploymentID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid deployment ID")
		return
	}
	batch, err := s.envDeploySvc.Get(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, batch)
}
