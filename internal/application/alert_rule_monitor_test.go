package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

type fakeAlertRuleRepo struct {
	mu    sync.Mutex
	rules map[int64]*domain.AlertRule
}

func (f *fakeAlertRuleRepo) ListEnabled(context.Context) ([]*domain.AlertRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.AlertRule, 0, len(f.rules))
	for _, r := range f.rules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeAlertRuleRepo) Update(_ context.Context, rule *domain.AlertRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules[rule.ID] = rule
	return nil
}

func (f *fakeAlertRuleRepo) get(id int64) *domain.AlertRule {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rules[id]
}

type fakeSettingsGetter map[string]string

func (f fakeSettingsGetter) Get(key string) (string, bool) {
	v, ok := f[key]
	return v, ok
}

// fakeSettingsReader satisfies service.SettingsReader (GetByPrefix) so a real
// *service.AlertService can be constructed and its SendToAll calls observed
// via a local webhook server, rather than mocking AlertService itself.
type fakeSettingsReader struct {
	prefix map[string]string
}

func (f fakeSettingsReader) GetByPrefix(prefix string) map[string]string {
	out := map[string]string{}
	for k, v := range f.prefix {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out[k] = v
		}
	}
	return out
}

// promStub serves a Prometheus instant-query response whose "breaching"
// state can be flipped between evaluateOnce calls, to drive the monitor's
// ok<->firing state machine across ticks.
type promStub struct {
	mu        sync.Mutex
	breaching bool
	queries   int
}

func (p *promStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		breaching := p.breaching
		p.queries++
		p.mu.Unlock()

		result := "[]"
		if breaching {
			result = `[{"metric":{"__name__":"up"},"value":[1,"1"]}]`
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":` + result + `}}`))
	}
}

func (p *promStub) setBreaching(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.breaching = v
}

type capturedWebhook struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Level   string `json:"level"`
}

func newTestMonitor(t *testing.T, rules map[int64]*domain.AlertRule, prom *promStub) (*AlertRuleMonitor, *fakeAlertRuleRepo, chan capturedWebhook) {
	t.Helper()
	promSrv := httptest.NewServer(prom.handler())
	t.Cleanup(promSrv.Close)

	captured := make(chan capturedWebhook, 10)
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg capturedWebhook
		_ = json.NewDecoder(r.Body).Decode(&msg)
		captured <- msg
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhookSrv.Close)

	settings := fakeSettingsReader{prefix: map[string]string{
		"alert.channel.custom.test": `{"webhook_url":"` + webhookSrv.URL + `","enabled":true}`,
	}}
	alertSvc := service.NewAlertService(settings, nil)

	repo := &fakeAlertRuleRepo{rules: rules}
	sg := fakeSettingsGetter{prometheusURLSettingKey: promSrv.URL}
	mon := NewAlertRuleMonitor(repo, sg, alertSvc, time.Second)
	return mon, repo, captured
}

