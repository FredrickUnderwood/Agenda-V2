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
