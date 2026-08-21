package domain

type Environment string

const (
	EnvironmentProd  Environment = "prod"
	EnvironmentStage Environment = "stage"
	EnvironmentTest  Environment = "test"
)

func (e Environment) Valid() bool {
	switch e {
	case EnvironmentProd, EnvironmentStage, EnvironmentTest:
		return true
	default:
		return false
	}
}

func DefaultEnvironment(e Environment) Environment {
	if e == "" {
		return EnvironmentProd
	}
	return e
}

// AllEnvironments is the fixed environment set, in display order. The env-vars
// matrix API returns one column per entry, so callers can rely on every
// environment being present in a response even when it has no vars configured.
func AllEnvironments() []Environment {
	return []Environment{EnvironmentProd, EnvironmentStage, EnvironmentTest}
}
