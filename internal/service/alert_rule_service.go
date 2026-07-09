package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/promclient"
	alertsdk "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"
)

// AlertRuleRepo is the persistence contract AlertRuleService needs — mirrors
// MachineService's MachineRepo interface shape, so tests can fake it without
// a real DB.
type AlertRuleRepo interface {
	Create(ctx context.Context, rule *domain.AlertRule) error
	Update(ctx context.Context, rule *domain.AlertRule) error
	GetByID(ctx context.Context, id int64) (*domain.AlertRule, error)
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context) ([]*domain.AlertRule, error)
	ListEnabled(ctx context.Context) ([]*domain.AlertRule, error)
}

// prometheusURLSettingKey is where the alert rule engine reads Prometheus's
// own base URL — Setting table, not yaml, matching the git.token.* precedent
// (operator-rotatable, not a bootstrap secret needed before DB connectivity).
const prometheusURLSettingKey = "observability.prometheus_url"

// settingsGetter is the narrow accessor AlertRuleService/AlertRuleMonitor
// need from SettingService — just Get, unlike AlertService's SettingsReader
// (GetByPrefix, for enumerating a whole "alert.channel." namespace). Defined
// as its own interface (rather than depending on *SettingService directly)
// for the same unit-testability reasoning as MachineService's MachineRepo.
type settingsGetter interface {
	Get(key string) (string, bool)
}

func validAlertLevel(level string) bool {
	switch alertsdk.Level(level) {
	case alertsdk.LevelInfo, alertsdk.LevelWarning, alertsdk.LevelCritical:
		return true
	default:
		return false
	}
}

// UpsertAlertRuleRequest is the input to Create/Update.
type UpsertAlertRuleRequest struct {
	Name       string   `json:"name"`
	Expr       string   `json:"expr"`
	ForSeconds int      `json:"for_seconds"`
	Level      string   `json:"level"`
	Channels   []string `json:"channels"`
	Enabled    *bool    `json:"enabled"`
}

// AlertRuleService owns AlertRule CRUD and on-demand evaluation. The ticking
// evaluation loop that actually fires alerts lives separately in
// internal/application.AlertRuleMonitor, which calls SendToAll via alerts —
// this service intentionally has no background goroutine of its own.
type AlertRuleService struct {
	rules    AlertRuleRepo
	settings settingsGetter
	alerts   *AlertService
}

func NewAlertRuleService(rules AlertRuleRepo, settings settingsGetter, alerts *AlertService) *AlertRuleService {
	return &AlertRuleService{rules: rules, settings: settings, alerts: alerts}
}

func (s *AlertRuleService) validate(req UpsertAlertRuleRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(req.Expr) == "" {
		return errors.New("expr is required")
	}
	if req.ForSeconds < 0 {
		return errors.New("for_seconds must be >= 0")
	}
	if req.Level != "" && !validAlertLevel(req.Level) {
		return errors.New("level must be one of info, warning, critical")
	}
	return nil
}

func (s *AlertRuleService) Create(ctx context.Context, req UpsertAlertRuleRequest) (*domain.AlertRule, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	channelsJSON, err := domain.MarshalChannels(req.Channels)
	if err != nil {
		return nil, err
	}
	level := req.Level
	if level == "" {
		level = string(alertsdk.LevelWarning)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rule := &domain.AlertRule{
		Name:         strings.TrimSpace(req.Name),
		Expr:         req.Expr,
		ForSeconds:   req.ForSeconds,
		Level:        level,
		ChannelsJSON: channelsJSON,
		Enabled:      enabled,
		State:        domain.AlertRuleStateOK,
	}
	if err := s.rules.Create(ctx, rule); err != nil {
		return nil, err
	}
	return s.hydrate(rule), nil
}

func (s *AlertRuleService) Update(ctx context.Context, id int64, req UpsertAlertRuleRequest) (*domain.AlertRule, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	rule, err := s.rules.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	channelsJSON, err := domain.MarshalChannels(req.Channels)
	if err != nil {
		return nil, err
	}
	rule.Name = strings.TrimSpace(req.Name)
	rule.Expr = req.Expr
	rule.ForSeconds = req.ForSeconds
	if req.Level != "" {
		rule.Level = req.Level
	}
	rule.ChannelsJSON = channelsJSON
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if err := s.rules.Update(ctx, rule); err != nil {
		return nil, err
	}
	return s.hydrate(rule), nil
}

func (s *AlertRuleService) Get(ctx context.Context, id int64) (*domain.AlertRule, error) {
	rule, err := s.rules.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.hydrate(rule), nil
}

func (s *AlertRuleService) Delete(ctx context.Context, id int64) error {
	return s.rules.Delete(ctx, id)
}

func (s *AlertRuleService) List(ctx context.Context) ([]*domain.AlertRule, error) {
	rules, err := s.rules.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		s.hydrate(r)
	}
	return rules, nil
}

// hydrate populates the decoded Channels view on rule (best-effort — a
// malformed ChannelsJSON leaves Channels empty rather than failing the whole
// read, mirroring how AlertService.ListChannels skips malformed entries
// rather than erroring the whole list).
func (s *AlertRuleService) hydrate(rule *domain.AlertRule) *domain.AlertRule {
	if names, err := rule.ParseChannels(); err == nil {
		rule.Channels = names
	}
	return rule
}

// EvaluateNow runs rule.Expr immediately via promclient.Query and returns the
// raw result plus whether it would be considered "firing" (non-empty
// vector). Used by the /test endpoint — pure preview: does NOT touch
// ConsecutiveBreaches/State/LastFiredAt and does NOT send an alert.
func (s *AlertRuleService) EvaluateNow(ctx context.Context, id int64) (result *promclient.QueryResult, firing bool, err error) {
	rule, err := s.rules.GetByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	url, ok := s.settings.Get(prometheusURLSettingKey)
	if !ok || url == "" {
		return nil, false, errors.New(prometheusURLSettingKey + " is not configured")
	}
	res, err := promclient.Query(ctx, url, rule.Expr, time.Now())
	if err != nil {
		return nil, false, err
	}
	return res, len(res.Result) > 0, nil
}
