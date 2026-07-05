// Package node implements agenda-node: the resident per-machine agent that
// executes deploy commands on behalf of the control plane (replacing SSH) and
// acts as a local reverse proxy for gateway traffic. It is a separate binary
// (cmd/agenda-node) that shares internal/runner with the control plane.
package node

import (
	"errors"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is agenda-node's own configuration, loaded from agenda-node.yaml
// (independent of the control plane's agenda-v2.yaml).
type Config struct {
	// ListenAddr is the management API (/v1/jobs, /v1/proxy, /v1/health). Bind
	// it to a private interface — it is equivalent to remote code execution.
	ListenAddr string `yaml:"listen_addr"`
	// ProxyListenAddr is the reverse-proxy port gateway backends point at.
	ProxyListenAddr string `yaml:"proxy_listen_addr"`
	// MachineID matches the control plane's machine.id for heartbeats.
	MachineID int64 `yaml:"machine_id"`
	// Token is the shared per-machine secret; must equal machine.agent_token.
	Token string `yaml:"token"`
	// CentralBaseURL is the control plane's base URL for heartbeats.
	CentralBaseURL string `yaml:"central_base_url"`

	HeartbeatInterval Duration `yaml:"heartbeat_interval"`
	MaxOutputBytes    int      `yaml:"max_output_bytes"`
	JobRetention      Duration `yaml:"job_retention"`
}

// Duration is a yaml-unmarshalable time.Duration ("15s", "1h").
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	v, err := time.ParseDuration(value.Value)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func defaults() *Config {
	return &Config{
		ListenAddr:        "127.0.0.1:7100",
		ProxyListenAddr:   "0.0.0.0:7200",
		HeartbeatInterval: Duration{15 * time.Second},
		MaxOutputBytes:    65536,
		JobRetention:      Duration{time.Hour},
	}
}

// Load reads the node config from path (or $AGENDA_NODE_CONFIG when path is "").
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("AGENDA_NODE_CONFIG")
	}
	if path == "" {
		return nil, errors.New("no config path given (use --config or $AGENDA_NODE_CONFIG)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := defaults()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	if cfg.Token == "" {
		return nil, errors.New("token is required in agenda-node.yaml")
	}
	if cfg.MachineID <= 0 {
		return nil, errors.New("machine_id is required in agenda-node.yaml")
	}
	return cfg, nil
}
