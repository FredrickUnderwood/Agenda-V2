package domain

import (
	"regexp"
	"strings"
	"time"
)

// ApplicationEnvTarget is one deployable instance of an Application within a
// given env (application_id, env, instance_name) unique. It is the unit that
// Release/deploy/health-check/gateway-route all key off.
type ApplicationEnvTarget struct {
	ID                          int64       `json:"id"                                   gorm:"primaryKey;autoIncrement"`
	ApplicationID               int64       `json:"application_id"                       gorm:"uniqueIndex:idx_app_env_target_app_env_instance;index;not null"`
	Env                         Environment `json:"env"                                  gorm:"uniqueIndex:idx_app_env_target_app_env_instance;index;size:16;not null"`
	InstanceName                string      `json:"instance_name"                        gorm:"uniqueIndex:idx_app_env_target_app_env_instance;size:64;not null;default:'default'"`
	DisplayName                 string      `json:"display_name"                         gorm:"size:128;not null;default:''"`
	MachineID                   int64       `json:"machine_id"                           gorm:"index:idx_app_env_target_machine_port;not null;default:0"`
	Port                        int         `json:"port"                                 gorm:"index:idx_app_env_target_machine_port;not null;default:0"`
	Enabled                     bool        `json:"enabled"                              gorm:"index:idx_app_env_target_health;not null;default:true"`
	HealthCheckEnabled          bool        `json:"health_check_enabled"                 gorm:"index:idx_app_env_target_health;not null;default:false"`
	HealthCheckType             string      `json:"health_check_type"                    gorm:"size:16;not null;default:'http'"`
	HealthCheckScheme           string      `json:"health_check_scheme"                  gorm:"size:16;not null;default:'http'"`
	HealthCheckHost             string      `json:"health_check_host"                    gorm:"size:255;not null;default:''"`
	HealthCheckURL              string      `json:"health_check_url"                     gorm:"size:1024;not null;default:''"`
	HealthCheckPath             string      `json:"health_check_path"                    gorm:"size:255;not null;default:'/healthz'"`
	HealthCheckMethod           string      `json:"health_check_method"                  gorm:"size:16;not null;default:'GET'"`
	HealthCheckExpectedStatus   int         `json:"health_check_expected_status"         gorm:"not null;default:200"`
	HealthCheckTimeoutMS        int         `json:"health_check_timeout_ms"              gorm:"not null;default:3000"`
	HealthCheckIntervalSec      int         `json:"health_check_interval_sec"            gorm:"not null;default:30"`
	HealthCheckFailureThreshold int         `json:"health_check_failure_threshold"       gorm:"not null;default:3"`
	HealthCheckSuccessThreshold int         `json:"health_check_success_threshold"       gorm:"not null;default:1"`
	// MetricsEnabled/MetricsPort opt this target into custom app metrics: when
	// enabled, the deploy pipeline injects AGENDA_METRICS_ADDR into every
	// service's container (sdk/go/metric listens there) and passes
	// APP_METRICS_PORT to `docker compose up` for the operator's own compose
	// file to publish, exactly mirroring how Port/APP_PORT already works.
	// Requires an agent-mode machine — Prometheus reaches this port only via
	// agenda-node's authenticated relay, same requirement application logs have.
	MetricsEnabled bool `json:"metrics_enabled" gorm:"not null;default:false"`
	MetricsPort    int  `json:"metrics_port"    gorm:"not null;default:0"`
	// EnvOverrideJSON is the instance-level env var override layer (highest
	// priority), merged on top of ApplicationEnvironment.EnvVars (env level)
	// and DockerDeployConfig.Env (application level).
	EnvOverrideJSON string    `json:"env_override_json" gorm:"type:text;not null"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	GatewayRoutes []*ApplicationGatewayRoute `json:"gateway_routes,omitempty" gorm:"-"`
	Health        *ApplicationInstanceHealth `json:"health,omitempty"         gorm:"-"`
}

func (ApplicationEnvTarget) TableName() string { return "application_env_target" }

// ParseEnvOverride decodes the instance-level env var override layer. Never
// returns a nil map, so callers can merge unconditionally.
func (t *ApplicationEnvTarget) ParseEnvOverride() (map[string]string, error) {
	if t == nil {
		return parseEnvMap("")
	}
	return parseEnvMap(t.EnvOverrideJSON)
}

const DefaultInstanceName = "default"

var instanceNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

func NormalizeInstanceName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return DefaultInstanceName
	}
	return name
}

func ValidInstanceName(name string) bool {
	return instanceNamePattern.MatchString(name)
}

type GatewayBackendMode string

const (
	GatewayBackendModeSingle     GatewayBackendMode = "single"
	GatewayBackendModeAllEnabled GatewayBackendMode = "all_enabled"
	GatewayBackendModeSelected   GatewayBackendMode = "selected"
)

type GatewayInstanceSelectMode string

const (
	GatewayInstanceSelectModeDisabled GatewayInstanceSelectMode = "disabled"
	GatewayInstanceSelectModeEnabled  GatewayInstanceSelectMode = "enabled"
)

const DefaultGatewayInstanceHeader = "X-Agenda-Instance"

// ApplicationGatewayRoute is one externally reachable route (host+path)
// pointing at one or more ApplicationEnvTarget backends. An instance can
// expose several routes (e.g. a C-end host and an admin-end host).
type ApplicationGatewayRoute struct {
	ID                 int64                     `json:"id"            gorm:"primaryKey;autoIncrement"`
	ApplicationID      int64                     `json:"application_id" gorm:"index:idx_app_gateway_app_env;not null;default:0"`
	Env                Environment               `json:"env"            gorm:"index:idx_app_gateway_app_env;size:16;not null"`
	RouteKey           string                    `json:"route_key"      gorm:"uniqueIndex:idx_app_gateway_route_key;size:128;not null"`
	Host               string                    `json:"host"           gorm:"uniqueIndex:idx_app_gateway_host_path;size:255;not null"`
	PathPrefix         string                    `json:"path_prefix"    gorm:"uniqueIndex:idx_app_gateway_host_path;size:255;not null;default:'/'"`
	StripPrefix        bool                      `json:"strip_prefix"   gorm:"not null;default:false"`
	BackendPath        string                    `json:"backend_path"   gorm:"size:255;not null;default:''"`
	Enabled            bool                      `json:"enabled"        gorm:"not null;default:true"`
	BackendMode        GatewayBackendMode        `json:"backend_mode"         gorm:"size:16;not null;default:single"`
	InstanceSelectMode GatewayInstanceSelectMode `json:"instance_select_mode" gorm:"size:16;not null;default:disabled"`
	InstanceHeader     string                    `json:"instance_header"      gorm:"size:64;not null;default:'X-Agenda-Instance'"`
	SortOrder          int                       `json:"sort_order"     gorm:"not null;default:0"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`

	Backends []*ApplicationGatewayRouteBackend `json:"backends,omitempty" gorm:"-"`
}

func (ApplicationGatewayRoute) TableName() string { return "application_gateway_route" }

// ApplicationGatewayRouteBackend pins a route (backend_mode=selected) to
// specific instances, each with its own weight/enabled toggle.
type ApplicationGatewayRouteBackend struct {
	ID        int64     `json:"id"         gorm:"primaryKey;autoIncrement"`
	RouteID   int64     `json:"route_id"   gorm:"uniqueIndex:idx_app_gateway_route_backend_route_target;not null;default:0"`
	TargetID  int64     `json:"target_id"  gorm:"uniqueIndex:idx_app_gateway_route_backend_route_target;not null;default:0"`
	Weight    int       `json:"weight"     gorm:"not null;default:1"`
	Enabled   bool      `json:"enabled"    gorm:"not null;default:true"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ApplicationGatewayRouteBackend) TableName() string {
	return "application_gateway_route_backend"
}
