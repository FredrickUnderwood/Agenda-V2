package service

import (
	"context"
	"fmt"
	"strings"

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
	if err := validateEnvVars(req.EnvVars); err != nil {
		return nil, err
	}
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

// ApplicationEnvironments is the all-environments view of one application's
// env vars: exactly one entry per domain.AllEnvironments(), so the UI can
// render a Key × (prod, stage, test) matrix without inferring which
// environments exist. An environment with nothing configured is an empty map,
// never nil.
type ApplicationEnvironments struct {
	ApplicationID int64                                    `json:"application_id"`
	Envs          map[domain.Environment]map[string]string `json:"envs"`
}

// GetAll returns every environment's env var map for one application.
func (s *ApplicationEnvironmentService) GetAll(ctx context.Context, appID int64) (*ApplicationEnvironments, error) {
	rows, err := s.envs.ListByApplication(ctx, appID)
	if err != nil {
		return nil, err
	}
	byEnv := make(map[domain.Environment]map[string]string, len(domain.AllEnvironments()))
	for _, env := range domain.AllEnvironments() {
		byEnv[env] = map[string]string{}
	}
	for _, row := range rows {
		vars, err := row.ParseEnvVars()
		if err != nil {
			return nil, err
		}
		// Ignore rows for environments that are no longer part of the enum, so
		// a stale row can't produce a column the UI doesn't know how to render.
		if _, ok := byEnv[row.Env]; !ok {
			continue
		}
		byEnv[row.Env] = vars
	}
	return &ApplicationEnvironments{ApplicationID: appID, Envs: byEnv}, nil
}

// UpsertApplicationEnvironmentsRequest is a full replacement of the named
// environments' env var maps. Environments absent from Envs are left untouched;
// an environment present with an empty map has its vars cleared.
//
// An empty value is stored verbatim as an empty string — there is no
// inheritance between environments, so a blank box in the UI means "this
// environment sets the key to an empty value", not "fall back to prod".
type UpsertApplicationEnvironmentsRequest struct {
	Envs map[domain.Environment]map[string]string `json:"envs"`
}

func (s *ApplicationEnvironmentService) UpsertAll(ctx context.Context, appID int64, req UpsertApplicationEnvironmentsRequest) (*ApplicationEnvironments, error) {
	for env, vars := range req.Envs {
		if !env.Valid() {
			return nil, fmt.Errorf("invalid env %q", env)
		}
		if err := validateEnvVars(vars); err != nil {
			return nil, fmt.Errorf("%s: %w", env, err)
		}
	}
	for _, env := range domain.AllEnvironments() {
		vars, ok := req.Envs[env]
		if !ok {
			continue
		}
		// Carry the existing ConfigJSON through: Upsert rewrites both columns,
		// and this path only means to replace the env var map.
		existing, err := s.Get(ctx, appID, env)
		if err != nil {
			return nil, err
		}
		if _, err := s.Upsert(ctx, appID, env, UpsertApplicationEnvironmentRequest{
			EnvVars: vars,
			Config:  existing.ConfigJSON,
		}); err != nil {
			return nil, err
		}
	}
	return s.GetAll(ctx, appID)
}

// validateEnvVars rejects keys docker or the platform can't accept, so a bad
// key fails the save with a message instead of being silently dropped when the
// compose override is generated (see pipeline.buildOverrideYAML). Values are
// never validated — any string, including empty, is a legitimate value.
func validateEnvVars(vars map[string]string) error {
	for k := range vars {
		if strings.HasPrefix(k, domain.ReservedEnvVarPrefix) {
			return fmt.Errorf("env var %q uses the reserved %s prefix", k, domain.ReservedEnvVarPrefix)
		}
		if !domain.ValidEnvVarKey(k) {
			return fmt.Errorf("invalid env var name %q (expected letters, digits and underscore, not starting with a digit)", k)
		}
	}
	return nil
}
