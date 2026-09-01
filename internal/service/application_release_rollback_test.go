package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// Uses a real (in-memory) DB rather than fakes: what rollback resolution has to
// get right is which ROW out of an instance's release history is picked, which
// is a question about ordering and predicates that a fake repo would only
// assert back at itself.
func newRollbackTestService(t *testing.T) (*ApplicationReleaseService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.Application{},
		&domain.ApplicationEnvTarget{},
		&domain.ApplicationGatewayRoute{},
		&domain.ApplicationEnvironment{},
		&domain.ApplicationRelease{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := NewApplicationReleaseService(
		repository.NewApplicationReleaseRepository(db),
		repository.NewApplicationRepository(db),
		repository.NewApplicationTargetRepository(db),
		repository.NewApplicationGatewayRouteRepository(db),
		repository.NewApplicationEnvironmentRepository(db),
	)
	return svc, db
}

// seedRollbackApp creates one app with two enabled prod instances.
func seedRollbackApp(t *testing.T, db *gorm.DB) *domain.Application {
	t.Helper()
	app := &domain.Application{Name: "agenda-example", RepoURL: "https://example.com/x.git", DeployMethod: domain.DeployMethodDocker, DeployConfig: `{"v":"current"}`}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	for i, name := range []string{"blue", "green"} {
		target := &domain.ApplicationEnvTarget{
			ApplicationID: app.ID, Env: domain.EnvironmentProd, InstanceName: name,
			MachineID: int64(i + 1), Port: 18081 + i, Enabled: true, DesiredState: domain.RuntimeStateRunning,
		}
		if err := db.Create(target).Error; err != nil {
			t.Fatalf("create target %s: %v", name, err)
		}
	}
	return app
}

// seedRelease persists one release row directly, bypassing the state machine so
// a test can set up any history shape it needs.
func seedRelease(t *testing.T, db *gorm.DB, app *domain.Application, instance, sha string, status domain.ReleaseStatus) *domain.ApplicationRelease {
	t.Helper()
	rel := &domain.ApplicationRelease{
		ApplicationID: app.ID, Env: domain.EnvironmentProd, InstanceName: instance,
		Branch: "master", CommitSHA: sha, Status: status,
		DeployConfigSnapshot: `{"v":"at-the-time"}`, EnvConfigSnapshot: "", RouteSnapshot: "[]",
	}
	if status == domain.ReleaseStatusVerified {
		now := time.Now().UTC()
		rel.VerifiedAt = &now
	}
	if err := db.Create(rel).Error; err != nil {
		t.Fatalf("create release: %v", err)
	}
	return rel
}

func TestResolveRollbackTargetPicksPreviousVerified(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	old := seedRelease(t, db, app, "blue", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	seedRelease(t, db, app, "blue", "bbbbbbbbbbbb", domain.ReleaseStatusFailed)
	bad := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusPendingVerify)

	got, err := svc.ResolveRollbackTarget(ctx, bad.ID, 0)
	if err != nil {
		t.Fatalf("ResolveRollbackTarget: %v", err)
	}
	if got.ID != old.ID {
		t.Fatalf("target = #%d, want the last verified release #%d", got.ID, old.ID)
	}
}

// The problem is often only spotted after someone already clicked verify.
// Resolving "latest verified" would find the bad release itself and refuse.
func TestResolveRollbackTargetWhenBadIsAlreadyVerified(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	old := seedRelease(t, db, app, "blue", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	bad := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusVerified)

	got, err := svc.ResolveRollbackTarget(ctx, bad.ID, 0)
	if err != nil {
		t.Fatalf("ResolveRollbackTarget: %v", err)
	}
	if got.ID != old.ID {
		t.Fatalf("target = #%d, want the release before the bad one (#%d)", got.ID, old.ID)
	}
}

// A verified release on a *sibling* instance is not this instance's history.
func TestResolveRollbackTargetIgnoresOtherInstances(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	seedRelease(t, db, app, "green", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	bad := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusPendingVerify)

	if _, err := svc.ResolveRollbackTarget(ctx, bad.ID, 0); err == nil {
		t.Fatal("ResolveRollbackTarget succeeded; want an error since blue has no verified history")
	}
}

func TestResolveRollbackTargetRejectsExplicitBadTargets(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	otherInstance := seedRelease(t, db, app, "green", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	unverified := seedRelease(t, db, app, "blue", "bbbbbbbbbbbb", domain.ReleaseStatusPendingVerify)
	bad := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusPendingVerify)

	if _, err := svc.ResolveRollbackTarget(ctx, bad.ID, otherInstance.ID); err == nil {
		t.Fatal("rolling blue back onto green's release was accepted")
	}
	if _, err := svc.ResolveRollbackTarget(ctx, bad.ID, unverified.ID); err == nil {
		t.Fatal("rolling back to a release that was never verified was accepted")
	}
}

func TestCreateRollbackDraftLinksAndSnapshotsCurrentConfig(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	target := seedRelease(t, db, app, "blue", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	bad := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusPendingVerify)

	bad.Operator = "alice"
	rel, err := svc.CreateRollbackDraft(ctx, bad, target, 77, "bob")
	if err != nil {
		t.Fatalf("CreateRollbackDraft: %v", err)
	}
	// Whoever asked for the rollback owns it — attributing it to whoever ran
	// the bad deploy would misread deploy history exactly when it matters.
	if rel.Operator != "bob" {
		t.Fatalf("operator = %q, want the caller who asked for the rollback", rel.Operator)
	}
	if rel.CommitSHA != target.CommitSHA {
		t.Fatalf("commit_sha = %q, want the target's %q", rel.CommitSHA, target.CommitSHA)
	}
	if rel.PreviousReleaseID != bad.ID || rel.PreviousCommitSHA != bad.CommitSHA {
		t.Fatalf("previous = (#%d, %q), want (#%d, %q)", rel.PreviousReleaseID, rel.PreviousCommitSHA, bad.ID, bad.CommitSHA)
	}
	if rel.EnvDeploymentID != 77 {
		t.Fatalf("env_deployment_id = %d, want 77 (the rollback batch)", rel.EnvDeploymentID)
	}
	if rel.Status != domain.ReleaseStatusDraft {
		t.Fatalf("status = %q, want draft", rel.Status)
	}
	// Only the commit rolls back: config comes from the application as it is
	// now, not from the release being rolled back to.
	if rel.DeployConfigSnapshot != app.DeployConfig {
		t.Fatalf("deploy_config_snapshot = %q, want the application's current %q", rel.DeployConfigSnapshot, app.DeployConfig)
	}
}

func TestPlanEnvRollbackSkipsInstancesWithNothingToUndo(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	blueOld := seedRelease(t, db, app, "blue", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	blueBad := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusPendingVerify)
	// green's deploy never reached the instance, so there is nothing to undo
	// there — and crucially it must not fail the whole plan.
	greenBad := seedRelease(t, db, app, "green", "cccccccccccc", domain.ReleaseStatusFailed)

	pairs, err := svc.PlanEnvRollback(ctx, []*domain.ApplicationRelease{blueBad, greenBad})
	if err != nil {
		t.Fatalf("PlanEnvRollback: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("planned %d rollbacks, want 1 (green failed and never replaced anything)", len(pairs))
	}
	if pairs[0].Bad.ID != blueBad.ID || pairs[0].Target.ID != blueOld.ID {
		t.Fatalf("plan = (bad #%d -> target #%d), want (#%d -> #%d)",
			pairs[0].Bad.ID, pairs[0].Target.ID, blueBad.ID, blueOld.ID)
	}
}

// Half-rolling-back an environment leaves two versions behind one gateway
// route, which is worse than the bad deploy being undone.
func TestPlanEnvRollbackIsAllOrNothing(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	seedRelease(t, db, app, "blue", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	blueBad := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusPendingVerify)
	// green was added later and has never had a verified release.
	greenBad := seedRelease(t, db, app, "green", "cccccccccccc", domain.ReleaseStatusPendingVerify)

	_, err := svc.PlanEnvRollback(ctx, []*domain.ApplicationRelease{blueBad, greenBad})
	if err == nil {
		t.Fatal("PlanEnvRollback succeeded; want the whole plan rejected because green has no history")
	}
	if !strings.Contains(err.Error(), "green") {
		t.Fatalf("error %q does not name the instance that blocked the plan", err)
	}
}

func TestPlanEnvRollbackRefusesInFlightInstances(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	seedRelease(t, db, app, "blue", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	deploying := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusDeploying)

	if _, err := svc.PlanEnvRollback(ctx, []*domain.ApplicationRelease{deploying}); err == nil {
		t.Fatal("PlanEnvRollback accepted a release that is still deploying")
	}
}

// rolling_back records only that a replacement was once started, and nothing
// clears it when that replacement fails. Refusing to plan it would leave the
// instance running the bad code with no way to retry from here.
func TestPlanEnvRollbackReplaysAStuckRollback(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	good := seedRelease(t, db, app, "blue", "aaaaaaaaaaaa", domain.ReleaseStatusVerified)
	stuck := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusRollingBack)

	pairs, err := svc.PlanEnvRollback(ctx, []*domain.ApplicationRelease{stuck})
	if err != nil {
		t.Fatalf("PlanEnvRollback refused a stuck rollback: %v", err)
	}
	if len(pairs) != 1 || pairs[0].Target.ID != good.ID {
		t.Fatalf("plan = %+v, want blue replanned onto #%d", pairs, good.ID)
	}
}

func TestPlanEnvRollbackRejectsBatchWithNothingToRollBack(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	failed := seedRelease(t, db, app, "blue", "cccccccccccc", domain.ReleaseStatusFailed)
	alreadyDone := seedRelease(t, db, app, "green", "cccccccccccc", domain.ReleaseStatusRolledBack)

	if _, err := svc.PlanEnvRollback(ctx, []*domain.ApplicationRelease{failed, alreadyDone}); err == nil {
		t.Fatal("PlanEnvRollback succeeded on a batch where every instance was already settled")
	}
}

func TestCreateNormalizesCommitPin(t *testing.T) {
	svc, db := newRollbackTestService(t)
	app := seedRollbackApp(t, db)
	ctx := context.Background()

	rel, err := svc.Create(ctx, app.ID, CreateReleaseRequest{
		Env: domain.EnvironmentProd, InstanceName: "blue", Branch: "master", CommitSHA: "  8CE16504D4 ",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rel.CommitSHA != "8ce16504d4" {
		t.Fatalf("commit_sha = %q, want the trimmed lowercase pin", rel.CommitSHA)
	}

	if _, err := svc.Create(ctx, app.ID, CreateReleaseRequest{
		Env: domain.EnvironmentProd, InstanceName: "blue", Branch: "master", CommitSHA: "not-a-sha",
	}); err == nil {
		t.Fatal("Create accepted a commit pin that is not a SHA")
	}
}
