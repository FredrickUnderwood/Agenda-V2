package domain

import "github.com/bytedance/sonic"

// parseEnvMap nil-safe-decodes a JSON-encoded map[string]string column.
// Shared by ApplicationEnvironment.ParseEnvVars and
// ApplicationEnvTarget.ParseEnvOverride — both env-var override layers store
// the same shape and must parse it identically. Never returns a nil map, so
// callers can merge unconditionally.
func parseEnvMap(raw string) (map[string]string, error) {
	vars := map[string]string{}
	if raw == "" {
		return vars, nil
	}
	if err := sonic.UnmarshalString(raw, &vars); err != nil {
		return nil, err
	}
	return vars, nil
}
