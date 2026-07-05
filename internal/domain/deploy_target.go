package domain

// DeployTarget bundles everything one pipeline run needs. In-memory only,
// never persisted — Branch/CommitSHA come from the triggering
// ApplicationRelease at Deploy() time.
type DeployTarget struct {
	App       *Application
	EnvTarget *ApplicationEnvTarget
	Branch    string
	// CommitSHA pins git_pull to a specific commit reachable from Branch's
	// history. Empty means "latest Branch HEAD" (used e.g. for a first-ever
	// release created without a caller-supplied commit).
	CommitSHA string
}

func (t *DeployTarget) Env() Environment {
	if t == nil {
		return EnvironmentProd
	}
	if t.EnvTarget != nil && t.EnvTarget.Env != "" {
		return t.EnvTarget.Env
	}
	return EnvironmentProd
}
