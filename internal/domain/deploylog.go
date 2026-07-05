package domain

import "time"

type DeployStatus string

const (
	DeployStatusPending DeployStatus = "pending"
	DeployStatusRunning DeployStatus = "running"
	DeployStatusSuccess DeployStatus = "success"
	DeployStatusFailed  DeployStatus = "failed"
	DeployStatusPaused  DeployStatus = "paused"
)

// DeployLog is one physical pipeline execution. It is always driven by an
// ApplicationRelease (ReleaseID); there is no standalone/poller-triggered
// deploy path in this build — the release is the only trigger.
type DeployLog struct {
	ID             int64        `json:"id"              gorm:"primaryKey;autoIncrement"`
	ApplicationID  int64        `json:"application_id"  gorm:"index;not null"`
	ReleaseID      int64        `json:"release_id"      gorm:"index;not null;default:0"`
	Env            Environment  `json:"env"             gorm:"index;size:16;not null;default:prod"`
	DeployTargetID int64        `json:"deploy_target_id" gorm:"index;index:idx_deploy_log_target_instance;not null;default:0"`
	InstanceName   string       `json:"instance_name"    gorm:"index:idx_deploy_log_target_instance;size:64;not null;default:'default'"`
	MachineID      int64        `json:"machine_id"      gorm:"not null;default:0"`
	DeployPort     int          `json:"deploy_port"     gorm:"not null;default:0"`
	Branch         string       `json:"branch"          gorm:"size:128;not null;default:''"`
	TriggerSHA     string       `json:"trigger_sha"     gorm:"size:64;not null;default:''"`
	Status         DeployStatus `json:"status"          gorm:"size:16;not null;default:pending"`
	PauseRequested bool         `json:"pause_requested" gorm:"not null;default:false"`
	Output         string       `json:"output"          gorm:"type:text;not null"`
	ErrorMsg       string       `json:"error_msg"       gorm:"type:text;not null"`
	StartedAt      time.Time    `json:"started_at"      gorm:"index:idx_deploy_log_started,sort:desc;not null"`
	FinishedAt     *time.Time   `json:"finished_at"`
	DurationMs     int64        `json:"duration_ms"     gorm:"not null;default:0"`

	// Steps is populated by the service layer for API responses. Not persisted
	// on the deploy_log row itself; rows live in pipeline_step.
	Steps []*PipelineStep `json:"steps,omitempty" gorm:"-"`
}

func (DeployLog) TableName() string { return "deploy_log" }
