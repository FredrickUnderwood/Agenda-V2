package application

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// purgeTimeout bounds one retention pass. Generous, because a first run after
// the feature has been in use for a while may have a lot to delete.
const purgeTimeout = 5 * time.Minute

// DBQueryLogMonitor enforces the audit trail's retention window on a ticker.
//
// This is not housekeeping for its own sake: audit entries hold real query
// results, so without it the control-plane database would slowly accumulate a
// copy of whatever anyone has ever looked at.
type DBQueryLogMonitor struct {
	querySvc *service.DatabaseQueryService
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewDBQueryLogMonitor(querySvc *service.DatabaseQueryService, interval time.Duration) *DBQueryLogMonitor {
	if interval <= 0 {
		interval = time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &DBQueryLogMonitor{querySvc: querySvc, interval: interval, ctx: ctx, cancel: cancel}
}

func (m *DBQueryLogMonitor) Start() {
	if m == nil || m.querySvc == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		m.purgeOnce()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.purgeOnce()
			}
		}
	}()
}

func (m *DBQueryLogMonitor) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.wg.Wait()
}

func (m *DBQueryLogMonitor) purgeOnce() {
	ctx, cancel := context.WithTimeout(m.ctx, purgeTimeout)
	defer cancel()
	removed, err := m.querySvc.PurgeExpiredLogs(ctx)
	if err != nil {
		logger.L().Warn("failed to purge expired database query logs", zap.Error(err))
		return
	}
	if removed > 0 {
		logger.L().Info("purged expired database query logs", zap.Int64("removed", removed))
	}
}
