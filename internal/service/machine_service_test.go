package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
)

// testBox is a disabled Box (no master key): Encrypt/Decrypt pass plaintext
// through unchanged, matching how these tests store AgentToken directly.
func testBox() *secret.Box { return secret.NewBox("") }

type fakeMachineRepo struct {
	m         *domain.Machine
	hbVersion string
	hbAt      time.Time
	hbCalls   int
	updateErr error
}

func (f *fakeMachineRepo) Create(_ context.Context, m *domain.Machine) error {
	if m.ID == 0 {
		m.ID = 1
	}
	f.m = m
	return nil
}
func (f *fakeMachineRepo) GetByName(context.Context, string) (*domain.Machine, error) {
	return nil, nil
}
func (f *fakeMachineRepo) List(context.Context) ([]*domain.Machine, error) { return nil, nil }
func (f *fakeMachineRepo) Update(_ context.Context, m *domain.Machine) error {
	f.m = m
	return nil
}
func (f *fakeMachineRepo) Delete(context.Context, int64) error { return nil }

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
	svc := NewMachineService(repo, testBox())

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
	svc := NewMachineService(repo, testBox())
	if err := svc.Heartbeat(context.Background(), 3, "", "0.1.0"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("empty stored token err = %v, want ErrInvalidCredentials", err)
	}
}

func TestHeartbeatAcceptsGoodToken(t *testing.T) {
	repo := &fakeMachineRepo{m: &domain.Machine{ID: 3, Mode: domain.MachineModeAgent, AgentToken: "correct"}}
	svc := NewMachineService(repo, testBox())
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

func TestCreateAgentMachineGeneratesAndEncryptsToken(t *testing.T) {
	repo := &fakeMachineRepo{}
	svc := NewMachineService(repo, secret.NewBox("test-master-key"))

	_, plaintext, err := svc.Create(context.Background(), CreateMachineRequest{
		Name: "m1", Mode: domain.MachineModeAgent, AgentBaseURL: "http://n:7100",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(plaintext) != 64 {
		t.Fatalf("want a 64-char hex token, got %q (len %d)", plaintext, len(plaintext))
	}
	if repo.m.AgentToken == plaintext {
		t.Fatal("agent_token was persisted in plaintext, want it encrypted at rest")
	}
	if !secret.IsEncrypted(repo.m.AgentToken) {
		t.Fatalf("persisted agent_token %q is not enc:v1:-prefixed", repo.m.AgentToken)
	}
}

func TestGetDecryptsAgentToken(t *testing.T) {
	repo := &fakeMachineRepo{}
	svc := NewMachineService(repo, secret.NewBox("test-master-key"))

	_, plaintext, err := svc.Create(context.Background(), CreateMachineRequest{
		Name: "m1", Mode: domain.MachineModeAgent, AgentBaseURL: "http://n:7100",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.Get(context.Background(), repo.m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AgentToken != plaintext {
		t.Fatalf("Get returned %q, want decrypted plaintext %q", got.AgentToken, plaintext)
	}
}

func TestHeartbeatAcceptsGeneratedTokenAfterEncryption(t *testing.T) {
	repo := &fakeMachineRepo{}
	svc := NewMachineService(repo, secret.NewBox("test-master-key"))

	_, plaintext, err := svc.Create(context.Background(), CreateMachineRequest{
		Name: "m1", Mode: domain.MachineModeAgent, AgentBaseURL: "http://n:7100",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Heartbeat(context.Background(), repo.m.ID, plaintext, "0.1.0"); err != nil {
		t.Fatalf("heartbeat with the generated token: %v", err)
	}
}

func TestRotateAgentTokenInvalidatesOldToken(t *testing.T) {
	repo := &fakeMachineRepo{}
	svc := NewMachineService(repo, secret.NewBox("test-master-key"))

	_, oldToken, err := svc.Create(context.Background(), CreateMachineRequest{
		Name: "m1", Mode: domain.MachineModeAgent, AgentBaseURL: "http://n:7100",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newToken, err := svc.RotateAgentToken(context.Background(), repo.m.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newToken == oldToken {
		t.Fatal("rotated token equals the old one")
	}
	if err := svc.Heartbeat(context.Background(), repo.m.ID, oldToken, "0.1.0"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("heartbeat with old token err = %v, want ErrInvalidCredentials", err)
	}
	if err := svc.Heartbeat(context.Background(), repo.m.ID, newToken, "0.1.0"); err != nil {
		t.Fatalf("heartbeat with rotated token: %v", err)
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
