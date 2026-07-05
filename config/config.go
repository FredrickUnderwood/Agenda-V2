package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig             `yaml:"server"`
	Database      DatabaseConfig           `yaml:"database"`
	Redis         RedisConfig              `yaml:"redis"`
	Git           GitConfig                `yaml:"git"`
	Deploy        DeployConfig             `yaml:"deploy"`
	Gateway       GatewayConfig            `yaml:"gateway"`
	Log           LogConfig                `yaml:"log"`
	Security      SecurityConfig           `yaml:"security"`
	Machines      map[string]MachineConfig `yaml:"machines"`
	WorkspaceRoot string                   `yaml:"workspace_root"`
}

// SecurityConfig holds bootstrap secrets that must exist before the DB is
// reachable, so they cannot themselves live in the Setting table.
type SecurityConfig struct {
	// MasterKey encrypts secret Settings at rest (AES-256-GCM, key = SHA-256 of
	// this string). Keep it out of source control. Rotating it makes existing
	// encrypted secrets unreadable. Empty disables encryption — secrets are then
	// stored in plaintext and a warning is logged.
	MasterKey string `yaml:"master_key"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// MachineConfig holds SSH connection info for a remote deploy target. An
// empty Host means "localhost" (no SSH needed).
type MachineConfig struct {
	MachineType   string `yaml:"machine_type"`
	Host          string `yaml:"host"`
	User          string `yaml:"user"`
	Port          int    `yaml:"port"`
	SSHKeyPath    string `yaml:"ssh_key_path"`
	Password      string `yaml:"password"`
	WorkspaceRoot string `yaml:"workspace_root"`
}

func (m *MachineConfig) IsLocal() bool {
	return m == nil || m.Host == ""
}

// GetMachine returns the MachineConfig for the given name. Returns nil when
// name is empty, "local", or not found — all meaning localhost.
func (c *Config) GetMachine(name string) *MachineConfig {
	if name == "" || name == "local" {
		return nil
	}
	m, ok := c.Machines[name]
	if !ok {
		return nil
	}
	return &m
}

type ServerConfig struct {
	Addr      string `yaml:"addr"`
	AuthToken string `yaml:"auth_token"`
	PprofAddr string `yaml:"pprof_addr"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type GitConfig struct {
	Tokens           map[string]string `yaml:"tokens"`
	GitBin           string            `yaml:"git_bin"`
	OperationTimeout duration          `yaml:"operation_timeout"`

	// TokenResolver, when set, is consulted before the static Tokens map so a
	// git token (e.g. a GitHub PAT) can be rotated at runtime from the Setting
	// table without restarting. Wired at startup by the server; not serialized.
	TokenResolver func(host string) string `yaml:"-"`
	// SecretValues, when set, returns every secret string that must be redacted
	// from git output and logs (a superset of Tokens). Not serialized.
	SecretValues func() []string `yaml:"-"`
}

type DeployConfig struct {
	MaxOutputBytes int      `yaml:"max_output_bytes"`
	DefaultTimeout duration `yaml:"default_timeout"`
}

type GatewayConfig struct {
	Enabled       bool     `yaml:"enabled"`
	BaseURL       string   `yaml:"base_url"`
	ServiceToken  string   `yaml:"service_token"`
	Timeout       duration `yaml:"timeout"`
	BackendScheme string   `yaml:"backend_scheme"`
	BackendHost   string   `yaml:"backend_host"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

// duration is a yaml-unmarshallable wrapper around time.Duration.
type duration struct{ time.Duration }

func (d *duration) UnmarshalYAML(value *yaml.Node) error {
	v, err := time.ParseDuration(value.Value)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d duration) MarshalYAML() (interface{}, error) {
	return d.String(), nil
}

func defaults() *Config {
	return &Config{
		Server:   ServerConfig{Addr: ":8080", PprofAddr: "127.0.0.1:6060"},
		Database: DatabaseConfig{},
		Git: GitConfig{
			Tokens:           map[string]string{},
			OperationTimeout: duration{60 * time.Second},
		},
		Machines: map[string]MachineConfig{},
		Deploy: DeployConfig{
			MaxOutputBytes: 65536,
			DefaultTimeout: duration{5 * time.Minute},
		},
		Gateway: GatewayConfig{
			Timeout:       duration{10 * time.Second},
			BackendScheme: "http",
			BackendHost:   "host.docker.internal",
		},
		Log:           LogConfig{Level: "info"},
		WorkspaceRoot: "~/.agenda-v2/workspaces",
	}
}

// Load reads the config file from the first available location:
// flagPath > $AGENDA_V2_CONFIG > ~/.config/agenda-v2/agenda-v2.yaml > /etc/agenda-v2/agenda-v2.yaml
func Load(flagPath string) (*Config, error) {
	cfg := defaults()

	path := resolveConfigPath(flagPath)
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read config " + path + ": " + err.Error())
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, errors.New("parse config " + path + ": " + err.Error())
	}
	return cfg, nil
}

func resolveConfigPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	if v := os.Getenv("AGENDA_V2_CONFIG"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".config/agenda-v2/agenda-v2.yaml"),
		"/etc/agenda-v2/agenda-v2.yaml",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
