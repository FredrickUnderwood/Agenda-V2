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

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/application"
	"github.com/agenda-v2/internal/handler"
	"github.com/agenda-v2/internal/logger"
	"github.com/agenda-v2/internal/pipeline"
	"github.com/agenda-v2/internal/repository"
	"github.com/agenda-v2/internal/service"
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

	// Schema is managed exclusively via resources/migrations/*.sql, applied
	// out of band before the server starts (see resources/migrations/README).
	db, err := repository.OpenMySQL(cfg.Database.DSN)
	if err != nil {
		logger.L().Fatal("failed to open database", zap.Error(err))
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
	machineRepo := repository.NewMachineRepository(db)
	logRepo := repository.NewDeployLogRepository(db)
	stepRepo := repository.NewPipelineStepRepository(db)

	// Service
	appSvc := service.NewApplicationService(appRepo, appTargetRepo, appGatewayRouteRepo, appGatewayRouteBackendRepo, machineRepo, appHealthRepo)
	appHealthSvc := service.NewApplicationHealthService(appTargetRepo, appHealthRepo, machineRepo)
	appEnvironmentSvc := service.NewApplicationEnvironmentService(appEnvironmentRepo)
	appReleaseSvc := service.NewApplicationReleaseService(appReleaseRepo, appRepo, appTargetRepo, appGatewayRouteRepo, appEnvironmentRepo)
	machineSvc := service.NewMachineService(machineRepo)
	logSvc := service.NewDeployLogService(logRepo, stepRepo)
	stepSvc := service.NewPipelineStepService(stepRepo)
	lockSvc := service.NewDeployLockService(rdb)

	// Pipeline
	builder := pipeline.NewBuilder(cfg, machineSvc, appSvc, appEnvironmentSvc)
	runner := pipeline.NewRunner(cfg, logSvc, stepSvc)

	// Application
	releaseApp := application.NewReleaseApplication(cfg, builder, runner, logSvc, stepSvc, appSvc, appReleaseSvc, lockSvc)

	healthMonitor := application.NewHealthMonitor(appHealthSvc, 15*time.Second)
	healthMonitor.Start()
	defer healthMonitor.Stop()

	// Handler
	srv := handler.NewServer(cfg, appSvc, appHealthSvc, appEnvironmentSvc, appReleaseSvc, machineSvc, logSvc, releaseApp)

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
