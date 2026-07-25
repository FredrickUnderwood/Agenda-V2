package node

import (
	"bytes"
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// Server is agenda-node's management API (jobs + proxy registration + health).
// Commands execute locally via runner.New(nil) — the node reuses the exact same
// localRunner the control plane uses, so it never reimplements command running.
type Server struct {
	token       string
	jobs        *JobStore
	registry    *ProxyRegistry
	local       runner.Runner
	engine      *gin.Engine
	started     time.Time
	backendHost string
}

func NewServer(token string, jobs *JobStore, registry *ProxyRegistry, backendHost string) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		token:       token,
		jobs:        jobs,
		registry:    registry,
		local:       runner.New(nil), // nil machine → localRunner
		engine:      gin.New(),
		started:     time.Now(),
		backendHost: backendHost,
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
		v1.GET("/logs/:app/:instance", s.getLogs)
		v1.GET("/metrics/:app/:instance", s.getMetrics)
		v1.GET("/probe/:app/:instance", s.probe)
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

// defaultLogTailLines / maxLogTailLines bound the ?tail= query so a client
// can't force the node to scan an unbounded number of lines per file.
const (
	defaultLogTailLines = 200
	maxLogTailLines     = 5000
)

// getLogs reads log files from the given host-side directory (the "dir"
// query param — an absolute host path the control plane resolves as the
// instance's runtime log dir, git.InstanceLogDir →
// <root>/run/<app>/<env>/<instance>/logs, or the legacy
// <root>/<host>/<repo>/<branch>/logs for instances deployed before that
// layout; NOT contract.AgendaContainerLogDir, which is only the in-container
// path a deployed app's own container sees, not the host path this process
// runs on).
func (s *Server) getLogs(c *gin.Context) {
	app := c.Param("app")
	instance := c.Param("instance")
	dir := c.Query("dir")
	if dir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dir is required"})
		return
	}

	tail := defaultLogTailLines
	if v := c.Query("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}
	if tail > maxLogTailLines {
		tail = maxLogTailLines
	}
	service := c.Query("service")

	files, err := findLogFiles(dir, app, instance)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no logs found for " + app + "/" + instance})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logs := make([]contract.NodeLogFile, 0, len(files))
	for _, name := range files {
		svc := serviceNameFromFile(name, app, instance)
		if service != "" && svc != service {
			continue
		}
		lines, err := tailLines(filepath.Join(dir, name), tail)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		logs = append(logs, contract.NodeLogFile{Service: svc, File: name, Lines: lines})
	}
	if len(logs) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no logs found for " + app + "/" + instance})
		return
	}
	c.JSON(http.StatusOK, contract.NodeLogsResponse{App: app, Instance: instance, Logs: logs})
}

// getMetrics relays app/instance's own /metrics response (sdk/go/metric,
// listening on the given local port) back to the caller verbatim. app/
// instance aren't used for routing (port is self-sufficient, unlike getLogs'
// filename-glob lookup) — kept for parity with the logs route and so the
// call is legible in access logs.
func (s *Server) getMetrics(c *gin.Context) {
	portStr := c.Query(contract.NodeMetricsQueryPort)
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "port must be a valid TCP port"})
		return
	}
	path := c.Query(contract.NodeMetricsQueryPath)
	if path == "" {
		path = contract.DefaultMetricsPath
	}
	if !strings.HasPrefix(path, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path must start with /"})
		return
	}
	scheme := c.Query(contract.NodeMetricsQueryScheme)

	body, contentType, err := fetchLocalMetrics(c.Request.Context(), s.backendHost, scheme, port, path)
	if err != nil {
		c.String(http.StatusBadGateway, "%s", err.Error())
		return
	}
	c.Data(http.StatusOK, contentType, body)
}

// probe relays a health check to app/instance's own endpoint, listening on the
// given local port, and returns the upstream status/latency/error to the
// control plane. Like getMetrics, app/instance aren't used for routing (port
// is self-sufficient) — kept for parity and legibility in access logs. The
// node never decides healthy/unhealthy: a reachable app always yields HTTP 200
// from this endpoint with the app's real status in the body, so the control
// plane can apply its own expected-status rule.
func (s *Server) probe(c *gin.Context) {
	portStr := c.Query(contract.NodeProbeQueryPort)
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "port must be a valid TCP port"})
		return
	}
	path := c.Query(contract.NodeProbeQueryPath)
	if path != "" && !strings.HasPrefix(path, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path must start with /"})
		return
	}
	scheme := c.Query(contract.NodeProbeQueryScheme)
	method := c.Query(contract.NodeProbeQueryMethod)

	var timeout time.Duration
	if v := c.Query(contract.NodeProbeQueryTimeoutMS); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	status, latencyMS, probeErr := probeLocal(c.Request.Context(), s.backendHost, scheme, method, path, port, timeout)
	resp := contract.NodeProbeResponse{HTTPStatus: status, LatencyMS: latencyMS}
	if probeErr != nil {
		resp.Error = probeErr.Error()
	}
	c.JSON(http.StatusOK, resp)
}
