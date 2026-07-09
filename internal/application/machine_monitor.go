package application

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	alertsdk "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"
)

// machineCheckTimeout bounds one evaluateOnce pass, the same backstop shape
// AlertRuleMonitor's alertEvalBatchTimeout documents.
const machineCheckTimeout = time.Minute

// machineLister is the narrow slice of MachineService the monitor needs. Using
// ListViews (not the repo directly) means the offline decision reuses the exact
// same online-window derivation the API/UI shows operators — an alert can never
// disagree with the dashboard.
type machineLister interface {
	ListViews(ctx context.Context) ([]service.MachineView, error)
}

// MachineMonitor is a ticking background worker that watches agent-mode
// machines' heartbeat-derived online status and fires an alert (via
// AlertService.SendToAll, reusing the Feishu/DingTalk/WeCom/Slack/custom
// channels + in-app inbox) on an online->offline transition, plus a recovery
// notice on offline->online. Mirrors HealthMonitor/AlertRuleMonitor's shape.
//
// Only agent-mode machines are considered: SSH/local machines have no
// heartbeat and always report offline (domain.Machine.Online), so alerting on
// them would be a permanent false positive.
type MachineMonitor struct {
	machines machineLister
	alerts   *service.AlertService
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	// lastOnline remembers each agent machine's previously observed status so
	// only edges (not every tick) alert. Touched solely by the single monitor
	// goroutine, so it needs no lock.
	lastOnline map[int64]bool
}

func NewMachineMonitor(machines machineLister, alerts *service.AlertService, interval time.Duration) *MachineMonitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &MachineMonitor{
		machines:   machines,
		alerts:     alerts,
		interval:   interval,
		ctx:        ctx,
		cancel:     cancel,
		lastOnline: make(map[int64]bool),
	}
}

func (m *MachineMonitor) Start() {
	if m == nil || m.machines == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		m.evaluateOnce()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.evaluateOnce()
			}
		}
	}()
}

func (m *MachineMonitor) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.wg.Wait()
}

func (m *MachineMonitor) evaluateOnce() {
	ctx, cancel := context.WithTimeout(m.ctx, machineCheckTimeout)
	defer cancel()

	views, err := m.machines.ListViews(ctx)
	if err != nil {
		logger.L().Warn("machine monitor: failed to list machines", zap.Error(err))
		return
	}

	seen := make(map[int64]struct{}, len(views))
	for _, v := range views {
		if v.Machine == nil || v.Mode != domain.MachineModeAgent {
			continue
		}
		seen[v.ID] = struct{}{}
		online := v.Online
		prev, known := m.lastOnline[v.ID]
		m.lastOnline[v.ID] = online
		if !known {
			// First observation (fresh process or newly-added machine): seed
			// state without alerting, so a restart doesn't re-blast every
			// already-offline machine.
			continue
		}
		switch {
		case prev && !online:
			m.alert(ctx, v, false)
		case !prev && online:
			m.alert(ctx, v, true)
		}
	}

	// Drop machines that were deleted (or flipped out of agent mode) so their
	// stale state can't resurface if the id is ever reused.
	for id := range m.lastOnline {
		if _, ok := seen[id]; !ok {
			delete(m.lastOnline, id)
		}
	}
}

func (m *MachineMonitor) alert(ctx context.Context, v service.MachineView, recovered bool) {
	if m.alerts == nil {
		return
	}
	title := fmt.Sprintf("Machine offline: %s", v.Name)
	level := alertsdk.LevelCritical
	last := "never"
	if v.AgentLastHeartbeatAt != nil {
		last = v.AgentLastHeartbeatAt.UTC().Format(time.RFC3339)
	}
	content := fmt.Sprintf("Agent machine %s (%s) has stopped sending heartbeats.\nLast heartbeat: %s", v.Name, v.Host, last)
	if recovered {
		title = fmt.Sprintf("Machine recovered: %s", v.Name)
		level = alertsdk.LevelInfo
		content = fmt.Sprintf("Agent machine %s (%s) is sending heartbeats again.", v.Name, v.Host)
	}

	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m.alerts.SendToAll(sendCtx, alertsdk.Message{Title: title, Content: content, Level: level})
}
