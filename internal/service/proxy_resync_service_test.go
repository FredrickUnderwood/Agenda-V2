package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

type fakeMachineGetterPR struct {
	machines map[int64]*domain.Machine
}

func (f fakeMachineGetterPR) Get(_ context.Context, id int64) (*domain.Machine, error) {
	if m, ok := f.machines[id]; ok {
		return m, nil
	}
	return nil, errors.New("machine not found")
}

type fakeTargetLister struct {
	byMachine map[int64][]*domain.ApplicationEnvTarget
}

func (f fakeTargetLister) ListEnabledByMachine(_ context.Context, machineID int64) ([]*domain.ApplicationEnvTarget, error) {
	return f.byMachine[machineID], nil
}

type fakeAppGetter struct {
	names map[int64]string
}

func (f fakeAppGetter) GetByID(_ context.Context, id int64) (*domain.Application, error) {
	if name, ok := f.names[id]; ok {
		return &domain.Application{ID: id, Name: name}, nil
	}
	return nil, errors.New("app not found")
}

type fakeVerifiedGetter struct {
	verified map[string]bool // key: "<app>/<env>/<instance>"
}

func (f fakeVerifiedGetter) GetLatestVerified(_ context.Context, appID int64, env domain.Environment, instance string) (*domain.ApplicationRelease, error) {
	key := fmt.Sprintf("%d/%s/%s", appID, env, domain.NormalizeInstanceName(instance))
	if f.verified[key] {
		return &domain.ApplicationRelease{}, nil
	}
	return nil, nil
}

type regCall struct {
	base, token, instance string
	port                  int
}

func newResyncSvc(machine *domain.Machine, targets []*domain.ApplicationEnvTarget, verified map[string]bool, calls *[]regCall) *ProxyResyncService {
	return &ProxyResyncService{
		targets:  fakeTargetLister{byMachine: map[int64][]*domain.ApplicationEnvTarget{machine.ID: targets}},
		releases: fakeVerifiedGetter{verified: verified},
		machines: fakeMachineGetterPR{machines: map[int64]*domain.Machine{machine.ID: machine}},
		apps:     fakeAppGetter{names: map[int64]string{1: "agenda-example"}},
		register: func(_ context.Context, base, token, instance string, port int) error {
			*calls = append(*calls, regCall{base, token, instance, port})
			return nil
		},
	}
}

func agentMachinePR(id int64) *domain.Machine {
	return &domain.Machine{ID: id, Mode: domain.MachineModeAgent, AgentBaseURL: "http://node:7100", AgentToken: "tok"}
}

func TestResyncMachine_RegistersEnabledVerifiedInstances(t *testing.T) {
	m := agentMachinePR(2)
	targets := []*domain.ApplicationEnvTarget{
		{ApplicationID: 1, Env: "prod", InstanceName: "default", Port: 18081, Enabled: true, MachineID: 2},
		{ApplicationID: 1, Env: "prod", InstanceName: "green", Port: 18082, Enabled: true, MachineID: 2},
		{ApplicationID: 1, Env: "prod", InstanceName: "nodeploy", Port: 0, Enabled: true, MachineID: 2},      // no port → skip
		{ApplicationID: 1, Env: "prod", InstanceName: "unverified", Port: 9999, Enabled: true, MachineID: 2}, // not verified → skip
	}
	verified := map[string]bool{
		"1/prod/default": true,
		"1/prod/green":   true,
		// nodeploy/unverified deliberately absent
	}
	var calls []regCall
	svc := newResyncSvc(m, targets, verified, &calls)

	n, err := svc.ResyncMachine(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 registrations, got %d (calls=%v)", n, calls)
	}
	got := map[string]int{}
	for _, c := range calls {
		got[c.instance] = c.port
		if c.base != "http://node:7100" || c.token != "tok" {
			t.Fatalf("wrong agent target: %+v", c)
		}
	}
	// Registered under app-scoped proxy keys (nodeproxy.ProxyKey), never bare
	// instance names — bare names collide across apps sharing a machine.
	if got["agenda-example-prod-default"] != 18081 || got["agenda-example-prod-green"] != 18082 {
		t.Fatalf("expected agenda-example-prod-default:18081 agenda-example-prod-green:18082, got %v", got)
	}
}

func TestResyncMachine_NonAgentIsNoop(t *testing.T) {
	m := &domain.Machine{ID: 5, Mode: domain.MachineModeSSH}
	var calls []regCall
	svc := newResyncSvc(m, []*domain.ApplicationEnvTarget{
		{ApplicationID: 1, Env: "prod", InstanceName: "x", Port: 1, Enabled: true, MachineID: 5},
	}, map[string]bool{"1/prod/x": true}, &calls)

	n, err := svc.ResyncMachine(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 || len(calls) != 0 {
		t.Fatalf("expected no registrations for ssh machine, got n=%d calls=%v", n, calls)
	}
}

func TestResyncMachine_PerInstanceFailureDoesNotAbort(t *testing.T) {
	m := agentMachinePR(3)
	targets := []*domain.ApplicationEnvTarget{
		{ApplicationID: 1, Env: "prod", InstanceName: "a", Port: 100, Enabled: true, MachineID: 3},
		{ApplicationID: 1, Env: "prod", InstanceName: "b", Port: 200, Enabled: true, MachineID: 3},
	}
	verified := map[string]bool{"1/prod/a": true, "1/prod/b": true}
	svc := &ProxyResyncService{
		targets:  fakeTargetLister{byMachine: map[int64][]*domain.ApplicationEnvTarget{3: targets}},
		releases: fakeVerifiedGetter{verified: verified},
		machines: fakeMachineGetterPR{machines: map[int64]*domain.Machine{3: m}},
		apps:     fakeAppGetter{names: map[int64]string{1: "agenda-example"}},
		register: func(_ context.Context, _, _, key string, _ int) error {
			if key == "agenda-example-prod-a" {
				return errors.New("boom")
			}
			return nil
		},
	}

	n, err := svc.ResyncMachine(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 successful registration (b), got %d", n)
	}
}
