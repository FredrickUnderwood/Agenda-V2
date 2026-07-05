package domain

import "time"

// ApplicationEnvironment holds env-level (application_id, env) config that
// sits between the application-level DockerDeployConfig.Env baseline and the
// instance-level ApplicationEnvTarget.EnvOverride. ConfigJSON is reserved,
// unstructured passthrough for future non-env-var environment config.
type ApplicationEnvironment struct {
	ID            int64       `json:"id"             gorm:"primaryKey;autoIncrement"`
	ApplicationID int64       `json:"application_id" gorm:"uniqueIndex:idx_app_environment_app_env;not null"`
	Env           Environment `json:"env"            gorm:"uniqueIndex:idx_app_environment_app_env;size:16;not null"`
	EnvVarsJSON   string      `json:"env_vars_json"  gorm:"type:text;not null"`
	ConfigJSON    string      `json:"config_json"    gorm:"type:text;not null"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

func (ApplicationEnvironment) TableName() string { return "application_environment" }

func (e *ApplicationEnvironment) ParseEnvVars() (map[string]string, error) {
	if e == nil {
		return parseEnvMap("")
	}
	return parseEnvMap(e.EnvVarsJSON)
}
