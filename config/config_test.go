package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeployConfigDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		timeout    time.Duration
		maxBytes   int
	}{
		{"defaults", "{}\n", 20 * time.Minute, 4096},
		{"explicit settings", "deploy:\n  default_timeout: 40m\n  max_output_bytes: 1024\n", 40 * time.Minute, 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Deploy.DefaultTimeout.Duration != tc.timeout || cfg.Deploy.MaxOutputBytes != tc.maxBytes {
				t.Fatalf("deploy config = %+v", cfg.Deploy)
			}
		})
	}
}
