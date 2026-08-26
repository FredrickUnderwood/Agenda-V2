package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof handlers on http.DefaultServeMux
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/application"
	"github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gatewayclient"
	"github.com/FredrickUnderwood/agenda-v2/internal/handler"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/pipeline"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func main() {
	configPath := flag.String("config", "", "path to agenda-v2.yaml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger.Init(logger.Config{Level: cfg.Log.Level})
	defer logger.Shutdown()

	db, err := repository.OpenMySQL(cfg.Database.DSN)
	if err != nil {
		logger.L().Fatal("failed to open database", zap.Error(err))
	}
	// Create/update the schema from the domain models so a fresh install works
	// with no manual SQL. AutoMigrate is additive (never drops), safe on restart.
	if err := repository.Migrate(db); err != nil {
		logger.L().Fatal("failed to migrate database", zap.Error(err))
	}

	rdb, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		logger.L().Fatal("failed to connect redis", zap.Error(err))
	}
	defer rdb.Close()

	// Repository
	appRepo := repository.NewApplicationRepository(db)
	appTargetRepo := repository.NewApplicationTargetRepository(db)
	appGatewayRouteRepo := repository.NewApplicationGatewayRouteRepository(db)
	appGatewayRouteBackendRepo := repository.NewApplicationGatewayRouteBackendRepository(db)
	appHealthRepo := repository.NewApplicationInstanceHealthRepository(db)
	appEnvironmentRepo := repository.NewApplicationEnvironmentRepository(db)
	appReleaseRepo := repository.NewApplicationReleaseRepository(db)
	envDeploymentRepo := repository.NewEnvDeploymentRepository(db)
	machineRepo := repository.NewMachineRepository(db)
	logRepo := repository.NewDeployLogRepository(db)
	stepRepo := repository.NewPipelineStepRepository(db)
	settingRepo := repository.NewSettingRepository(db)
	userRepo := repository.NewUserRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)
	alertRuleRepo := repository.NewAlertRuleRepository(db)
	dbInstanceRepo := repository.NewDatabaseInstanceRepository(db)
	dbQueryLogRepo := repository.NewDBQueryLogRepository(db)

	// One-time data migration: env vars used to live on the application as a
	// single all-environments baseline; they are now per-environment. Move any
	// remaining baseline into the prod environment. Idempotent — a no-op once
	// every application has been migrated.
	// A failure here is not fatal: each application is migrated atomically
	// (prod row written before the baseline is cleared), unmigrated ones keep
	// deploying off the baseline layer, and the next restart retries.
	if _, err := service.BackfillApplicationEnvVars(context.Background(), appRepo, appEnvironmentRepo, appTargetRepo); err != nil {
		logger.L().Warn("failed to backfill application env vars; will retry on next start", zap.Error(err))
	}

	// Service
	secretBox := secret.NewBox(cfg.Security.MasterKey)

	machineSvc := service.NewMachineService(machineRepo, secretBox)
	machineSvc.SetAgentPollInterval(cfg.Deploy.AgentPollInterval.Duration)
	appSvc := service.NewApplicationService(appRepo, appTargetRepo, appGatewayRouteRepo, appGatewayRouteBackendRepo, machineRepo, appHealthRepo)
	// Health probes to agent-mode machines are relayed through the node, which
	// needs the decrypted agent token — so pass machineSvc (Get decrypts), not
	// the raw machineRepo, matching the logs/metrics relays.
	appHealthSvc := service.NewApplicationHealthService(appRepo, appTargetRepo, appHealthRepo, machineSvc)
	appEnvironmentSvc := service.NewApplicationEnvironmentService(appEnvironmentRepo)
	appReleaseSvc := service.NewApplicationReleaseService(appReleaseRepo, appRepo, appTargetRepo, appGatewayRouteRepo, appEnvironmentRepo)
	envDeploymentSvc := service.NewEnvDeploymentService(envDeploymentRepo, appReleaseRepo)
	appLogSvc := service.NewApplicationLogService(appRepo, appTargetRepo, appReleaseRepo, machineSvc, cfg.WorkspaceRoot)
	appMetricsSvc := service.NewApplicationMetricsService(appRepo, appTargetRepo, machineSvc)
	// Queries are relayed through the target machine's agenda-node, so the
	// instance service resolves machines via machineSvc (which decrypts the
	// agent token) rather than the raw repository — same reason logs and
	// metrics do.
	dbInstanceSvc := service.NewDatabaseInstanceService(dbInstanceRepo, machineSvc, secretBox)
	logSvc := service.NewDeployLogService(logRepo, stepRepo)
	stepSvc := service.NewPipelineStepService(stepRepo)
	lockSvc := service.NewDeployLockService(rdb)
	settingSvc := service.NewSettingService(settingRepo, secretBox)
	if err := settingSvc.Load(context.Background()); err != nil {
		logger.L().Warn("failed to load settings from db; falling back to yaml config (has migration 0002_setting.sql been applied?)", zap.Error(err))
	}
	// Route git token lookups through the Setting table so tokens can rotate at
	// runtime; the static yaml git.tokens map remains as a bootstrap fallback.
	cfg.Git.TokenResolver = settingSvc.GitToken
	cfg.Git.SecretValues = settingSvc.SecretValues
	dbQuerySvc := service.NewDatabaseQueryService(dbInstanceSvc, dbQueryLogRepo, settingSvc, secretBox)
	alertSvc := service.NewAlertService(settingSvc, notificationRepo)
	alertRuleSvc := service.NewAlertRuleService(alertRuleRepo, settingSvc, alertSvc)
	notificationSvc := service.NewNotificationService(notificationRepo)
	userSvc := service.NewUserService(userRepo)
	authMgr := auth.NewManager(cfg.Auth.JWTSecret, cfg.Server.AuthToken, cfg.Auth.TokenTTL.Duration)
	if err := userSvc.EnsureBootstrapAdmin(context.Background(), cfg.Auth.BootstrapAdminUsername, cfg.Auth.BootstrapAdminPassword); err != nil {
		logger.L().Warn("failed to ensure bootstrap admin", zap.Error(err))
	}

	// Pipeline
	builder := pipeline.NewBuilder(cfg, machineSvc, appSvc, appEnvironmentSvc)
	runner := pipeline.NewRunner(cfg, logSvc, stepSvc)

	// Application
	releaseApp := application.NewReleaseApplication(cfg, builder, runner, logSvc, stepSvc, appSvc, appReleaseSvc, lockSvc, envDeploymentSvc, alertSvc)
	instanceLifecycleApp := application.NewInstanceLifecycleApplication(cfg, builder, runner, logSvc, stepSvc, appSvc, appReleaseSvc, lockSvc, appHealthSvc, alertSvc)

	healthMonitor := application.NewHealthMonitor(appHealthSvc, 15*time.Second)
	healthMonitor.Start()
	defer healthMonitor.Stop()

	alertRuleMonitor := application.NewAlertRuleMonitor(alertRuleRepo, settingSvc, alertSvc, cfg.Observability.AlertEvalInterval.Duration)
	alertRuleMonitor.Start()
	defer alertRuleMonitor.Stop()

	proxyResyncSvc := service.NewProxyResyncService(appTargetRepo, appReleaseRepo, machineSvc, appRepo)
	instanceReconcile := application.NewInstanceReconcile(appTargetRepo, builder, appRepo, appReleaseRepo)
	machineMonitor := application.NewMachineMonitor(machineSvc, alertSvc, proxyResyncSvc, instanceReconcile, 30*time.Second)
	machineMonitor.Start()
	defer machineMonitor.Stop()

	// Audit entries hold real query results; this keeps them from outliving
	// their retention window (rds.query_log_retention_days).
	dbQueryLogMonitor := application.NewDBQueryLogMonitor(dbQuerySvc, time.Hour)
	dbQueryLogMonitor.Start()
	defer dbQueryLogMonitor.Stop()

	// Push edge-TLS credentials (from Settings) to the gateway on a ticker so the
	// gateway can auto-issue certs without those secrets in its env, and so a
	// gateway restart is re-primed. Only when gateway integration is enabled.
	if cfg.Gateway.Enabled {
		gatewayTLSSyncSvc := service.NewGatewayTLSSyncService(settingSvc, gatewayclient.NewClient(cfg.Gateway))
		gatewayTLSMonitor := application.NewGatewayTLSMonitor(gatewayTLSSyncSvc, 30*time.Second)
		gatewayTLSMonitor.Start()
		defer gatewayTLSMonitor.Stop()
	}

	// Handler
	srv := handler.NewServer(cfg, appSvc, appHealthSvc, appEnvironmentSvc, appReleaseSvc, envDeploymentSvc, machineSvc, logSvc, appLogSvc, appMetricsSvc, dbInstanceSvc, dbQuerySvc, settingSvc, alertSvc, alertRuleSvc, notificationSvc, userSvc, authMgr, releaseApp, instanceLifecycleApp)

	// pprof debug server (goroutine/heap profiling). Bound to loopback by
	// default so it is reachable via `docker exec` but never public. Disable
	// by setting server.pprof_addr to "" in config.
	if addr := cfg.Server.PprofAddr; addr != "" {
		go func() {
			logger.L().Info("pprof server starting", zap.String("addr", addr))
			if err := http.ListenAndServe(addr, nil); err != nil {
				logger.L().Error("pprof server error", zap.Error(err))
			}
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		logger.L().Info("HTTP server starting", zap.String("addr", cfg.Server.Addr))
		serverErr <- srv.Start()
	}()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.L().Fatal("server error", zap.Error(err))
		}
	case sig := <-stop:
		logger.L().Info("shutting down", zap.String("signal", sig.String()))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.L().Error("shutdown error", zap.Error(err))
		}
	}
}
