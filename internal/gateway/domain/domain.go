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
	TimeoutMs          int                `json:"timeout_ms" gorm:"not null;default:30000"`
	Backends           []Backend          `json:"backends,omitempty" gorm:"-"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

func (Route) TableName() string { return "gateway_route" }

type Backend struct {
	ID           int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RouteID      int64     `json:"route_id" gorm:"uniqueIndex:idx_gateway_backend_route_release_target;index:idx_gateway_backend_route_release;not null"`
	ReleaseID    string    `json:"release_id" gorm:"uniqueIndex:idx_gateway_backend_route_release_target;index:idx_gateway_backend_route_release;size:128;not null"`
	TargetKey    string    `json:"target_key" gorm:"uniqueIndex:idx_gateway_backend_route_release_target;index;size:128;not null"`
	InstanceName string    `json:"instance_name" gorm:"size:64;not null;default:''"`
	URL          string    `json:"url" gorm:"size:512;not null"`
	Weight       int       `json:"weight" gorm:"not null;default:1"`
	Enabled      bool      `json:"enabled" gorm:"index:idx_gateway_backend_enabled_healthy;not null;default:true"`
	Healthy      bool      `json:"healthy" gorm:"index:idx_gateway_backend_enabled_healthy;not null;default:true"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
