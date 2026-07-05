package domain

import "time"

type HealthStatus string

const (
	HealthStatusUnknown   HealthStatus = "unknown"
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

type ApplicationInstanceHealth struct {
	ID                   int64        `json:"id"                    gorm:"primaryKey;autoIncrement"`
	TargetID             int64        `json:"target_id"             gorm:"uniqueIndex:idx_app_instance_health_target;not null;default:0"`
	ApplicationID        int64        `json:"application_id"        gorm:"index:idx_app_instance_health_app_env;not null;default:0"`
	Env                  Environment  `json:"env"                   gorm:"index:idx_app_instance_health_app_env;size:16;not null;default:prod"`
	InstanceName         string       `json:"instance_name"         gorm:"size:64;not null;default:'default'"`
	Status               HealthStatus `json:"status"                gorm:"index:idx_app_instance_health_status;size:16;not null;default:unknown"`
	CheckedAt            *time.Time   `json:"checked_at"            gorm:"index:idx_app_instance_health_status"`
	HTTPStatus           int          `json:"http_status"           gorm:"not null;default:0"`
	LatencyMS            int          `json:"latency_ms"            gorm:"not null;default:0"`
	ErrorMsg             string       `json:"error_msg"             gorm:"type:text;not null"`
	ConsecutiveSuccesses int          `json:"consecutive_successes" gorm:"not null;default:0"`
	ConsecutiveFailures  int          `json:"consecutive_failures"  gorm:"not null;default:0"`
	LastSuccessAt        *time.Time   `json:"last_success_at"`
	LastFailureAt        *time.Time   `json:"last_failure_at"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

func (ApplicationInstanceHealth) TableName() string { return "application_instance_health" }
