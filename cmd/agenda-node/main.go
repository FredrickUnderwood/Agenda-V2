// Command agenda-node is the resident per-machine agent. It exposes a
// management API (jobs + proxy registration) the control plane drives instead of
// SSH, runs a local reverse proxy for gateway traffic, and heartbeats the
// control plane. See doc/agenda-node-tech-design.md.
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/node"
	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

func main() {
	configPath := flag.String("config", "", "path to agenda-node.yaml")
	flag.Parse()

	if err := log.Init(log.Config{AppName: "agenda-node"}); err != nil {
		os.Stderr.WriteString("failed to init logger: " + err.Error() + "\n")
	}
	defer log.Shutdown()

	cfg, err := node.Load(*configPath)
	if err != nil {
		log.L().Fatal("failed to load config", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	jobs := node.NewJobStore(cfg.MaxOutputBytes, cfg.JobRetention.Duration)
	jobs.StartGC(ctx)
	registry := node.NewProxyRegistry()

	// Management API (jobs + proxy registration + health), token-protected.
	mgmt := node.NewServer(cfg.Token, jobs, registry)
	mgmtSrv := &http.Server{Addr: cfg.ListenAddr, Handler: mgmt.Handler(), ReadHeaderTimeout: 10 * time.Second}

	// Data-plane reverse proxy (gateway backends point here), no auth.
	proxySrv := &http.Server{Addr: cfg.ProxyListenAddr, Handler: node.NewProxyHandler(registry, cfg.ProxyBackendHost)}

	// Heartbeat to the control plane.
	node.NewHeartbeat(cfg.CentralBaseURL, cfg.MachineID, cfg.Token, cfg.HeartbeatInterval.Duration).Start(ctx)

	errCh := make(chan error, 2)
	go func() {
		log.L().Info("management server starting", zap.String("addr", cfg.ListenAddr))
		errCh <- mgmtSrv.ListenAndServe()
	}()
	go func() {
		log.L().Info("proxy server starting", zap.String("addr", cfg.ProxyListenAddr))
		errCh <- proxySrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.L().Fatal("server error", zap.Error(err))
		}
	case <-ctx.Done():
		log.L().Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = mgmtSrv.Shutdown(shutdownCtx)
	_ = proxySrv.Shutdown(shutdownCtx)
}
