package domain

import (
	"time"

	"github.com/bytedance/sonic"
)

type DeployMethod string

const (
	DeployMethodDocker DeployMethod = "docker"
	DeployMethodAPI    DeployMethod = "api"
)

// DockerDeployConfig is the docker-method payload stored as JSON in
// Application.DeployConfig.
type DockerDeployConfig struct {
	MachineID        int64                    `json:"machine_id,omitempty"`
	Machine          string                   `json:"machine,omitempty"`
	WorkDir          string                   `json:"work_dir"`
	ComposeFile      string                   `json:"compose_file,omitempty"`
	Services         []string                 `json:"services,omitempty"`
	PullBeforeDeploy bool                     `json:"pull_before_deploy"`
	PreCommands      []string                 `json:"pre_commands,omitempty"`
	HealthCheck      *DockerHealthCheckConfig `json:"health_check,omitempty"`
	// Env is the application-level env var baseline injected into every
	// target service. Overridden by ApplicationEnvironment.EnvVars (env
	// level) and then ApplicationEnvTarget.EnvOverride (instance level).
	Env map[string]string `json:"env,omitempty"`
}

type DockerHealthCheckConfig struct {
	Enabled         *bool `json:"enabled,omitempty"`
	TimeoutSeconds  int   `json:"timeout_seconds,omitempty"`
	IntervalSeconds int   `json:"interval_seconds,omitempty"`
	RequireHealthy  bool  `json:"require_healthy,omitempty"`
}

// APIDeployConfig is the api-method payload stored as JSON in
// Application.DeployConfig.
type APIDeployConfig struct {
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	ExpectedStatus int               `json:"expected_status,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// Application is the top-level deployable unit. There is no default branch
// on Application itself — branch is supplied per Release (see
// ApplicationRelease), since a single application can be released from
// different branches over time.
type Application struct {
	ID           int64        `json:"id"            gorm:"primaryKey;autoIncrement"`
	Name         string       `json:"name"          gorm:"uniqueIndex;size:128;not null"`
	RepoURL      string       `json:"repo_url"      gorm:"size:512;not null"`
	DeployMethod DeployMethod `json:"deploy_method" gorm:"size:16;not null"`
	DeployConfig string       `json:"deploy_config" gorm:"type:text;not null"`
	Description  string       `json:"description"   gorm:"type:text;not null"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`

	Targets []*ApplicationEnvTarget `json:"targets,omitempty" gorm:"-"`
}

func (Application) TableName() string { return "application" }

func (a *Application) ParseDockerConfig() (*DockerDeployConfig, error) {
	var cfg DockerDeployConfig
	if err := sonic.UnmarshalString(a.DeployConfig, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (a *Application) ParseAPIConfig() (*APIDeployConfig, error) {
	var cfg APIDeployConfig
	if err := sonic.UnmarshalString(a.DeployConfig, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
