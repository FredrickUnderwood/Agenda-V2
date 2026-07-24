package domain

import "time"

type EnvDeploymentStatus string

const (
	EnvDeploymentStatusPending EnvDeploymentStatus = "pending"
	EnvDeploymentStatusRunning EnvDeploymentStatus = "running"
	EnvDeploymentStatusSuccess EnvDeploymentStatus = "success"
	// EnvDeploymentStatusPartial is a mixed terminal outcome: some instances
	// deployed successfully and at least one failed.
	EnvDeploymentStatusPartial EnvDeploymentStatus = "partial_failed"
	EnvDeploymentStatusFailed  EnvDeploymentStatus = "failed"
)

// DefaultBranch is the branch an env-wide deploy assumes when none is given.
const DefaultBranch = "master"

// EnvDeployment groups one environment-wide deploy: a single "deploy this
// branch to every enabled instance of (application, env)" intent fanned out to
// one ApplicationRelease + DeployLog per instance. It is the single "deploy
// record" surfaced in the UI for a whole-environment rollout.
//
// It is deliberately a thin grouping/summary layer: the per-instance releases
// and deploy logs it spawns keep their own independent per-instance locks,
// pipeline runs and rollback granularity, and each back-references this row via
// ApplicationRelease.EnvDeploymentID. Status is derived from those children
// (see EnvDeploymentService.Reconcile), never authored directly by the child
// runs — so it is race-free under parallel fan-out.
type EnvDeployment struct {
	ID            int64               `json:"id"             gorm:"primaryKey;autoIncrement"`
	ApplicationID int64               `json:"application_id" gorm:"index;not null"`
	Env           Environment         `json:"env"            gorm:"index;size:16;not null"`
	Branch        string              `json:"branch"         gorm:"size:128;not null;default:''"`
	CommitSHA     string              `json:"commit_sha"     gorm:"size:64;not null;default:''"`
	Operator      string              `json:"operator"       gorm:"size:128;not null;default:''"`
	Status        EnvDeploymentStatus `json:"status"         gorm:"size:16;not null;default:pending"`
	TotalCount    int                 `json:"total_count"    gorm:"not null;default:0"`
	SuccessCount  int                 `json:"success_count"  gorm:"not null;default:0"`
	FailedCount   int                 `json:"failed_count"   gorm:"not null;default:0"`
	StartedAt     time.Time           `json:"started_at"     gorm:"index:idx_env_deployment_started,sort:desc;not null"`
	FinishedAt    *time.Time          `json:"finished_at"`

	// Releases is the child per-instance release list, populated by the service
	// layer for API responses. Not a persisted column.
	Releases []*ApplicationRelease `json:"releases,omitempty" gorm:"-"`
}

func (EnvDeployment) TableName() string { return "env_deployment" }
