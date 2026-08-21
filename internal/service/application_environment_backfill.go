package service

import (
	"context"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// legacyEnvConfigKey is the field inside Application.DeployConfig that used to
// hold the application-level env var baseline, before env vars became
// per-environment. It is still the lowest layer of pipeline.mergeEnv for
// installs that haven't been backfilled yet.
const legacyEnvConfigKey = "env"

// BackfillApplicationEnvVars moves each application's legacy application-level
// env var baseline (DockerDeployConfig.Env) into that application's prod
// ApplicationEnvironment row, then removes it from deploy_config — the UI no
// longer edits the baseline, so leaving values there would make them invisible
// yet still live at deploy time.
//
// Prod is the only destination by design: the baseline currently applies to
// every environment, and the operator's decision is that today's values are the
// prod values. An application that also has stage/test instances therefore
// LOSES those vars in stage/test at its next deploy, so each such case is
// logged at warn level with the app name and the affected environments.
//
// Idempotent: an application whose deploy_config no longer carries an env key
// is skipped, so this is safe to run on every startup. Where a prod row already
// has a key, the prod row wins — it is the higher-priority layer, so keeping it
// preserves the exact env the app deploys with today.
//
// deploy_config is rewritten from a generic map rather than from
// DockerDeployConfig so unknown/future fields survive the rewrite untouched.
func BackfillApplicationEnvVars(
	ctx context.Context,
	apps *repository.ApplicationRepository,
	envs *repository.ApplicationEnvironmentRepository,
	targets *repository.ApplicationTargetRepository,
) (int, error) {
	list, err := apps.List(ctx)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, app := range list {
		if app.DeployMethod != domain.DeployMethodDocker {
			continue
		}
		var cfg map[string]any
		if err := sonic.UnmarshalString(app.DeployConfig, &cfg); err != nil || cfg == nil {
			// A config we can't parse is a config we must not rewrite.
			if err != nil {
				logger.L().Warn("env var backfill: skipping application with unparseable deploy_config",
					zap.String("application", app.Name), zap.Error(err))
			}
			continue
		}
		baseline := stringMap(cfg[legacyEnvConfigKey])
		if len(baseline) == 0 {
			// Nothing to move; still drop a stray empty/`null` env key so the
			// config settles into its post-migration shape.
			if _, present := cfg[legacyEnvConfigKey]; present {
				if err := writeDeployConfigWithoutEnv(ctx, apps, app, cfg); err != nil {
					return migrated, err
				}
			}
			continue
		}

		row, err := envs.GetByApplicationEnv(ctx, app.ID, domain.EnvironmentProd)
		if err != nil {
			return migrated, err
		}
		prodVars, err := row.ParseEnvVars()
		if err != nil {
			return migrated, err
		}
		for k, v := range baseline {
			if _, ok := prodVars[k]; !ok {
				prodVars[k] = v
			}
		}
		envVarsJSON, err := sonicMarshal(prodVars)
		if err != nil {
			return migrated, err
		}
		next := &domain.ApplicationEnvironment{
			ApplicationID: app.ID,
			Env:           domain.EnvironmentProd,
			EnvVarsJSON:   envVarsJSON,
		}
		if row != nil {
			next.ConfigJSON = row.ConfigJSON
		}
		if err := envs.Upsert(ctx, next); err != nil {
			return migrated, err
		}
		if err := writeDeployConfigWithoutEnv(ctx, apps, app, cfg); err != nil {
			return migrated, err
		}
		migrated++

		if lost := nonProdEnvsOf(ctx, targets, app.ID); len(lost) > 0 {
			logger.L().Warn("env var backfill: application-level env vars moved to prod only; these environments will deploy without them",
				zap.String("application", app.Name),
				zap.Strings("environments", lost),
				zap.Int("var_count", len(baseline)))
		}
	}
	if migrated > 0 {
		logger.L().Info("env var backfill complete", zap.Int("applications_migrated", migrated))
	}
	return migrated, nil
}

func writeDeployConfigWithoutEnv(ctx context.Context, apps *repository.ApplicationRepository, app *domain.Application, cfg map[string]any) error {
	delete(cfg, legacyEnvConfigKey)
	encoded, err := sonicMarshal(cfg)
	if err != nil {
		return err
	}
	app.DeployConfig = encoded
	return apps.Update(ctx, app)
}

// nonProdEnvsOf returns the distinct non-prod environments an application has
// instances in. Best-effort: a lookup failure only costs the warning.
func nonProdEnvsOf(ctx context.Context, targets *repository.ApplicationTargetRepository, appID int64) []string {
	if targets == nil {
		return nil
	}
	list, err := targets.ListByApplication(ctx, appID)
	if err != nil {
		return nil
	}
	seen := map[domain.Environment]bool{}
	out := make([]string, 0, 2)
	for _, t := range list {
		if t.Env == domain.EnvironmentProd || seen[t.Env] {
			continue
		}
		seen[t.Env] = true
		out = append(out, string(t.Env))
	}
	return out
}

// stringMap coerces a decoded JSON object into map[string]string, skipping
// entries whose value isn't a string (deploy_config is user-supplied JSON).
func stringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		s, ok := val.(string)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out
}
