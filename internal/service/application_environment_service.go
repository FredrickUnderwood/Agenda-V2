package service

import (
	"context"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

type ApplicationEnvironmentService struct {
	envs *repository.ApplicationEnvironmentRepository
}

func NewApplicationEnvironmentService(envs *repository.ApplicationEnvironmentRepository) *ApplicationEnvironmentService {
	return &ApplicationEnvironmentService{envs: envs}
}

// Get returns the env-level config row, or a zero-value (unpersisted,
// ID=0) row if none has been configured yet — GET endpoints treat "no
// overrides configured" as a valid, empty state rather than a 404.
func (s *ApplicationEnvironmentService) Get(ctx context.Context, appID int64, env domain.Environment) (*domain.ApplicationEnvironment, error) {
	row, err := s.envs.GetByApplicationEnv(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &domain.ApplicationEnvironment{ApplicationID: appID, Env: env}, nil
	}
	return row, nil
}

// GetEnvVars is the merge-time accessor used by the pipeline builder: empty
// map (never nil, never an error) when no env-level config exists.
func (s *ApplicationEnvironmentService) GetEnvVars(ctx context.Context, appID int64, env domain.Environment) (map[string]string, error) {
	row, err := s.envs.GetByApplicationEnv(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	return row.ParseEnvVars()
}

type UpsertApplicationEnvironmentRequest struct {
	EnvVars map[string]string `json:"env_vars"`
	Config  string            `json:"config_json"`
}

func (s *ApplicationEnvironmentService) Upsert(ctx context.Context, appID int64, env domain.Environment, req UpsertApplicationEnvironmentRequest) (*domain.ApplicationEnvironment, error) {
	envVarsJSON, err := marshalEnvOverride(req.EnvVars)
	if err != nil {
		return nil, err
	}
	row := &domain.ApplicationEnvironment{
		ApplicationID: appID,
		Env:           env,
		EnvVarsJSON:   envVarsJSON,
		ConfigJSON:    req.Config,
	}
	if err := s.envs.Upsert(ctx, row); err != nil {
		return nil, err
	}
	logStruct("application environment updated", row)
	return row, nil
}
