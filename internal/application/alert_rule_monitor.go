package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/promclient"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	alertsdk "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"
)

// alertEvalBatchTimeout bounds one evaluateOnce pass as a whole, the same
// backstop reasoning checkBatchTimeout documents for HealthMonitor: it must
// not be as short as the tick interval, or a handful of slow rules early in
// the list could starve rules later in the list.
const alertEvalBatchTimeout = 2 * time.Minute

// prometheusURLSettingKey mirrors service.prometheusURLSettingKey (unexported
// there) — duplicated rather than exported solely for this one constant,
// since AlertRuleMonitor lives in a different package for the same reason
// HealthMonitor does (internal/application hosts tickers, internal/service
// hosts the non-ticking CRUD/business logic they call into).
const prometheusURLSettingKey = "observability.prometheus_url"

// settingsGetter mirrors the unexported interface of the same name in
// internal/service — AlertRuleMonitor only needs Get, not the full
// SettingsReader (GetByPrefix) AlertService uses.
type settingsGetter interface {
	Get(key string) (string, bool)
}

// alertRuleRepo is the narrow slice of AlertRuleService's persistence surface
// the monitor drives directly (list due rules, persist post-evaluation
// state) — AlertRuleService itself doesn't expose a "just run one tick"
// method, so the monitor talks to the repo for reads/writes and to
// AlertService for sends, the same two-collaborator shape HealthMonitor has
// (ApplicationHealthService for state, nothing else) except split because
// firing an alert crosses into a different service than persisting rule state.
type alertRuleRepo interface {
	ListEnabled(ctx context.Context) ([]*domain.AlertRule, error)
	Update(ctx context.Context, rule *domain.AlertRule) error
}

// AlertRuleMonitor is a ticking background worker that evaluates every
// enabled AlertRule's PromQL expression against Prometheus and fires alerts
// (via AlertService.SendToAll, reusing the existing Feishu/DingTalk/WeCom/
// Slack/custom-webhook channels + in-app inbox) on ok→firing transitions,
// and a recovery notice on firing→ok. Mirrors HealthMonitor's shape exactly.
type AlertRuleMonitor struct {
	rules    alertRuleRepo
	settings settingsGetter
	alerts   *service.AlertService
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewAlertRuleMonitor(rules alertRuleRepo, settings settingsGetter, alerts *service.AlertService, interval time.Duration) *AlertRuleMonitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &AlertRuleMonitor{rules: rules, settings: settings, alerts: alerts, interval: interval, ctx: ctx, cancel: cancel}
}

func (m *AlertRuleMonitor) Start() {
	if m == nil || m.rules == nil {
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

func (m *AlertRuleMonitor) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.wg.Wait()
}

func (m *AlertRuleMonitor) evaluateOnce() {
	ctx, cancel := context.WithTimeout(m.ctx, alertEvalBatchTimeout)
	defer cancel()

	promURL, ok := m.settings.Get(prometheusURLSettingKey)
	if !ok || promURL == "" {
		logger.L().Warn(prometheusURLSettingKey + " is not configured; skipping alert rule evaluation")
		return
	}

	rules, err := m.rules.ListEnabled(ctx)
	if err != nil {
		logger.L().Warn("alert rule monitor: failed to list enabled rules", zap.Error(err))
		return
	}
	for _, rule := range rules {
		m.evaluateRule(ctx, promURL, rule)
	}
}

// evaluateRule runs one rule's PromQL and updates its persisted state. A
// query/transport error leaves ConsecutiveBreaches/State untouched — a
// transient Prometheus hiccup shouldn't flip state or reset the breach
// counter, the same reasoning checkBatchTimeout's doc comment gives for not
// letting an infra blip masquerade as a real health-check failure.
func (m *AlertRuleMonitor) evaluateRule(ctx context.Context, promURL string, rule *domain.AlertRule) {
	now := time.Now()
	result, err := promclient.Query(ctx, promURL, rule.Expr, now)
	if err != nil {
		rule.LastError = err.Error()
		rule.LastEvaluatedAt = &now
		if uerr := m.rules.Update(ctx, rule); uerr != nil {
			logger.L().Warn("alert rule monitor: failed to persist evaluation error", zap.Int64("rule_id", rule.ID), zap.Error(uerr))
		}
		return
	}

	breaching := len(result.Result) > 0
	if breaching {
		rule.ConsecutiveBreaches++
	} else {
		rule.ConsecutiveBreaches = 0
	}
	threshold := 1
	if rule.ForSeconds > 0 && m.interval > 0 {
		threshold = int((time.Duration(rule.ForSeconds)*time.Second + m.interval - 1) / m.interval) // ceil
		if threshold < 1 {
			threshold = 1
		}
	}

	channels, _ := rule.ParseChannels()
	switch {
	case rule.State == domain.AlertRuleStateOK && breaching && rule.ConsecutiveBreaches >= threshold:
		rule.State = domain.AlertRuleStateFiring
		rule.LastFiredAt = &now
		msg := alertsdk.Message{
			Title:   rule.Name,
			Content: renderResult(rule.Expr, result),
			Level:   alertsdk.Level(rule.Level),
		}
		m.alerts.SendToAll(ctx, msg, channels...)
	case rule.State == domain.AlertRuleStateFiring && !breaching:
		rule.State = domain.AlertRuleStateOK
		msg := alertsdk.Message{
			Title:   rule.Name + " recovered",
			Content: "Condition no longer breaching: " + rule.Expr,
			Level:   alertsdk.LevelInfo,
		}
		m.alerts.SendToAll(ctx, msg, channels...)
	}

	rule.LastError = ""
	rule.LastEvaluatedAt = &now
	if err := m.rules.Update(ctx, rule); err != nil {
		logger.L().Warn("alert rule monitor: failed to persist rule state", zap.Int64("rule_id", rule.ID), zap.Error(err))
	}
}

// renderResult formats a PromQL result vector into readable alert text,
// capped to the first 10 series so a high-cardinality query can't blow up
// message size.
func renderResult(expr string, result *promclient.QueryResult) string {
	if result == nil || len(result.Result) == 0 {
		return expr
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", expr)
	n := len(result.Result)
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		s := result.Result[i]
		value := ""
		if len(s.Value) == 2 {
			value = fmt.Sprintf("%v", s.Value[1])
		}
		fmt.Fprintf(&b, "%s = %s\n", formatLabels(s.Metric), value)
	}
	if len(result.Result) > n {
		fmt.Fprintf(&b, "... and %d more series\n", len(result.Result)-n)
	}
	return b.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%q", k, v))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
