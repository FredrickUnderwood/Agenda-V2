package application

import (
	"context"
	"errors"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/pipeline"
)

type fakeStoppedLister struct {
	byMachine map[int64][]*domain.ApplicationEnvTarget
}

func (f *fakeStoppedLister) ListStoppedByMachine(_ context.Context, id int64) ([]*domain.ApplicationEnvTarget, error) {
	return f.byMachine[id], nil
}

type fakeAppGetter struct{ apps map[int64]*domain.Application }

func (f *fakeAppGetter) GetByID(_ context.Context, id int64) (*domain.Application, error) {
	a, ok := f.apps[id]
	if !ok {
		return nil, errors.New("no app")
	}
	return a, nil
}

type fakeReleaseGetter struct {
	branch map[int64]string // by appID
}

func (f *fakeReleaseGetter) GetLatestVerified(_ context.Context, appID int64, _ domain.Environment, _ string) (*domain.ApplicationRelease, error) {
	if br, ok := f.branch[appID]; ok {
		return &domain.ApplicationRelease{Branch: br}, nil
	}
	return nil, nil
}

// stubStep records that it ran and optionally fails.
type stubStep struct {
	ran     *int
	failErr error
}

func (s *stubStep) Execute(_ context.Context, _ *pipeline.RunContext) error {
	*s.ran++
	return s.failErr
}

// fakeTeardownBuilder hands back a stubStep per target, capturing the branch it
// was asked to build with so the test can assert branch resolution.
type fakeTeardownBuilder struct {
	ran        *int
	failFor    map[string]bool // instanceName -> should fail
	branchSeen map[string]string
}

func (f *fakeTeardownBuilder) BuildContainerTeardownStep(_ context.Context, target *domain.DeployTarget) (pipeline.Blueprint, string, error) {
	if f.branchSeen != nil {
		f.branchSeen[target.EnvTarget.InstanceName] = target.Branch
	}
	var failErr error
	if f.failFor[target.EnvTarget.InstanceName] {
		failErr = errors.New("teardown boom")
	}
	return pipeline.Blueprint{Name: "compose_down", Exec: &stubStep{ran: f.ran, failErr: failErr}}, "/tmp", nil
}

func TestReconcileStopped_TearsDownEachAndResolvesBranch(t *testing.T) {
	ran := 0
	branchSeen := map[string]string{}
	targets := []*domain.ApplicationEnvTarget{
		{ID: 1, ApplicationID: 100, Env: domain.EnvironmentProd, InstanceName: "default", DesiredState: domain.RuntimeStateStopped},
		{ID: 2, ApplicationID: 100, Env: domain.EnvironmentProd, InstanceName: "blue", DesiredState: domain.RuntimeStateStopped},
	}
	r := NewInstanceReconcile(
		&fakeStoppedLister{byMachine: map[int64][]*domain.ApplicationEnvTarget{7: targets}},
		&fakeTeardownBuilder{ran: &ran, branchSeen: branchSeen},
		&fakeAppGetter{apps: map[int64]*domain.Application{100: {ID: 100, Name: "myapp"}}},
		&fakeReleaseGetter{branch: map[int64]string{100: "master"}},
	)

	n, err := r.ReconcileStopped(context.Background(), 7)
	if err != nil {
		t.Fatalf("ReconcileStopped: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 instances torn down, got %d", n)
	}
	if ran != 2 {
		t.Fatalf("expected 2 teardown steps executed, got %d", ran)
	}
	// Branch resolved from the latest verified release must flow into the builder.
	if branchSeen["default"] != "master" || branchSeen["blue"] != "master" {
		t.Fatalf("expected branch 'master' passed to builder, got %v", branchSeen)
	}
}

func TestReconcileStopped_OneFailureDoesNotBlockOthers(t *testing.T) {
	ran := 0
	targets := []*domain.ApplicationEnvTarget{
		{ID: 1, ApplicationID: 100, Env: domain.EnvironmentProd, InstanceName: "bad", DesiredState: domain.RuntimeStateStopped},
		{ID: 2, ApplicationID: 100, Env: domain.EnvironmentProd, InstanceName: "good", DesiredState: domain.RuntimeStateStopped},
	}
	r := NewInstanceReconcile(
		&fakeStoppedLister{byMachine: map[int64][]*domain.ApplicationEnvTarget{7: targets}},
		&fakeTeardownBuilder{ran: &ran, failFor: map[string]bool{"bad": true}},
		&fakeAppGetter{apps: map[int64]*domain.Application{100: {ID: 100, Name: "myapp"}}},
		&fakeReleaseGetter{},
	)

	n, err := r.ReconcileStopped(context.Background(), 7)
	if err != nil {
		t.Fatalf("ReconcileStopped: %v", err)
	}
	// Both steps attempted; only the good one counts as reconciled.
	if ran != 2 {
		t.Fatalf("expected both teardown steps attempted, got %d", ran)
	}
	if n != 1 {
		t.Fatalf("expected 1 successful reconcile, got %d", n)
	}
}

func TestReconcileStopped_NoStoppedInstancesIsNoop(t *testing.T) {
	ran := 0
	r := NewInstanceReconcile(
		&fakeStoppedLister{byMachine: map[int64][]*domain.ApplicationEnvTarget{}},
		&fakeTeardownBuilder{ran: &ran},
		&fakeAppGetter{},
		&fakeReleaseGetter{},
	)
	n, err := r.ReconcileStopped(context.Background(), 7)
	if err != nil || n != 0 {
		t.Fatalf("expected clean no-op, got n=%d err=%v", n, err)
	}
	if ran != 0 {
		t.Fatalf("expected no teardown when nothing is stopped, got %d", ran)
	}
}