func TestAlertRuleMonitor_FiresOnFirstBreach_NoForDuration(t *testing.T) {
	prom := &promStub{breaching: true}
	rule := &domain.AlertRule{ID: 1, Name: "r1", Expr: "up == 0", Level: "warning", Enabled: true, State: domain.AlertRuleStateOK, ChannelsJSON: `["test"]`}
	mon, repo, captured := newTestMonitor(t, map[int64]*domain.AlertRule{1: rule}, prom)

	mon.evaluateOnce()

	select {
	case msg := <-captured:
		if msg.Title != "r1" {
			t.Errorf("title = %q, want r1", msg.Title)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected an alert to be sent on first breach (for_seconds=0)")
	}

	got := repo.get(1)
	if got.State != domain.AlertRuleStateFiring {
		t.Errorf("state = %q, want firing", got.State)
	}
	if got.LastFiredAt == nil {
		t.Error("expected LastFiredAt to be set")
	}
}

func TestAlertRuleMonitor_RecoveryNotice_OnFiringToOK(t *testing.T) {
	prom := &promStub{breaching: false}
	rule := &domain.AlertRule{ID: 1, Name: "r1", Expr: "up == 0", Level: "warning", Enabled: true, State: domain.AlertRuleStateFiring, ChannelsJSON: `["test"]`}
	mon, repo, captured := newTestMonitor(t, map[int64]*domain.AlertRule{1: rule}, prom)

	mon.evaluateOnce()

	select {
	case msg := <-captured:
		if msg.Level != "info" {
			t.Errorf("recovery level = %q, want info", msg.Level)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a recovery notice on firing->ok transition")
	}

	got := repo.get(1)
	if got.State != domain.AlertRuleStateOK {
		t.Errorf("state = %q, want ok", got.State)
	}
}

func TestAlertRuleMonitor_ForSeconds_RequiresConsecutiveBreaches(t *testing.T) {
	prom := &promStub{breaching: true}
	// interval=1s, for_seconds=3s -> threshold=3 consecutive ticks.
	rule := &domain.AlertRule{ID: 1, Name: "r1", Expr: "up == 0", Level: "warning", ForSeconds: 3, Enabled: true, State: domain.AlertRuleStateOK, ChannelsJSON: `["test"]`}
	mon, repo, captured := newTestMonitor(t, map[int64]*domain.AlertRule{1: rule}, prom)

	mon.evaluateOnce() // breach 1
	if got := repo.get(1); got.State != domain.AlertRuleStateOK {
		t.Fatalf("state after 1st breach = %q, want still ok", got.State)
	}
	mon.evaluateOnce() // breach 2
	if got := repo.get(1); got.State != domain.AlertRuleStateOK {
		t.Fatalf("state after 2nd breach = %q, want still ok", got.State)
	}
	mon.evaluateOnce() // breach 3 -> fires
	if got := repo.get(1); got.State != domain.AlertRuleStateFiring {
		t.Fatalf("state after 3rd breach = %q, want firing", got.State)
	}

	select {
	case <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("expected an alert to be sent once threshold crossed")
	}
	select {
	case msg := <-captured:
		t.Fatalf("expected exactly one alert send, got extra: %+v", msg)
	default:
	}
}

func TestAlertRuleMonitor_QueryError_DoesNotResetBreachesOrState(t *testing.T) {
	// A Prometheus server that always 500s.
	promSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer promSrv.Close()

	rule := &domain.AlertRule{ID: 1, Name: "r1", Expr: "up == 0", Enabled: true, State: domain.AlertRuleStateOK, ConsecutiveBreaches: 2, ChannelsJSON: `["test"]`}
	repo := &fakeAlertRuleRepo{rules: map[int64]*domain.AlertRule{1: rule}}
	sg := fakeSettingsGetter{prometheusURLSettingKey: promSrv.URL}
	alertSvc := service.NewAlertService(fakeSettingsReader{}, nil)
	mon := NewAlertRuleMonitor(repo, sg, alertSvc, time.Second)

	mon.evaluateOnce()

	got := repo.get(1)
	if got.State != domain.AlertRuleStateOK {
		t.Errorf("state = %q, want unchanged ok", got.State)
	}
	if got.ConsecutiveBreaches != 2 {
		t.Errorf("consecutive_breaches = %d, want unchanged 2 (query error must not reset)", got.ConsecutiveBreaches)
	}
	if got.LastError == "" {
		t.Error("expected LastError to be recorded")
	}
}

func TestAlertRuleMonitor_NoPrometheusURL_SkipsWithoutError(t *testing.T) {
	rule := &domain.AlertRule{ID: 1, Name: "r1", Expr: "up", Enabled: true, State: domain.AlertRuleStateOK}
	repo := &fakeAlertRuleRepo{rules: map[int64]*domain.AlertRule{1: rule}}
	alertSvc := service.NewAlertService(fakeSettingsReader{}, nil)
	mon := NewAlertRuleMonitor(repo, fakeSettingsGetter{}, alertSvc, time.Second)

	mon.evaluateOnce() // must not panic; must not mutate rule state

	got := repo.get(1)
	if got.State != domain.AlertRuleStateOK || got.LastEvaluatedAt != nil {
		t.Errorf("rule was mutated despite no prometheus_url configured: %+v", got)
	}
}
