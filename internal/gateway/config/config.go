package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/edgetls"
)

type Config struct {
	AppName       string
	LogLevel      string
	Server        ServerConfig
	MySQL         MySQLConfig
	Auth          AuthConfig
	ServiceTokens []ServiceTokenConfig
	Gateway       GatewayConfig
	TLS           edgetls.Options
}

type ServerConfig struct {
	Addr string
	// MaxHeaderBytes bounds request headers on the data plane. Explicit rather
	// than left at net/http's 1MB default because a WebSocket handshake is
	// header-only and cheap to send: a caller can otherwise make the gateway
	// buffer a megabyte per in-flight handshake for free.
	MaxHeaderBytes int
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
	WebSocket             WebSocketConfig
}

// WebSocketConfig holds the gateway-wide WebSocket settings. Per-route settings
// (whether upgrades are allowed at all, the route's own idle timeout, its
// connection cap, its Origin allowlist) live on the route in the database —
// these are the ones that describe this gateway process's capacity, which no
// individual route's author is in a position to know.
type WebSocketConfig struct {
	// DefaultIdleTimeout applies to routes that don't set their own.
	DefaultIdleTimeout time.Duration
	// MaxConnections / MaxConnectionsPerIP: 0 disables that cap.
	MaxConnections      int
	MaxConnectionsPerIP int
	// HandshakeRate is handshakes/second across the gateway; 0 disables the
	// throttle. HandshakeBurst defaults to one second's worth.
	HandshakeRate  float64
	HandshakeBurst int
	// DialTimeout / ResponseHeaderTimeout bound the upstream handshake, which
	// runs without the route's total request timeout.
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	// DrainTimeout is how long shutdown waits for live tunnels to end on their
	// own before force-closing them.
	DrainTimeout time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		AppName:  env("GATEWAY_APP_NAME", "agenda-gateway"),
		LogLevel: env("GATEWAY_LOG_LEVEL", "info"),
		Server: ServerConfig{
			Addr:           env("GATEWAY_ADDR", ":8080"),
			MaxHeaderBytes: intEnv("GATEWAY_MAX_HEADER_BYTES", 64*1024),
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
			WebSocket: WebSocketConfig{
				DefaultIdleTimeout:    durationEnv("GATEWAY_WS_IDLE_TIMEOUT", 5*time.Minute),
				MaxConnections:        intEnv("GATEWAY_WS_MAX_CONNECTIONS", 0),
				MaxConnectionsPerIP:   intEnv("GATEWAY_WS_MAX_CONNECTIONS_PER_IP", 0),
				HandshakeRate:         floatEnv("GATEWAY_WS_HANDSHAKE_RATE", 0),
				HandshakeBurst:        intEnv("GATEWAY_WS_HANDSHAKE_BURST", 0),
				DialTimeout:           durationEnv("GATEWAY_WS_DIAL_TIMEOUT", 5*time.Second),
				ResponseHeaderTimeout: durationEnv("GATEWAY_WS_HANDSHAKE_TIMEOUT", 10*time.Second),
				DrainTimeout:          durationEnv("GATEWAY_WS_DRAIN_TIMEOUT", 30*time.Second),
			},
		},
	}
	cfg.ServiceTokens = serviceTokensFromEnv()
	cfg.TLS = tlsOptionsFromEnv()
	if cfg.MySQL.DSN == "" {
		return nil, errors.New("GATEWAY_DATABASE_DSN is required")
	}
	if len(cfg.ServiceTokens) == 0 {
		return nil, errors.New("GATEWAY_SERVICE_TOKEN is required")
	}
	return cfg, nil
}

// tlsOptionsFromEnv parses the edge-TLS bootstrap configuration: whether this
// gateway is the TLS edge, its :443 listen addr, cert storage, and the
// DNS-01 propagation-check knobs. The ACME account and Aliyun DNS credentials
// are NOT read here — they are operator secrets managed in the control plane's
// Settings UI and pushed in at runtime via the /-/tls admin endpoint (so they
// can be rotated without a gateway restart). Disabled by default.
func tlsOptionsFromEnv() edgetls.Options {
	return edgetls.Options{
		Enabled:            boolEnv("GATEWAY_TLS_ENABLED", false),
		HTTPSAddr:          env("GATEWAY_TLS_ADDR", ":443"),
		Resolvers:          splitFields(env("GATEWAY_TLS_RESOLVERS", "223.5.5.5 223.6.6.6")),
		PropagationTimeout: durationEnv("GATEWAY_TLS_PROPAGATION_TIMEOUT", 2*time.Minute),
		StoragePath:        env("GATEWAY_TLS_STORAGE_PATH", "/data"),
		ReconcileInterval:  durationEnv("GATEWAY_TLS_RECONCILE_INTERVAL", 30*time.Second),
	}
}

func boolEnv(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// splitFields splits on any whitespace and/or commas, so both the Caddyfile's
// space-separated multi-domain style and CSV work.
func splitFields(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ','
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
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

func floatEnv(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
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
