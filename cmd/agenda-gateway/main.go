package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	coreauth "github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/application"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/edgetls"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/handler"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"go.uber.org/zap"
)

// agenda-gateway
func main() {
	cfg, cfgErr := config.Load()

	appName := "agenda-gateway"
	logLevel := "info"
	if cfg != nil {
		appName = cfg.AppName
		logLevel = cfg.LogLevel
	}
	if err := alog.Init(alog.Config{AppName: appName, Level: logLevel}); err != nil {
		alog.L().Warn("log file sink not available", zap.Error(err))
	}
	defer alog.Shutdown()

	if cfgErr != nil {
		alog.L().Error("load config failed", zap.Error(cfgErr))
		os.Exit(1)
	}

	ctx := context.Background()
	alog.Info(ctx, "permissions exposed",
		zap.String("app", cfg.AppName),
		zap.Strings("perms", auth.All()),
	)

	db, err := repository.OpenMySQL(repository.DBOptions{
		DSN:             cfg.MySQL.DSN,
		MaxOpenConns:    cfg.MySQL.MaxOpenConns,
		MaxIdleConns:    cfg.MySQL.MaxIdleConns,
		ConnMaxLifetime: cfg.MySQL.ConnMaxLifetime,
	})
	if err != nil {
		alog.L().Error("open mysql failed", zap.Error(err))
		os.Exit(1)
	}
	sqlDB, err := db.DB()
	if err != nil {
		alog.L().Error("get mysql sql db failed", zap.Error(err))
		os.Exit(1)
	}
	defer sqlDB.Close()
	if err := repository.Migrate(db); err != nil {
		alog.L().Error("migrate mysql failed", zap.Error(err))
		os.Exit(1)
	}

	authMgr := coreauth.NewManager(cfg.Auth.JWTSecret, "", 0)
	routeRepo := repository.NewRouteRepository(db)
	routeSvc := service.NewRouteService(routeRepo, cfg.Gateway.DefaultBackendTimeout, cfg.Gateway.WebSocket.DefaultIdleTimeout)
	gatewayApp := application.NewGatewayApplication(routeSvc, cfg.Gateway.RefreshInterval, application.WebSocketOptions{
		MaxConnections:        cfg.Gateway.WebSocket.MaxConnections,
		MaxConnectionsPerIP:   cfg.Gateway.WebSocket.MaxConnectionsPerIP,
		HandshakeRate:         cfg.Gateway.WebSocket.HandshakeRate,
		HandshakeBurst:        cfg.Gateway.WebSocket.HandshakeBurst,
		DialTimeout:           cfg.Gateway.WebSocket.DialTimeout,
		ResponseHeaderTimeout: cfg.Gateway.WebSocket.ResponseHeaderTimeout,
	})
	if err := gatewayApp.Start(ctx); err != nil {
		alog.L().Error("start gateway app failed", zap.Error(err))
		os.Exit(1)
	}
	defer gatewayApp.Stop()

	// Edge TLS: when enabled, the gateway terminates TLS on :443 and auto-issues
	// certs (ACME DNS-01) itself, replacing the standalone agenda-caddy edge. The
	// managed-domain set is the static config list plus every live route host.
	tlsMgr, err := edgetls.New(cfg.TLS)
	if err != nil {
		alog.L().Error("init edge tls failed", zap.Error(err))
		os.Exit(1)
	}
	if tlsMgr != nil {
		// Issuance stays idle until the control plane pushes ACME/DNS credentials
		// (from Settings) to /-/tls; the reconcile loop and :443 listener are up
		// regardless so certs already in storage keep serving.
		tlsMgr.Start(ctx, gatewayApp.Hosts)
		defer tlsMgr.Stop()
	}

	srv := handler.NewServer(cfg, db, authMgr, routeSvc, gatewayApp, tlsMgr)

	// Buffered for both the HTTP and (optional) HTTPS listener goroutines, so
	// the second to exit on shutdown never blocks writing its result.
	serverErr := make(chan error, 2)
	go func() {
		alog.Info(ctx, "server starting",
			zap.String("addr", cfg.Server.Addr),
			zap.String("app", cfg.AppName),
			zap.Int("service_token_count", len(cfg.ServiceTokens)),
		)
		serverErr <- srv.Start()
	}()
	if tlsMgr != nil {
		go func() {
			alog.Info(ctx, "tls server starting", zap.String("addr", tlsMgr.HTTPSAddr()))
			serverErr <- srv.StartTLS()
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			alog.L().Error("server exited", zap.Error(err))
			os.Exit(1)
		}
	case sig := <-stop:
		alog.Info(ctx, "server shutting down", zap.String("signal", sig.String()))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		alog.L().Error("server shutdown failed", zap.Error(err))
	}
}
