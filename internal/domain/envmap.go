package domain

import (
	"regexp"
	"strings"

	"github.com/bytedance/sonic"
)

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

// ReservedEnvVarPrefix is the namespace the platform injects into every
// container (AGENDA_APP_NAME, AGENDA_LOG_DIR, ...). User-defined vars in this
// namespace are dropped at deploy time by pipeline.buildOverrideYAML so a
// misconfigured app can't break the SDK contract; validation rejects them up
// front so the operator sees why instead of losing them silently.
const ReservedEnvVarPrefix = "AGENDA_"

var envVarKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvVarKey reports whether k is a usable container env var name. Empty
// keys and the reserved AGENDA_ namespace are rejected.
func ValidEnvVarKey(k string) bool {
	if strings.HasPrefix(k, ReservedEnvVarPrefix) {
		return false
	}
	return envVarKeyPattern.MatchString(k)
}
