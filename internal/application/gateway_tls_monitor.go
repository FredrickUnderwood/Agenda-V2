package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// GatewayTLSMonitor periodically pushes the edge-TLS credentials (from Settings)
// to the gateway. The periodic re-push is deliberate: the gateway holds the
// config only in memory, so a gateway restart would otherwise leave it unable
// to issue new certs until a credential edit; re-priming every tick closes that
// window (same reasoning as re-registering node proxy targets each monitor tick).
type GatewayTLSMonitor struct {
	syncSvc  *service.GatewayTLSSyncService
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewGatewayTLSMonitor(syncSvc *service.GatewayTLSSyncService, interval time.Duration) *GatewayTLSMonitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &GatewayTLSMonitor{syncSvc: syncSvc, interval: interval, ctx: ctx, cancel: cancel}
}

func (m *GatewayTLSMonitor) Start() {
	if m == nil || m.syncSvc == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		m.syncOnce()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.syncOnce()
			}
		}
	}()
}

func (m *GatewayTLSMonitor) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.wg.Wait()
}

func (m *GatewayTLSMonitor) syncOnce() {
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	if err := m.syncSvc.Sync(ctx); err != nil {
		// Missing credentials is the expected steady state until an operator
		// fills in the Settings; don't log it as a failure every tick.
		if errors.Is(err, service.ErrGatewayTLSNotConfigured) {
			return
		}
		logger.L().Warn("gateway tls sync failed", zap.Error(err))
	}
}
