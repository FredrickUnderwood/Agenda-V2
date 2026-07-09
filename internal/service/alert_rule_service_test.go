package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

type fakeAlertRuleRepo struct {
	rules  map[int64]*domain.AlertRule
	nextID int64
}

func newFakeAlertRuleRepo() *fakeAlertRuleRepo {
	return &fakeAlertRuleRepo{rules: map[int64]*domain.AlertRule{}, nextID: 1}
}

func (f *fakeAlertRuleRepo) Create(_ context.Context, rule *domain.AlertRule) error {
	rule.ID = f.nextID
	f.nextID++
	f.rules[rule.ID] = rule
	return nil
}
func (f *fakeAlertRuleRepo) Update(_ context.Context, rule *domain.AlertRule) error {
	if _, ok := f.rules[rule.ID]; !ok {
		return errors.New("not found")
	}
	f.rules[rule.ID] = rule
	return nil
}
func (f *fakeAlertRuleRepo) GetByID(_ context.Context, id int64) (*domain.AlertRule, error) {
	r, ok := f.rules[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}
func (f *fakeAlertRuleRepo) Delete(_ context.Context, id int64) error {
	delete(f.rules, id)
	return nil
}
func (f *fakeAlertRuleRepo) List(_ context.Context) ([]*domain.AlertRule, error) {
	out := make([]*domain.AlertRule, 0, len(f.rules))
	for _, r := range f.rules {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeAlertRuleRepo) ListEnabled(_ context.Context) ([]*domain.AlertRule, error) {
	out := make([]*domain.AlertRule, 0)
	for _, r := range f.rules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

type fakeSettingsGetter map[string]string

func (f fakeSettingsGetter) Get(key string) (string, bool) {
	v, ok := f[key]
	return v, ok
}

func TestAlertRuleService_Create_ValidatesRequiredFields(t *testing.T) {
	svc := NewAlertRuleService(newFakeAlertRuleRepo(), fakeSettingsGetter{}, nil)

	if _, err := svc.Create(context.Background(), UpsertAlertRuleRequest{Expr: "up"}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := svc.Create(context.Background(), UpsertAlertRuleRequest{Name: "r1"}); err == nil {
		t.Fatal("expected error for missing expr")
	}
	if _, err := svc.Create(context.Background(), UpsertAlertRuleRequest{Name: "r1", Expr: "up", Level: "bogus"}); err == nil {
		t.Fatal("expected error for invalid level")
	}
}

func TestAlertRuleService_Create_DefaultsLevelAndEnabled(t *testing.T) {
	svc := NewAlertRuleService(newFakeAlertRuleRepo(), fakeSettingsGetter{}, nil)
	rule, err := svc.Create(context.Background(), UpsertAlertRuleRequest{Name: "r1", Expr: "up == 0", Channels: []string{"oncall"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rule.Level != "warning" {
		t.Errorf("level = %q, want warning", rule.Level)
	}
	if !rule.Enabled {
		t.Error("expected rule enabled by default")
	}
	if len(rule.Channels) != 1 || rule.Channels[0] != "oncall" {
		t.Errorf("channels = %v", rule.Channels)
	}
}

func TestAlertRuleService_Update_RoundTripsChannels(t *testing.T) {
	repo := newFakeAlertRuleRepo()
	svc := NewAlertRuleService(repo, fakeSettingsGetter{}, nil)
	rule, err := svc.Create(context.Background(), UpsertAlertRuleRequest{Name: "r1", Expr: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(context.Background(), rule.ID, UpsertAlertRuleRequest{
		Name: "r1", Expr: "up == 0", Channels: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Channels) != 2 || updated.Channels[0] != "a" || updated.Channels[1] != "b" {
		t.Errorf("channels = %v", updated.Channels)
	}

	got, err := svc.Get(context.Background(), rule.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Channels) != 2 {
		t.Errorf("persisted channels = %v", got.Channels)
	}
}

func TestAlertRuleService_EvaluateNow_NoPrometheusURL(t *testing.T) {
	repo := newFakeAlertRuleRepo()
	svc := NewAlertRuleService(repo, fakeSettingsGetter{}, nil)
	rule, _ := svc.Create(context.Background(), UpsertAlertRuleRequest{Name: "r1", Expr: "up"})

	if _, _, err := svc.EvaluateNow(context.Background(), rule.ID); err == nil {
		t.Fatal("expected error when observability.prometheus_url is unset")
	}
}

func TestAlertRuleService_EvaluateNow_ReportsFiring(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1,"1"]}]}}`))
	}))
	defer prom.Close()

	repo := newFakeAlertRuleRepo()
	settings := fakeSettingsGetter{prometheusURLSettingKey: prom.URL}
	svc := NewAlertRuleService(repo, settings, nil)
	rule, _ := svc.Create(context.Background(), UpsertAlertRuleRequest{Name: "r1", Expr: "up == 0"})

	result, firing, err := svc.EvaluateNow(context.Background(), rule.ID)
	if err != nil {
		t.Fatalf("EvaluateNow: %v", err)
	}
	if !firing {
		t.Error("expected firing = true for non-empty result vector")
	}
	if len(result.Result) != 1 {
		t.Errorf("result.Result = %v", result.Result)
	}

	// EvaluateNow is a pure preview — must not mutate persisted state.
	persisted, _ := repo.GetByID(context.Background(), rule.ID)
	if persisted.State != domain.AlertRuleStateOK {
		t.Errorf("state = %q, want unchanged %q", persisted.State, domain.AlertRuleStateOK)
	}
	if persisted.ConsecutiveBreaches != 0 {
		t.Errorf("consecutive_breaches = %d, want 0 (EvaluateNow must not persist)", persisted.ConsecutiveBreaches)
	}
}
