package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

type fakeMachineRepo struct {
	m         *domain.Machine
	hbVersion string
	hbAt      time.Time
	hbCalls   int
	updateErr error
}

func (f *fakeMachineRepo) Create(context.Context, *domain.Machine) error { return nil }
func (f *fakeMachineRepo) GetByName(context.Context, string) (*domain.Machine, error) {
	return nil, nil
}
func (f *fakeMachineRepo) List(context.Context) ([]*domain.Machine, error) { return nil, nil }
func (f *fakeMachineRepo) Update(context.Context, *domain.Machine) error   { return nil }
func (f *fakeMachineRepo) Delete(context.Context, int64) error             { return nil }

func (f *fakeMachineRepo) GetByID(_ context.Context, id int64) (*domain.Machine, error) {
	if f.m == nil || f.m.ID != id {
		return nil, errors.New("not found")
	}
	return f.m, nil
}

func (f *fakeMachineRepo) UpdateHeartbeat(_ context.Context, _ int64, version string, at time.Time) error {
	f.hbCalls++
	f.hbVersion, f.hbAt = version, at
	return f.updateErr
}

func TestHeartbeatRejectsBadToken(t *testing.T) {
	repo := &fakeMachineRepo{m: &domain.Machine{ID: 3, Mode: domain.MachineModeAgent, AgentToken: "correct"}}
	svc := NewMachineService(repo)

	if err := svc.Heartbeat(context.Background(), 3, "wrong", "0.1.0"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad token err = %v, want ErrInvalidCredentials", err)
	}
	if repo.hbCalls != 0 {
		t.Fatalf("heartbeat persisted despite bad token (%d calls)", repo.hbCalls)
	}
}

func TestHeartbeatRejectsEmptyStoredToken(t *testing.T) {
	// A machine with no agent_token must not be heartbeatable by an empty token.
	repo := &fakeMachineRepo{m: &domain.Machine{ID: 3, Mode: domain.MachineModeAgent, AgentToken: ""}}
	svc := NewMachineService(repo)
	if err := svc.Heartbeat(context.Background(), 3, "", "0.1.0"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("empty stored token err = %v, want ErrInvalidCredentials", err)
	}
}

func TestHeartbeatAcceptsGoodToken(t *testing.T) {
	repo := &fakeMachineRepo{m: &domain.Machine{ID: 3, Mode: domain.MachineModeAgent, AgentToken: "correct"}}
	svc := NewMachineService(repo)
	if err := svc.Heartbeat(context.Background(), 3, "correct", "0.2.0"); err != nil {
		t.Fatalf("good token heartbeat: %v", err)
	}
	if repo.hbCalls != 1 || repo.hbVersion != "0.2.0" {
		t.Fatalf("heartbeat not persisted correctly: calls=%d version=%q", repo.hbCalls, repo.hbVersion)
	}
}

func TestMachineOnlineDerivation(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-5 * time.Second)
	stale := now.Add(-time.Hour)

	agentFresh := &domain.Machine{Mode: domain.MachineModeAgent, AgentLastHeartbeatAt: &fresh}
	agentStale := &domain.Machine{Mode: domain.MachineModeAgent, AgentLastHeartbeatAt: &stale}
	agentNever := &domain.Machine{Mode: domain.MachineModeAgent}
	sshMachine := &domain.Machine{Mode: domain.MachineModeSSH, AgentLastHeartbeatAt: &fresh}

	if !agentFresh.Online(heartbeatInterval) {
		t.Error("fresh agent should be online")
	}
	if agentStale.Online(heartbeatInterval) {
		t.Error("stale agent should be offline")
	}
	if agentNever.Online(heartbeatInterval) {
		t.Error("never-heartbeated agent should be offline")
	}
	if sshMachine.Online(heartbeatInterval) {
		t.Error("ssh machine should always report offline (no heartbeat concept)")
	}
}

func TestValidateMachineMode(t *testing.T) {
	if err := validateMachineMode(domain.MachineModeAgent, "", ""); err == nil {
		t.Error("agent without agent_base_url should fail")
	}
	if err := validateMachineMode(domain.MachineModeAgent, "", "http://n:7100"); err != nil {
		t.Errorf("agent with base url should pass: %v", err)
	}
	if err := validateMachineMode(domain.MachineModeSSH, "", ""); err == nil {
		t.Error("ssh without host should fail")
	}
	if err := validateMachineMode(domain.MachineModeSSH, "10.0.0.1", ""); err != nil {
		t.Errorf("ssh with host should pass: %v", err)
	}
	if err := validateMachineMode("bogus", "h", "u"); err == nil {
		t.Error("bogus mode should fail")
	}
}
