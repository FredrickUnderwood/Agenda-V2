package node

import (
	"bytes"
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// Server is agenda-node's management API (jobs + proxy registration + health).
// Commands execute locally via runner.New(nil) — the node reuses the exact same
// localRunner the control plane uses, so it never reimplements command running.
type Server struct {
	token    string
	jobs     *JobStore
	registry *ProxyRegistry
	local    runner.Runner
	engine   *gin.Engine
	started  time.Time
}

func NewServer(token string, jobs *JobStore, registry *ProxyRegistry) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		token:    token,
		jobs:     jobs,
		registry: registry,
		local:    runner.New(nil), // nil machine → localRunner
		engine:   gin.New(),
		started:  time.Now(),
	}
	s.engine.Use(gin.Recovery())
	s.registerRoutes()
	return s
}

// Handler exposes the gin engine (for httptest and for embedding).
func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) registerRoutes() {
	// Health is unauthenticated (local docker/systemd probes only).
	s.engine.GET("/v1/health", s.health)

	v1 := s.engine.Group("/v1")
	v1.Use(s.tokenAuth())
	{
		v1.POST("/jobs", s.dispatchJob)
		v1.GET("/jobs/:job_id", s.getJob)
		v1.DELETE("/jobs/:job_id", s.deleteJob)
		v1.PUT("/proxy/:instance", s.registerProxy)
	}
}

func (s *Server) tokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := c.GetHeader(contract.HeaderNodeToken)
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid node token"})
			return
		}
		c.Next()
	}
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":    Version,
		"uptime_sec": int(time.Since(s.started).Seconds()),
	})
}

func (s *Server) dispatchJob(c *gin.Context) {
	var req contract.NodeJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.JobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}
	run := s.runnerFor(req)
	if run == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job mode: " + req.Mode})
		return
	}
	s.jobs.Dispatch(req.JobID, run)
	c.JSON(http.StatusAccepted, gin.H{"job_id": req.JobID})
}

// runnerFor builds the closure the JobStore executes, translating the wire
// request into the appropriate runner call. Returns nil for an unknown mode.
func (s *Server) runnerFor(req contract.NodeJobRequest) func(ctx context.Context, buf *bytes.Buffer) error {
	switch req.Mode {
	case contract.NodeJobModeCmd:
		return func(ctx context.Context, buf *bytes.Buffer) error {
			if len(req.Env) > 0 {
				return s.local.RunCmdEnv(ctx, req.Dir, req.Env, req.Name, req.Args, buf)
			}
			return s.local.RunCmd(ctx, req.Dir, req.Name, req.Args, buf)
		}
	case contract.NodeJobModeShell:
		return func(ctx context.Context, buf *bytes.Buffer) error {
			return s.local.RunShell(ctx, req.Dir, req.Shell, buf)
		}
	default:
		return nil
	}
}

func (s *Server) getJob(c *gin.Context) {
	st, ok := s.jobs.Get(c.Param("job_id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) deleteJob(c *gin.Context) {
	s.jobs.Delete(c.Param("job_id"))
	c.Status(http.StatusNoContent)
}

func (s *Server) registerProxy(c *gin.Context) {
	instance := c.Param("instance")
	var req contract.NodeProxyRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Port <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "port must be > 0"})
		return
	}
	s.registry.Set(instance, req.Port)
	c.JSON(http.StatusOK, gin.H{"instance": instance, "port": req.Port})
}
