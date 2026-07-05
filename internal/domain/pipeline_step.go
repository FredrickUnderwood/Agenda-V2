package domain

import "time"

type StepStatus string

const (
	StepStatusPending StepStatus = "pending"
	StepStatusRunning StepStatus = "running"
	StepStatusSuccess StepStatus = "success"
	StepStatusFailed  StepStatus = "failed"
	StepStatusSkipped StepStatus = "skipped"
)

// Step type identifiers. The executor matches on these to dispatch behavior.
const (
	StepTypeGitPull            = "git_pull"
	StepTypePreCommands        = "pre_commands"
	StepTypeComposePull        = "compose_pull"
	StepTypeComposeUp          = "compose_up"
	StepTypeComposeHealthCheck = "compose_healthcheck"
	StepTypeGatewayRouteSync   = "gateway_route_sync"
	StepTypeAPIRequest         = "http_request"
)

// PipelineStep is one node in a deploy pipeline. Relation to DeployLog is by
// DeployLogID only; there is no DB-level foreign key.
type PipelineStep struct {
	ID          int64      `json:"id"             gorm:"primaryKey;autoIncrement"`
	DeployLogID int64      `json:"deploy_log_id"  gorm:"index;not null"`
	Idx         int        `json:"idx"            gorm:"not null;default:0"`
	Name        string     `json:"name"           gorm:"size:64;not null"`
	StepType    string     `json:"step_type"      gorm:"size:32;not null"`
	Status      StepStatus `json:"status"         gorm:"size:16;not null;default:pending"`
	Output      string     `json:"output"         gorm:"type:text;not null"`
	ErrorMsg    string     `json:"error_msg"      gorm:"type:text;not null"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	DurationMs  int64      `json:"duration_ms"    gorm:"not null;default:0"`
	Attempt     int        `json:"attempt"        gorm:"not null;default:1"`
}

func (PipelineStep) TableName() string { return "pipeline_step" }
