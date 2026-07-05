package application

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// checkBatchTimeout bounds one CheckDueTargets pass as a whole (a backstop
// against a hung request), not each individual check — that's already
// bounded by the due target's own HealthCheckTimeoutMS. It is deliberately
// NOT tied to the ticker interval: CheckDueTargets checks every due target
// sequentially, so if the batch timeout were as short as the tick interval
// (as short as a few seconds), a handful of slow-but-due targets early in
// the list could exhaust the shared budget and starve targets later in the
// list — their request would be built on an already-expired context and
// fail instantly, which is indistinguishable from a real health-check
// failure and would wrongly count towards their failure_threshold.
const checkBatchTimeout = 2 * time.Minute

// HealthMonitor is a ticking background worker that runs due HTTP health
// checks for every enabled instance (internal/service.ApplicationHealthService
// decides which are "due" based on each target's configured interval).
type HealthMonitor struct {
	healthSvc *service.ApplicationHealthService
	interval  time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func NewHealthMonitor(healthSvc *service.ApplicationHealthService, interval time.Duration) *HealthMonitor {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &HealthMonitor{healthSvc: healthSvc, interval: interval, ctx: ctx, cancel: cancel}
}

func (m *HealthMonitor) Start() {
	if m == nil || m.healthSvc == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		m.checkOnce()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.checkOnce()
			}
		}
	}()
}

func (m *HealthMonitor) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.wg.Wait()
}

func (m *HealthMonitor) checkOnce() {
	ctx, cancel := context.WithTimeout(m.ctx, checkBatchTimeout)
	defer cancel()
	if err := m.healthSvc.CheckDueTargets(ctx); err != nil {
		logger.L().Warn("health monitor check failed", zap.Error(err))
	}
}
