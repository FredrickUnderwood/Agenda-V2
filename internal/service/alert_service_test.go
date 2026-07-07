package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
	alertsdk "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"
)

type fakeSettingsReader map[string]string

func (f fakeSettingsReader) GetByPrefix(prefix string) map[string]string {
	out := make(map[string]string)
	for k, v := range f {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out[k] = v
		}
	}
	return out
}

func TestAlertService_ListChannels_ParsesWellFormedKeys(t *testing.T) {
	reader := fakeSettingsReader{
		"alert.channel.feishu.ops-alerts": `{"webhook_url":"https://feishu.example/x","secret":"s1","enabled":true}`,
		"alert.channel.dingtalk.oncall":   `{"webhook_url":"https://dingtalk.example/y","enabled":false}`,
		"unrelated.setting":               `should not appear`,
	}
	svc := NewAlertService(reader, nil)
	channels := svc.ListChannels()

	sort.Slice(channels, func(i, j int) bool { return channels[i].Name < channels[j].Name })
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d: %+v", len(channels), channels)
	}
	if channels[0].Name != "oncall" || channels[0].Type != alertsdk.ChannelDingTalk || channels[0].Enabled {
		t.Errorf("channels[0] = %+v", channels[0])
	}
	if channels[1].Name != "ops-alerts" || channels[1].Type != alertsdk.ChannelFeishu || !channels[1].Enabled || channels[1].Secret != "s1" {
		t.Errorf("channels[1] = %+v", channels[1])
	}
}

func TestAlertService_ListChannels_SkipsMalformedKeysAndValues(t *testing.T) {
	reader := fakeSettingsReader{
		"alert.channel.feishu":          `{"webhook_url":"x","enabled":true}`, // missing .<name> segment
		"alert.channel.feishu.bad-json": `not json`,
		"alert.channel.feishu.good":     `{"webhook_url":"https://ok","enabled":true}`,
	}
	svc := NewAlertService(reader, nil)
	channels := svc.ListChannels()
	if len(channels) != 1 || channels[0].Name != "good" {
		t.Fatalf("expected only the well-formed channel to survive, got %+v", channels)
	}
}

func TestAlertService_SendToAll_SkipsDisabledChannels(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reader := fakeSettingsReader{
		"alert.channel.custom.enabled-one":  `{"webhook_url":"` + srv.URL + `","enabled":true}`,
		"alert.channel.custom.disabled-one": `{"webhook_url":"` + srv.URL + `","enabled":false}`,
	}
	svc := NewAlertService(reader, nil)
	results := svc.SendToAll(context.Background(), alertsdk.Message{Title: "t"})

	if len(results) != 1 || results[0].Channel != "enabled-one" {
		t.Fatalf("expected only enabled-one to be sent to, got %+v", results)
	}
	if hits != 1 {
		t.Errorf("expected exactly 1 HTTP hit, got %d", hits)
	}
}

func TestAlertService_SendToAll_FiltersByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reader := fakeSettingsReader{
		"alert.channel.custom.a": `{"webhook_url":"` + srv.URL + `","enabled":true}`,
		"alert.channel.custom.b": `{"webhook_url":"` + srv.URL + `","enabled":true}`,
	}
	svc := NewAlertService(reader, nil)
	results := svc.SendToAll(context.Background(), alertsdk.Message{Title: "t"}, "b")

	if len(results) != 1 || results[0].Channel != "b" {
		t.Fatalf("expected only channel 'b', got %+v", results)
	}
}

func TestAlertService_SendToAll_PartialFailureReportedPerChannel(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	reader := fakeSettingsReader{
		"alert.channel.custom.good": `{"webhook_url":"` + good.URL + `","enabled":true}`,
		"alert.channel.custom.bad":  `{"webhook_url":"` + bad.URL + `","enabled":true}`,
	}
	svc := NewAlertService(reader, nil)
	results := svc.SendToAll(context.Background(), alertsdk.Message{Title: "t"})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	byName := map[string]error{}
	for _, r := range results {
		byName[r.Channel] = r.Err
	}
	if byName["good"] != nil {
		t.Errorf("good channel should have succeeded, got %v", byName["good"])
	}
	if byName["bad"] == nil {
		t.Error("bad channel should have failed")
	}
}

// fakeSettingRepository is an in-memory stand-in for the DB, implementing the
// same SettingRepository interface production code depends on — so this test
// exercises the REAL SettingService (real AES-256-GCM encryption via
// secret.Box, real in-memory cache), not just AlertService's own parsing.
type fakeSettingRepository struct {
	mu    sync.Mutex
	items map[string]*domain.Setting
}

