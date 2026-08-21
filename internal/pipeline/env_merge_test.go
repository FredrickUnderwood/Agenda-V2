package pipeline

import (
	"context"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

// fakeEnvConfig serves one env var map per environment, the way
// ApplicationEnvironmentService does off the application_environment table.
type fakeEnvConfig map[domain.Environment]map[string]string

func (f fakeEnvConfig) GetEnvVars(_ context.Context, _ int64, env domain.Environment) (map[string]string, error) {
	if vars, ok := f[env]; ok {
		return vars, nil
	}
	return map[string]string{}, nil
}

// TestMergeEnvLayering pins the layer precedence every deploy depends on:
// application baseline < environment < instance. The environment layer is what
// makes one application deploy different values to prod and stage, so a
// regression here silently ships the wrong config to a running service.
func TestMergeEnvLayering(t *testing.T) {
	envConfig := fakeEnvConfig{
		domain.EnvironmentProd:  {"DB_DSN": "prod-dsn", "LOG_LEVEL": "warn", "EMPTY": ""},
		domain.EnvironmentStage: {"DB_DSN": "stage-dsn"},
	}
	b := NewBuilder(nil, nil, nil, envConfig)
	dockerCfg := &domain.DockerDeployConfig{Env: map[string]string{
		"DB_DSN":        "baseline-dsn",
		"LOG_LEVEL":     "info",
		"EMPTY":         "baseline",
		"ONLY_BASELINE": "1",
	}}

	cases := []struct {
		name     string
		env      domain.Environment
		override string
		want     map[string]string
	}{
		{
			name: "env layer overrides the baseline",
			env:  domain.EnvironmentProd,
			want: map[string]string{
				"DB_DSN": "prod-dsn", "LOG_LEVEL": "warn",
				// An env-level empty value wins over the baseline: blank means
				// "empty string here", not "inherit".
				"EMPTY": "", "ONLY_BASELINE": "1",
			},
		},
		{
			name: "a different env gets its own values",
			env:  domain.EnvironmentStage,
			want: map[string]string{
				"DB_DSN": "stage-dsn", "LOG_LEVEL": "info",
				"EMPTY": "baseline", "ONLY_BASELINE": "1",
			},
		},
		{
			name:     "instance override wins over the env layer",
			env:      domain.EnvironmentProd,
			override: `{"DB_DSN":"instance-dsn"}`,
			want: map[string]string{
				"DB_DSN": "instance-dsn", "LOG_LEVEL": "warn",
				"EMPTY": "", "ONLY_BASELINE": "1",
			},
		},
		{
			name: "env with nothing configured falls back to the baseline",
			env:  domain.EnvironmentTest,
			want: map[string]string{
				"DB_DSN": "baseline-dsn", "LOG_LEVEL": "info",
				"EMPTY": "baseline", "ONLY_BASELINE": "1",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := &domain.DeployTarget{
				App:       &domain.Application{ID: 1, Name: "svc"},
				EnvTarget: &domain.ApplicationEnvTarget{Env: tc.env, EnvOverrideJSON: tc.override},
			}
			got, err := b.mergeEnv(context.Background(), target, dockerCfg)
			if err != nil {
				t.Fatalf("mergeEnv: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("merged = %v, want %v", got, tc.want)
			}
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("%s = %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

// TestMergeEnvDoesNotMutateBaseline: the baseline map comes from the parsed
// application config, and mergeEnv is called once per instance in a batch
// deploy — writing through to it would leak one instance's override into the
// next.
func TestMergeEnvDoesNotMutateBaseline(t *testing.T) {
	b := NewBuilder(nil, nil, nil, fakeEnvConfig{
		domain.EnvironmentProd: {"LOG_LEVEL": "warn"},
	})
	dockerCfg := &domain.DockerDeployConfig{Env: map[string]string{"LOG_LEVEL": "info"}}
	target := &domain.DeployTarget{
		App:       &domain.Application{ID: 1, Name: "svc"},
		EnvTarget: &domain.ApplicationEnvTarget{Env: domain.EnvironmentProd},
	}

	if _, err := b.mergeEnv(context.Background(), target, dockerCfg); err != nil {
		t.Fatalf("mergeEnv: %v", err)
	}
	if dockerCfg.Env["LOG_LEVEL"] != "info" {
		t.Fatalf("baseline mutated: %v", dockerCfg.Env)
	}
}
