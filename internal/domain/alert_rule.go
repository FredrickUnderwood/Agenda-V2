package domain

import (
	"time"

	"github.com/bytedance/sonic"
)

// AlertRuleState is the last-evaluated status of an AlertRule.
type AlertRuleState string

const (
	AlertRuleStateOK     AlertRuleState = "ok"
	AlertRuleStateFiring AlertRuleState = "firing"
)

// AlertRule is a user-defined PromQL condition that, when it crosses from
// not-breaching to breaching for ForSeconds, sends an alert via the existing
// AlertService/sdk-go-alert channels (Channels, not necessarily every
// configured channel) and records it in the shared notification inbox —
// mirrors how HealthCheck* fields on ApplicationEnvTarget gate a status
// transition, but driven by an arbitrary Prometheus query instead of an HTTP
// probe.
type AlertRule struct {
	ID   int64  `json:"id"   gorm:"primaryKey;autoIncrement"`
	Name string `json:"name" gorm:"uniqueIndex;size:128;not null"`
	// Expr is a raw PromQL instant-query expression; a non-empty result
	// vector means "breaching" (e.g. "rate(orders_failed_total[5m]) > 0.1").
	Expr string `json:"expr" gorm:"type:text;not null"`
	// ForSeconds is how long Expr must stay breaching, measured in
	// consecutive evaluation ticks (see AlertRuleMonitor), before the rule
	// actually fires — 0 fires on the very first breach. Deliberately not
	// real PromQL range-vector `for:` semantics: PromQL authors can already
	// bake windowing into Expr itself (e.g. rate(...)[5m]), so reimplementing
	// Prometheus's own `for:` evaluator server-side would just be redundant
	// complexity for a "lightweight" rule engine.
	ForSeconds int `json:"for_seconds" gorm:"not null;default:0"`
	// Level matches sdk/go/alert's Level (info/warning/critical).
	Level string `json:"level" gorm:"size:16;not null;default:warning"`
	// ChannelsJSON is a JSON array of alert channel names (Setting keys'
	// "<name>" segment) this rule fires to — passed as SendToAll's names
	// filter, NOT "every enabled channel", since a rule should be able to
	// page a specific on-call channel rather than broadcast everywhere.
	ChannelsJSON string `json:"-" gorm:"column:channels_json;type:text;not null"`
	Enabled      bool   `json:"enabled" gorm:"index;not null;default:true"`

	State               AlertRuleState `json:"state"                gorm:"size:16;not null;default:ok"`
	ConsecutiveBreaches int            `json:"consecutive_breaches" gorm:"not null;default:0"`
	LastEvaluatedAt     *time.Time     `json:"last_evaluated_at"`
	LastFiredAt         *time.Time     `json:"last_fired_at"`
	// LastError surfaces the last PromQL/HTTP evaluation failure directly in
	// the UI, so a broken query is visible without digging into server logs.
	LastError string `json:"last_error" gorm:"type:text;not null"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Channels is the decoded view of ChannelsJSON, populated for API
	// responses; never persisted directly (gorm:"-").
	Channels []string `json:"channels" gorm:"-"`
}

func (AlertRule) TableName() string { return "alert_rule" }

// ParseChannels decodes ChannelsJSON. Never returns a nil slice, so callers
// can range over it unconditionally.
func (r *AlertRule) ParseChannels() ([]string, error) {
	if r == nil || r.ChannelsJSON == "" {
		return []string{}, nil
	}
	var names []string
	if err := sonic.UnmarshalString(r.ChannelsJSON, &names); err != nil {
		return nil, err
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// MarshalChannels encodes names as the JSON array ChannelsJSON stores.
func MarshalChannels(names []string) (string, error) {
	if names == nil {
		names = []string{}
	}
	return sonic.MarshalString(names)
}
