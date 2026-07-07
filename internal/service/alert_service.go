package service

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	alertsdk "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"
)

// alertChannelKeyPrefix is the Setting key namespace for alert channels:
// "alert.channel.<type>.<name>", e.g. "alert.channel.feishu.ops-alerts".
// The value is a JSON blob {"webhook_url","secret","enabled"} — type comes
// from the key, not duplicated in the value. Configured today through the
// existing generic PUT /api/v1/settings/:key (type: json, is_secret: true) —
// no dedicated CRUD is needed for this.
const alertChannelKeyPrefix = "alert.channel."

// alertChannelValue is the JSON shape stored in a Setting's Value column.
type alertChannelValue struct {
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret,omitempty"`
	Enabled    bool   `json:"enabled"`
}

// SettingsReader is the narrow accessor AlertService needs from
// SettingService — mirrors GitToken/SecretValues, SettingService's existing
// sanctioned surface for other code to consume decrypted values without
// duplicating its decrypt+cache logic. Defined as an interface (rather than
// depending on *SettingService directly) purely for unit-testability, the
// same shape as MachineService's MachineRepo interface.
type SettingsReader interface {
	GetByPrefix(prefix string) map[string]string
}

// NotificationWriter is the narrow persistence capability AlertService needs
// to record every alert it sends into the in-app inbox. Satisfied directly by
// *repository.NotificationRepository — this is plain data assembly (writing a
// record alongside sending external alerts), the same reasoning
// ApplicationReleaseService documents for reading other domains via their
// repositories rather than their services, not a service-to-service call.
type NotificationWriter interface {
	Create(ctx context.Context, n *domain.Notification) error
}

// AlertService sends ops alerts to whatever channels are configured in
// Settings, via sdk/go/alert. It holds no state of its own — channels are
// re-read from SettingsReader on every call, so a Setting change takes effect
// immediately. notifications may be nil (inbox recording is then skipped) —
// kept optional so tests that only care about channel dispatch don't need a
// fake for it.
type AlertService struct {
	settings      SettingsReader
	notifications NotificationWriter
}

func NewAlertService(settings SettingsReader, notifications NotificationWriter) *AlertService {
	return &AlertService{settings: settings, notifications: notifications}
}

// ListChannels parses every "alert.channel.*" setting into an alertsdk.Channel.
// A malformed key or value is logged and skipped rather than failing the
// whole list — one bad entry shouldn't block sending to the others.
func (s *AlertService) ListChannels() []alertsdk.Channel {
	raw := s.settings.GetByPrefix(alertChannelKeyPrefix)
	channels := make([]alertsdk.Channel, 0, len(raw))
	for key, value := range raw {
		rest := strings.TrimPrefix(key, alertChannelKeyPrefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			logger.L().Warn("skipping malformed alert channel key", zap.String("key", key))
			continue
		}
		typ, name := parts[0], parts[1]

		var v alertChannelValue
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			logger.L().Warn("skipping alert channel with invalid JSON value", zap.String("key", key), zap.Error(err))
			continue
		}
		channels = append(channels, alertsdk.Channel{
			Type:       alertsdk.ChannelType(typ),
			Name:       name,
			WebhookURL: v.WebhookURL,
			Secret:     v.Secret,
			Enabled:    v.Enabled,
		})
	}
	return channels
}

// SendToAll sends msg to every enabled configured channel, and — regardless
// of whether any external channel is configured or succeeds — records it in
// the in-app inbox, so there's always a durable copy even if every webhook
// silently failed. When names is non-empty, only channels whose Name is in
// that set are sent to (still subject to Enabled). Returns one Result per
// external channel actually attempted (the inbox write is best-effort and
// not reflected here — a DB hiccup shouldn't be reported as an alert-send
// failure when the actual alerts already went out).
func (s *AlertService) SendToAll(ctx context.Context, msg alertsdk.Message, names ...string) []alertsdk.Result {
	var want map[string]bool
	if len(names) > 0 {
		want = make(map[string]bool, len(names))
		for _, n := range names {
			want[n] = true
		}
	}

	targets := make([]alertsdk.Channel, 0)
	for _, ch := range s.ListChannels() {
		if !ch.Enabled {
			continue
		}
		if want != nil && !want[ch.Name] {
			continue
		}
		targets = append(targets, ch)
	}
	results := alertsdk.SendAll(ctx, targets, msg)

	if s.notifications != nil {
		n := &domain.Notification{Title: msg.Title, Content: msg.Content, Level: string(msg.Level)}
		if err := s.notifications.Create(ctx, n); err != nil {
			logger.L().Error("failed to record alert in inbox", zap.Error(err))
		}
	}
	return results
}
