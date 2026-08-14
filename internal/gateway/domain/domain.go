package domain

import (
	"errors"
	"time"
)

type RouteStatus string

const (
	RouteStatusEnabled  RouteStatus = "enabled"
	RouteStatusDisabled RouteStatus = "disabled"
)

var (
	ErrInvalidParam        = errors.New("invalid parameter")
	ErrRouteNotFound       = errors.New("route not found")
	ErrRollbackUnavailable = errors.New("rollback unavailable")
)

type InvalidParamError struct {
	Message string
}

func (e InvalidParamError) Error() string {
	if e.Message == "" {
		return ErrInvalidParam.Error()
	}
	return e.Message
}

func NewInvalidParamError(message string) error {
	return InvalidParamError{Message: message}
}

func IsInvalidParam(err error) bool {
	var e InvalidParamError
	return errors.As(err, &e) || errors.Is(err, ErrInvalidParam)
}

type InstanceSelectMode string

const (
	InstanceSelectModeDisabled InstanceSelectMode = "disabled"
	InstanceSelectModeEnabled  InstanceSelectMode = "enabled"
)

const DefaultInstanceHeader = "X-Agenda-Instance"

// UpgradeMode gates HTTP protocol upgrades on a route. It is an explicit
// per-route allowlist rather than a global switch (the model Envoy uses for
// upgrade_configs): a route that is not meant to carry long-lived connections
// must not be turned into one by a client that simply sends an Upgrade header,
// because an upgraded request escapes the ordinary request timeout and holds a
// connection slot on both the gateway and the node relay.
type UpgradeMode string

const (
	// UpgradeModeNone rejects every Upgrade request on the route; plain HTTP is
	// proxied as before. It is the default for existing and new routes.
	UpgradeModeNone UpgradeMode = "none"
	// UpgradeModeWebSocket allows RFC 6455 `Upgrade: websocket` (HTTP/1.1) and
	// nothing else. HTTP/2 Extended CONNECT (RFC 8441) is not supported.
	UpgradeModeWebSocket UpgradeMode = "websocket"
)

// ValidUpgradeMode reports whether mode is a known upgrade mode. The empty
// string is not valid here — callers normalize it to UpgradeModeNone first.
func ValidUpgradeMode(mode UpgradeMode) bool {
	return mode == UpgradeModeNone || mode == UpgradeModeWebSocket
}

type Route struct {
	ID                 int64              `json:"id" gorm:"primaryKey;autoIncrement"`
	RouteKey           string             `json:"route_key" gorm:"uniqueIndex;size:128;not null"`
	ApplicationID      int64              `json:"application_id" gorm:"index:idx_gateway_route_app_env;not null;default:0"`
	ServiceName        string             `json:"service_name" gorm:"index:idx_gateway_route_service_env;size:128;not null"`
	Env                string             `json:"env" gorm:"index:idx_gateway_route_app_env;index:idx_gateway_route_service_env;size:16;not null"`
	Host               string             `json:"host" gorm:"uniqueIndex:idx_gateway_route_host_path;size:255;not null"`
	PathPrefix         string             `json:"path_prefix" gorm:"uniqueIndex:idx_gateway_route_host_path;size:255;not null;default:/"`
	StripPrefix        bool               `json:"strip_prefix" gorm:"not null;default:false"`
	CurrentReleaseID   string             `json:"current_release_id" gorm:"size:128;not null;default:''"`
	Status             RouteStatus        `json:"status" gorm:"index;size:16;not null;default:enabled"`
	InstanceSelectMode InstanceSelectMode `json:"instance_select_mode" gorm:"size:16;not null;default:disabled"`
	InstanceHeader     string             `json:"instance_header" gorm:"size:64;not null;default:'X-Agenda-Instance'"`
	// TimeoutMs bounds the whole lifetime of an ordinary HTTP request. It is
	// deliberately NOT applied to an upgraded (WebSocket) request: a total
	// deadline there is not a timeout but a scheduled disconnect — see
	// application.ServeProxy.
	TimeoutMs   int         `json:"timeout_ms" gorm:"not null;default:30000"`
	UpgradeMode UpgradeMode `json:"upgrade_mode" gorm:"size:16;not null;default:none"`
	// WebsocketIdleTimeoutMs closes a tunnel after this long with no bytes in
	// either direction. 0 means "use the gateway default"; a negative value
	// disables the idle timeout entirely (the connection then lives until one
	// side closes it, so the app must keep it honest with its own Ping/Pong).
	WebsocketIdleTimeoutMs int `json:"websocket_idle_timeout_ms" gorm:"not null;default:0"`
	// WebsocketMaxConnections caps concurrent established tunnels on this route
	// (0 = unlimited, subject to the gateway-wide cap). Handshakes past the cap
	// are rejected with 503.
	WebsocketMaxConnections int `json:"websocket_max_connections" gorm:"not null;default:0"`
	// WebsocketAllowedOrigins is a comma-separated Origin allowlist for browser
	// handshakes. Empty disables the check. Entries are matched
	// case-insensitively against the whole Origin value ("https://app.example.com");
	// a leading "*." matches one or more leading labels of the host.
	WebsocketAllowedOrigins string    `json:"websocket_allowed_origins" gorm:"size:1024;not null;default:''"`
	Backends                []Backend `json:"backends,omitempty" gorm:"-"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func (Route) TableName() string { return "gateway_route" }

type Backend struct {
	ID           int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	RouteID      int64  `json:"route_id" gorm:"uniqueIndex:idx_gateway_backend_route_release_target;index:idx_gateway_backend_route_release;not null"`
	ReleaseID    string `json:"release_id" gorm:"uniqueIndex:idx_gateway_backend_route_release_target;index:idx_gateway_backend_route_release;size:128;not null"`
	TargetKey    string `json:"target_key" gorm:"uniqueIndex:idx_gateway_backend_route_release_target;index;size:128;not null"`
	InstanceName string `json:"instance_name" gorm:"size:64;not null;default:''"`
	URL          string `json:"url" gorm:"size:512;not null"`
	Weight       int    `json:"weight" gorm:"not null;default:1"`
	// No `default:true` on these two bools: GORM omits a zero-value field from
	// INSERT when its schema has a default, letting the DB default apply — so an
	// explicit Enabled=false / Healthy=false would be silently stored as true,
	// keeping an unhealthy/disabled backend in the load-balancing pool. The
	// control plane always sets both explicitly, so the column default is
	// unnecessary and actively harmful here.
	Enabled   bool      `json:"enabled" gorm:"index:idx_gateway_backend_enabled_healthy;not null"`
	Healthy   bool      `json:"healthy" gorm:"index:idx_gateway_backend_enabled_healthy;not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Backend) TableName() string { return "gateway_backend" }

type RouteHistory struct {
	ID            int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RouteID       int64     `json:"route_id" gorm:"index:idx_gateway_route_history_route;index:idx_gateway_route_history_to_release;not null"`
	FromReleaseID string    `json:"from_release_id" gorm:"size:128;not null;default:''"`
	ToReleaseID   string    `json:"to_release_id" gorm:"index:idx_gateway_route_history_to_release;size:128;not null;default:''"`
	Operator      string    `json:"operator" gorm:"size:128;not null;default:''"`
	Reason        string    `json:"reason" gorm:"size:255;not null;default:''"`
	BackendsJSON  string    `json:"backends_json" gorm:"type:json"`
	CreatedAt     time.Time `json:"created_at"`
}

func (RouteHistory) TableName() string { return "gateway_route_history" }
