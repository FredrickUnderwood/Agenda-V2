package domain

import "time"

// ReleaseStatus is the lifecycle of one deploy intent. Transitions:
//
//	draft -> deploying -> pending_verify -> verified
//	              \-> failed -> deploying (retry reuses the same deploy_log)
//	verified -> rolling_back (a later release starts rolling back to/from this one)
//	rolling_back -> rolled_back (the replacing release reached pending_verify)
//
// rolling_back/rolled_back only ever appear on the release being superseded;
// the new release created by a rollback goes through the normal
// draft -> deploying -> pending_verify -> verified path like any other release.
type ReleaseStatus string

const (
	ReleaseStatusDraft         ReleaseStatus = "draft"
	ReleaseStatusDeploying     ReleaseStatus = "deploying"
	ReleaseStatusPendingVerify ReleaseStatus = "pending_verify"
	ReleaseStatusVerified      ReleaseStatus = "verified"
	ReleaseStatusFailed        ReleaseStatus = "failed"
	ReleaseStatusRollingBack   ReleaseStatus = "rolling_back"
	ReleaseStatusRolledBack    ReleaseStatus = "rolled_back"
)

// ApplicationRelease is one append-only deploy record for an
// (application_id, env, instance_name). "Current live version" is resolved
// as the most recent row with status=verified for that key. No DB foreign
// keys — DeployLogID/PreviousReleaseID are plain int64 references resolved
// in the service layer.
type ApplicationRelease struct {
	ID            int64       `json:"id"                     gorm:"primaryKey;autoIncrement"`
	ApplicationID int64       `json:"application_id"         gorm:"index:idx_app_release_app_env_instance;not null"`
	Env           Environment `json:"env"                    gorm:"index:idx_app_release_app_env_instance;size:16;not null"`
	InstanceName  string      `json:"instance_name"          gorm:"index:idx_app_release_app_env_instance;size:64;not null;default:'default'"`
	MachineID     int64       `json:"machine_id"             gorm:"not null;default:0"`
	// EnvDeploymentID back-references the env-wide deploy batch that spawned
	// this release, or 0 for a standalone single-instance release. See
	// EnvDeployment.
	EnvDeploymentID      int64         `json:"env_deployment_id"      gorm:"index;not null;default:0"`
	Branch               string        `json:"branch"                 gorm:"size:128;not null;default:'master'"`
	CommitSHA            string        `json:"commit_sha"             gorm:"size:64;not null;default:''"`
	PreviousReleaseID    int64         `json:"previous_release_id"    gorm:"not null;default:0"`
	PreviousCommitSHA    string        `json:"previous_commit_sha"    gorm:"size:64;not null;default:''"`
	DeployLogID          int64         `json:"deploy_log_id"          gorm:"index;not null;default:0"`
	Status               ReleaseStatus `json:"status"                 gorm:"index:idx_app_release_status;size:16;not null;default:draft"`
	DeployConfigSnapshot string        `json:"deploy_config_snapshot" gorm:"type:text;not null"`
	EnvConfigSnapshot    string        `json:"env_config_snapshot"    gorm:"type:text;not null"`
	RouteSnapshot        string        `json:"route_snapshot"         gorm:"type:text;not null"`
	Operator             string        `json:"operator"               gorm:"size:128;not null;default:''"`
	DeployedAt           *time.Time    `json:"deployed_at"`
	VerifiedAt           *time.Time    `json:"verified_at"`
	CreatedAt            time.Time     `json:"created_at"`
	UpdatedAt            time.Time     `json:"updated_at"`
}

func (ApplicationRelease) TableName() string { return "application_release" }
