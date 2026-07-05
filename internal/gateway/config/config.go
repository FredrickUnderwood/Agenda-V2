package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppName       string
	LogLevel      string
	Server        ServerConfig
	MySQL         MySQLConfig
	Auth          AuthConfig
	ServiceTokens []ServiceTokenConfig
	Gateway       GatewayConfig
}

type ServerConfig struct {
	Addr string
}

type MySQLConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// AuthConfig holds the shared JWT secret used to verify user tokens minted by
// the control plane's built-in auth (internal/auth). Empty means the admin API
// accepts only service tokens (no user-JWT path). This replaces the former
// external user-core dependency.
type AuthConfig struct {
	JWTSecret string
}

type ServiceTokenConfig struct {
	Name  string
	Token string
	Perms []string
}

type GatewayConfig struct {
	RefreshInterval       time.Duration
	DefaultBackendTimeout time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		AppName:  env("GATEWAY_APP_NAME", "agenda-gateway"),
		LogLevel: env("GATEWAY_LOG_LEVEL", "info"),
		Server: ServerConfig{
			Addr: env("GATEWAY_ADDR", ":8080"),
		},
		MySQL: MySQLConfig{
			DSN:             os.Getenv("GATEWAY_DATABASE_DSN"),
			MaxOpenConns:    intEnv("GATEWAY_MYSQL_MAX_OPEN_CONNS", 20),
			MaxIdleConns:    intEnv("GATEWAY_MYSQL_MAX_IDLE_CONNS", 10),
			ConnMaxLifetime: durationEnv("GATEWAY_MYSQL_CONN_MAX_LIFETIME", time.Hour),
		},
		Auth: AuthConfig{
			JWTSecret: os.Getenv("GATEWAY_JWT_SECRET"),
		},
		Gateway: GatewayConfig{
			RefreshInterval:       durationEnv("GATEWAY_REFRESH_INTERVAL", 2*time.Second),
			DefaultBackendTimeout: durationEnv("GATEWAY_BACKEND_TIMEOUT", 30*time.Second),
		},
	}
	cfg.ServiceTokens = serviceTokensFromEnv()
	if cfg.MySQL.DSN == "" {
		return nil, errors.New("GATEWAY_DATABASE_DSN is required")
	}
	if len(cfg.ServiceTokens) == 0 {
		return nil, errors.New("GATEWAY_SERVICE_TOKEN is required")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err == nil {
		return d
	}
	if seconds, err := strconv.Atoi(v); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func serviceTokensFromEnv() []ServiceTokenConfig {
	token := os.Getenv("GATEWAY_SERVICE_TOKEN")
	if token == "" {
		return nil
	}
	name := env("GATEWAY_SERVICE_TOKEN_NAME", "agenda")
	perms := splitCSV(env("GATEWAY_SERVICE_TOKEN_PERMS", "route.read,route.update,route.rollback"))
	return []ServiceTokenConfig{{Name: name, Token: token, Perms: perms}}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
