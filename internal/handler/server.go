package handler

import (
	"context"
	"net/http"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/application"
	"github.com/agenda-v2/internal/logger"
	"github.com/agenda-v2/internal/service"
)

type Server struct {
	cfg        *config.Config
	engine     *gin.Engine
	appSvc     *service.ApplicationService
	healthSvc  *service.ApplicationHealthService
	envSvc     *service.ApplicationEnvironmentService
	releaseSvc *service.ApplicationReleaseService
	machineSvc *service.MachineService
	logSvc     *service.DeployLogService
	releaseApp *application.ReleaseApplication
	httpServer *http.Server
}

func NewServer(
	cfg *config.Config,
	appSvc *service.ApplicationService,
	healthSvc *service.ApplicationHealthService,
	envSvc *service.ApplicationEnvironmentService,
	releaseSvc *service.ApplicationReleaseService,
	machineSvc *service.MachineService,
	logSvc *service.DeployLogService,
	releaseApp *application.ReleaseApplication,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		cfg: cfg, engine: gin.New(),
		appSvc: appSvc, healthSvc: healthSvc, envSvc: envSvc, releaseSvc: releaseSvc,
		machineSvc: machineSvc, logSvc: logSvc, releaseApp: releaseApp,
	}
	s.engine.Use(ginzap.Ginzap(logger.L(), time.RFC3339, true))
	s.engine.Use(ginzap.RecoveryWithZap(logger.L(), true))
	s.registerRoutes()

	s.httpServer = &http.Server{
		Addr:    cfg.Server.Addr,
		Handler: s.engine,
	}
	return s
}

func (s *Server) registerRoutes() {
	s.engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	v1 := s.engine.Group("/api/v1")
	if s.cfg.Server.AuthToken != "" {
		v1.Use(bearerAuth(s.cfg.Server.AuthToken))
	}

	v1.GET("/config", s.getConfig)

	apps := v1.Group("/applications")
	{
		apps.GET("", s.listApplications)
		apps.POST("", s.createApplication)
		apps.GET("/:appID", s.getApplication)
		apps.PUT("/:appID", s.updateApplication)
		apps.DELETE("/:appID", s.deleteApplication)
		apps.GET("/:appID/instances", s.listApplicationInstances)
		apps.GET("/:appID/instances/:targetID/health", s.getApplicationInstanceHealth)
		apps.POST("/:appID/instances/:targetID/health/check", s.checkApplicationInstanceHealth)
		apps.GET("/:appID/environments/:env", s.getApplicationEnvironment)
		apps.PUT("/:appID/environments/:env", s.updateApplicationEnvironment)
		apps.GET("/:appID/releases", s.listReleases)
		apps.POST("/:appID/releases", s.createRelease)
	}

	releases := v1.Group("/releases")
	{
		releases.GET("/:releaseID", s.getRelease)
		releases.POST("/:releaseID/deploy", s.deployRelease)
		releases.POST("/:releaseID/retry", s.retryRelease)
		releases.POST("/:releaseID/pause", s.pauseRelease)
		releases.POST("/:releaseID/resume", s.resumeRelease)
		releases.POST("/:releaseID/verify", s.verifyRelease)
		releases.POST("/:releaseID/rollback", s.rollbackRelease)
	}

	v1.GET("/deploy-logs/:logID", s.getLog)

	machines := v1.Group("/machines")
	{
		machines.GET("", s.listMachines)
		machines.POST("", s.createMachine)
		machines.GET("/:machineID", s.getMachine)
		machines.PUT("/:machineID", s.updateMachine)
		machines.DELETE("/:machineID", s.deleteMachine)
		machines.POST("/:machineID/test", s.testMachineConnection)
	}
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