func newFakeSettingRepository() *fakeSettingRepository {
	return &fakeSettingRepository{items: make(map[string]*domain.Setting)}
}

func (r *fakeSettingRepository) List(context.Context) ([]*domain.Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.Setting, 0, len(r.items))
	for _, v := range r.items {
		cp := *v
		out = append(out, &cp)
	}
	return out, nil
}

func (r *fakeSettingRepository) GetByKey(_ context.Context, key string) (*domain.Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.items[key]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *v
	return &cp, nil
}

func (r *fakeSettingRepository) Upsert(_ context.Context, s *domain.Setting) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.items[s.Key] = &cp
	return nil
}

func (r *fakeSettingRepository) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, key)
	return nil
}

// TestAlertService_EndToEnd_WithRealSettingService proves the whole pipeline
// a real deployment relies on: PUT-equivalent Set() -> AES-256-GCM encryption
// at rest -> process-restart-equivalent Load() -> decrypt -> AlertService
// parses the "alert.channel.*" key -> sdk/go/alert posts real JSON over real
// HTTP to the configured webhook. Every other test in this file bypasses
// SettingService entirely (fakeSettingsReader), so this is the only one that
// would catch a break in the actual encrypt/decrypt/cache round-trip.
func TestAlertService_EndToEnd_WithRealSettingService(t *testing.T) {
	var capturedBody map[string]any
	catcher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer catcher.Close()

	repo := newFakeSettingRepository()
	box := secret.NewBox("test-master-key-for-alert-e2e")
	settingSvc := NewSettingService(repo, box)

	channelJSON := `{"webhook_url":"` + catcher.URL + `","enabled":true}`
	if _, err := settingSvc.Set(context.Background(), SetSettingRequest{
		Key:      "alert.channel.custom.smoke-test",
		Value:    channelJSON,
		Type:     domain.SettingTypeJSON,
		IsSecret: true,
	}, 1); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Simulate a fresh process boot: reload the cache from the (fake) DB,
	// forcing a real decrypt of what was actually persisted.
	if err := settingSvc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The persisted row must actually be encrypted, not stored in plaintext.
	stored, err := repo.GetByKey(context.Background(), "alert.channel.custom.smoke-test")
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if stored.Value == channelJSON {
		t.Fatal("expected the persisted value to be encrypted, got plaintext")
	}
	if !secret.IsEncrypted(stored.Value) {
		t.Fatalf("expected an enc:v1: prefixed value, got %q", stored.Value)
	}

	alertSvc := NewAlertService(settingSvc, nil)
	results := alertSvc.SendToAll(context.Background(), alertsdk.Message{Title: "smoke", Content: "test", Level: alertsdk.LevelInfo})

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("expected 1 successful result, got %+v", results)
	}
	if capturedBody["title"] != "smoke" || capturedBody["content"] != "test" || capturedBody["level"] != "info" {
		t.Fatalf("webhook catcher received unexpected body: %+v", capturedBody)
	}
}

type fakeNotificationWriter struct {
	mu      sync.Mutex
	created []*domain.Notification
}

func (f *fakeNotificationWriter) Create(_ context.Context, n *domain.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, n)
	return nil
}

func TestAlertService_SendToAll_RecordsInboxEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reader := fakeSettingsReader{
		"alert.channel.custom.a": `{"webhook_url":"` + srv.URL + `","enabled":true}`,
	}
	writer := &fakeNotificationWriter{}
	svc := NewAlertService(reader, writer)
	svc.SendToAll(context.Background(), alertsdk.Message{Title: "disk full", Content: "90% used", Level: alertsdk.LevelCritical})

	if len(writer.created) != 1 {
		t.Fatalf("expected 1 notification recorded, got %d", len(writer.created))
	}
	n := writer.created[0]
	if n.Title != "disk full" || n.Content != "90% used" || n.Level != "critical" {
		t.Errorf("recorded notification = %+v", n)
	}
}

func TestAlertService_SendToAll_NoChannelsStillRecordsInboxEntry(t *testing.T) {
	writer := &fakeNotificationWriter{}
	svc := NewAlertService(fakeSettingsReader{}, writer)
	svc.SendToAll(context.Background(), alertsdk.Message{Title: "t"})

	if len(writer.created) != 1 {
		t.Fatalf("expected the inbox to record the alert even with zero external channels, got %d entries", len(writer.created))
	}
}
