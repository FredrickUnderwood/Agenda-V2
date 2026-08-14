package handler

import (
	"context"
	"net/http"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/application"
	"github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

type Server struct {
	cfg                  *config.Config
	engine               *gin.Engine
	appSvc               *service.ApplicationService
	healthSvc            *service.ApplicationHealthService
	envSvc               *service.ApplicationEnvironmentService
	releaseSvc           *service.ApplicationReleaseService
	envDeploySvc         *service.EnvDeploymentService
	machineSvc           *service.MachineService
	logSvc               *service.DeployLogService
	appLogSvc            *service.ApplicationLogService
	appMetricsSvc        *service.ApplicationMetricsService
	settingSvc           *service.SettingService
	alertSvc             *service.AlertService
	alertRuleSvc         *service.AlertRuleService
	notificationSvc      *service.NotificationService
	userSvc              *service.UserService
	auth                 *auth.Manager
	releaseApp           *application.ReleaseApplication
	instanceLifecycleApp *application.InstanceLifecycleApplication
	httpServer           *http.Server
}

func NewServer(
	cfg *config.Config,
	appSvc *service.ApplicationService,
	healthSvc *service.ApplicationHealthService,
	envSvc *service.ApplicationEnvironmentService,
	releaseSvc *service.ApplicationReleaseService,
	envDeploySvc *service.EnvDeploymentService,
	machineSvc *service.MachineService,
	logSvc *service.DeployLogService,
	appLogSvc *service.ApplicationLogService,
	appMetricsSvc *service.ApplicationMetricsService,
	settingSvc *service.SettingService,
	alertSvc *service.AlertService,
	alertRuleSvc *service.AlertRuleService,
	notificationSvc *service.NotificationService,
	userSvc *service.UserService,
	authMgr *auth.Manager,
	releaseApp *application.ReleaseApplication,
	instanceLifecycleApp *application.InstanceLifecycleApplication,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{
		cfg: cfg, engine: gin.New(),
		appSvc: appSvc, healthSvc: healthSvc, envSvc: envSvc, releaseSvc: releaseSvc, envDeploySvc: envDeploySvc,
		machineSvc: machineSvc, logSvc: logSvc, appLogSvc: appLogSvc, appMetricsSvc: appMetricsSvc, settingSvc: settingSvc,
		alertSvc: alertSvc, alertRuleSvc: alertRuleSvc, notificationSvc: notificationSvc, userSvc: userSvc, auth: authMgr, releaseApp: releaseApp,
		instanceLifecycleApp: instanceLifecycleApp,
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

	// Public: login issues a token, so it must precede the auth middleware.
	v1.POST("/auth/login", s.login)
	// Node heartbeat authenticates with the per-machine node token (checked in
	// the handler), not the admin bearer, so it lives before the auth middleware.
	v1.POST("/machines/:machineID/heartbeat", s.machineHeartbeat)

	// Prometheus is the caller here, not a user session — gated by its own
	// static scrape-token middleware instead of the user auth middleware below.
	obs := v1.Group("/observability")
	obs.Use(s.requireScrapeToken())
	{
		obs.GET("/scrape-targets", s.listScrapeTargets)
		obs.GET("/app-metrics", s.scrapeAppMetrics)
	}

	// Everything registered after this requires authentication when auth is
	// configured; with neither jwt_secret nor auth_token set the API stays open
	// (dev mode).
	if s.auth.Enabled() {
		v1.Use(s.auth.Middleware())
	}

	v1.GET("/auth/me", s.me)
	v1.GET("/config", s.getConfig)

	// Notifications are the shared in-app inbox — any authenticated
	// user can read/dismiss them, unlike Settings/Alerts which hold secrets or
	// can trigger external sends.
	notifications := v1.Group("/notifications")
	{
		notifications.GET("", s.listNotifications)
		notifications.GET("/unread-count", s.getNotificationUnreadCount)
		notifications.POST("/read-all", s.markAllNotificationsRead)
		notifications.POST("/:notificationID/read", s.markNotificationRead)
		notifications.DELETE("/:notificationID", s.deleteNotification)
	}

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
		apps.GET("/:appID/instances/:targetID/logs", s.getApplicationInstanceLogs)
		apps.POST("/:appID/instances/:targetID/decommission", s.decommissionInstance)
		apps.DELETE("/:appID/instances/:targetID", s.deleteInstance)
		apps.GET("/:appID/environments/:env", s.getApplicationEnvironment)
		apps.PUT("/:appID/environments/:env", s.updateApplicationEnvironment)
		apps.GET("/:appID/releases", s.listReleases)
		apps.POST("/:appID/releases", s.createRelease)
		apps.GET("/:appID/env-deployments", s.listEnvDeployments)
		apps.POST("/:appID/env-deployments", s.createEnvDeployment)
	}

	envDeployments := v1.Group("/env-deployments")
	{
		envDeployments.GET("/:deploymentID", s.getEnvDeployment)
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
		machines.POST("/:machineID/rotate-token", s.rotateMachineToken)
	}

	// Settings hold secrets (tokens) — admin only.
	settings := v1.Group("/settings")
	settings.Use(s.requireAdmin())
	{
		settings.GET("", s.listSettings)
		settings.PUT("/:key", s.upsertSetting)
		settings.DELETE("/:key", s.deleteSetting)
	}

	// Alerts send to webhooks configured via Settings (alert.channel.<type>.<name>)
	// — admin only, same reasoning as settings themselves holding secrets.
	alerts := v1.Group("/alerts")
	alerts.Use(s.requireAdmin())
	{
		alerts.POST("", s.sendAlert)
		alerts.POST("/test", s.testAlert)
		alerts.GET("/channels", s.listAlertChannels)
	}

	// Alert rules evaluate PromQL against Prometheus and can trigger external
	// sends via the channels above — admin only, same reasoning as /alerts.
	alertRules := v1.Group("/alert-rules")
	alertRules.Use(s.requireAdmin())
	{
		alertRules.GET("", s.listAlertRules)
		alertRules.POST("", s.createAlertRule)
		alertRules.GET("/:id", s.getAlertRule)
		alertRules.PUT("/:id", s.updateAlertRule)
		alertRules.DELETE("/:id", s.deleteAlertRule)
		alertRules.POST("/:id/test", s.testAlertRule)
	}

	// User management — admin only.
	users := v1.Group("/users")
	users.Use(s.requireAdmin())
	{
		users.GET("", s.listUsers)
		users.POST("", s.createUser)
		users.DELETE("/:userID", s.deleteUser)
	}
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
