package application

import (
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

func target(name string, enabled bool, state domain.RuntimeState) *domain.ApplicationEnvTarget {
	return &domain.ApplicationEnvTarget{InstanceName: name, Enabled: enabled, DesiredState: state}
}

func TestEligibleForEnvDeploy(t *testing.T) {
	cases := []struct {
		name         string
		target       *domain.ApplicationEnvTarget
		wantInstance string
		want         bool
	}{
		{"whole-env includes running enabled", target("blue", true, domain.RuntimeStateRunning), "", true},
		{"whole-env skips stopped (decommission is sticky)", target("blue", true, domain.RuntimeStateStopped), "", false},
		{"whole-env skips disabled", target("blue", false, domain.RuntimeStateRunning), "", false},
		{"named instance restarts a stopped one", target("blue", true, domain.RuntimeStateStopped), "blue", true},
		{"named instance only matches its name", target("green", true, domain.RuntimeStateRunning), "blue", false},
		{"named instance still requires enabled", target("blue", false, domain.RuntimeStateStopped), "blue", false},
		{"empty desired_state reads as running", target("blue", true, ""), "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eligibleForEnvDeploy(c.target, c.wantInstance); got != c.want {
				t.Errorf("eligibleForEnvDeploy(%s, %q) = %v, want %v", c.target.InstanceName, c.wantInstance, got, c.want)
			}
		})
	}
}
