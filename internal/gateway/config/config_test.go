package config

import (
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GATEWAY_DATABASE_DSN", "user:pass@tcp(localhost:3306)/agenda?parseTime=true")
}

func TestLoadRequiresServiceToken(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GATEWAY_SERVICE_TOKEN", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "GATEWAY_SERVICE_TOKEN is required") {
		t.Fatalf("Load() error = %v, want GATEWAY_SERVICE_TOKEN is required", err)
	}
}

func TestLoadServiceTokenFromEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GATEWAY_SERVICE_TOKEN", "service-token")
	t.Setenv("GATEWAY_SERVICE_TOKEN_NAME", "agenda-release")
	t.Setenv("GATEWAY_SERVICE_TOKEN_PERMS", "route.read, route.update")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.ServiceTokens) != 1 {
		t.Fatalf("ServiceTokens len = %d, want 1", len(cfg.ServiceTokens))
	}
	got := cfg.ServiceTokens[0]
	if got.Name != "agenda-release" || got.Token != "service-token" {
		t.Fatalf("ServiceToken = %+v, want configured name/token", got)
	}
	if len(got.Perms) != 2 || got.Perms[0] != "route.read" || got.Perms[1] != "route.update" {
		t.Fatalf("ServiceToken perms = %#v, want route.read/route.update", got.Perms)
	}
}
