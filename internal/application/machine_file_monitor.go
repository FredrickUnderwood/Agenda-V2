package application

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// verifyTimeout bounds one verification pass. Each file costs one stat plus a
// checksum on the machine that holds it, so a large estate takes a while — but
// a pass that runs past the next tick is a sign something is wedged, not a
// reason to keep waiting.
const verifyTimeout = 10 * time.Minute

// MachineFileMonitor re-checks every uploaded file on a ticker.
//
// The console has a per-file check button, but a button only reports what
// someone thought to ask about. A credential that disappears — a machine
// rebuilt, a workspace wiped, a file replaced by hand — produces no signal of
// its own: the application that needed it fails later, elsewhere, without
// naming it. The ticker is what turns that into something the platform notices
// on its own.
type MachineFileMonitor struct {
	files    *service.MachineFileService
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewMachineFileMonitor(files *service.MachineFileService, interval time.Duration) *MachineFileMonitor {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &MachineFileMonitor{files: files, interval: interval, ctx: ctx, cancel: cancel}
}

func (m *MachineFileMonitor) Start() {
	if m == nil || m.files == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		m.verifyOnce()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.verifyOnce()
			}
		}
	}()
}

func (m *MachineFileMonitor) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.wg.Wait()
}

func (m *MachineFileMonitor) verifyOnce() {
	ctx, cancel := context.WithTimeout(m.ctx, verifyTimeout)
	defer cancel()
	checked, problems, err := m.files.VerifyAllCurrent(ctx)
	if err != nil {
		logger.L().Warn("failed to verify machine files", zap.Error(err))
		return
	}
	if problems > 0 {
		logger.L().Warn("uploaded files are not in their recorded state",
			zap.Int("checked", checked), zap.Int("problems", problems))
	}
}
