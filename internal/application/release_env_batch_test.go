package application

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// newBatchTestApp wires just enough of ReleaseApplication to exercise the
// batch-level release bookkeeping. Only releaseSvc and envDeploySvc are needed:
// VerifyEnv never touches the pipeline, and leaving the rest nil is deliberate
// — a test that accidentally reaches the builder or the runner should crash
// rather than quietly try to deploy something.
func newBatchTestApp(t *testing.T) (*ReleaseApplication, *gorm.DB) {
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
		&domain.EnvDeployment{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	releaseSvc := service.NewApplicationReleaseService(
		repository.NewApplicationReleaseRepository(db),
		repository.NewApplicationRepository(db),
		repository.NewApplicationTargetRepository(db),
		repository.NewApplicationGatewayRouteRepository(db),
		repository.NewApplicationEnvironmentRepository(db),
	)
	envDeploySvc := service.NewEnvDeploymentService(
		repository.NewEnvDeploymentRepository(db),
		repository.NewApplicationReleaseRepository(db),
	)
	return &ReleaseApplication{releaseSvc: releaseSvc, envDeploySvc: envDeploySvc}, db
}

func seedBatch(t *testing.T, db *gorm.DB) *domain.EnvDeployment {
	t.Helper()
	batch := &domain.EnvDeployment{
		ApplicationID: 1, Env: domain.EnvironmentProd, Branch: "master",
		Status: domain.EnvDeploymentStatusRunning, StartedAt: time.Now().UTC(),
	}
	if err := db.Create(batch).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	return batch
}

func seedChild(t *testing.T, db *gorm.DB, batchID int64, instance string, status domain.ReleaseStatus) *domain.ApplicationRelease {
	t.Helper()
	rel := &domain.ApplicationRelease{
		ApplicationID: 1, Env: domain.EnvironmentProd, InstanceName: instance,
		EnvDeploymentID: batchID, Branch: "master", CommitSHA: "cccccccccccc", Status: status,
		DeployConfigSnapshot: "{}", EnvConfigSnapshot: "", RouteSnapshot: "[]",
	}
	if err := db.Create(rel).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}
	return rel
}

// One check of the environment closes out the whole rollout, and a child that
// failed (or was already verified on its own) must not block that.
func TestVerifyEnvVerifiesEveryPendingChild(t *testing.T) {
	app, db := newBatchTestApp(t)
	ctx := context.Background()
	batch := seedBatch(t, db)
	blue := seedChild(t, db, batch.ID, "blue", domain.ReleaseStatusPendingVerify)
	green := seedChild(t, db, batch.ID, "green", domain.ReleaseStatusPendingVerify)
	failed := seedChild(t, db, batch.ID, "red", domain.ReleaseStatusFailed)

	got, err := app.VerifyEnv(ctx, batch.ID)
	if err != nil {
		t.Fatalf("VerifyEnv: %v", err)
	}

	byID := map[int64]domain.ReleaseStatus{}
	for _, rel := range got.Releases {
		byID[rel.ID] = rel.Status
	}
	if byID[blue.ID] != domain.ReleaseStatusVerified || byID[green.ID] != domain.ReleaseStatusVerified {
		t.Fatalf("children not verified: blue=%s green=%s", byID[blue.ID], byID[green.ID])
	}
	if byID[failed.ID] != domain.ReleaseStatusFailed {
		t.Fatalf("failed child = %s, want to be left alone", byID[failed.ID])
	}

	// verified_at must actually be written — it is what the release history
	// reads back as "known good".
	var stored domain.ApplicationRelease
	if err := db.First(&stored, blue.ID).Error; err != nil {
		t.Fatalf("reload blue: %v", err)
	}
	if stored.VerifiedAt == nil {
		t.Fatal("verified_at was not recorded")
	}
}

func TestVerifyEnvRejectsBatchWithNothingAwaitingVerification(t *testing.T) {
	app, db := newBatchTestApp(t)
	ctx := context.Background()
	batch := seedBatch(t, db)
	seedChild(t, db, batch.ID, "blue", domain.ReleaseStatusVerified)
	seedChild(t, db, batch.ID, "green", domain.ReleaseStatusFailed)

	if _, err := app.VerifyEnv(ctx, batch.ID); err == nil {
		t.Fatal("VerifyEnv succeeded on a batch with nothing left to verify")
	}
}

// A double-submitted rollback must not create a second batch: both would plan
// against children that are still pending_verify and then race on each
// instance's deploy lock.
func TestRollbackEnvRefusesWhileAnEarlierRollbackIsUnfinished(t *testing.T) {
	app, db := newBatchTestApp(t)
	ctx := context.Background()
	bad := seedBatch(t, db)
	seedChild(t, db, bad.ID, "blue", domain.ReleaseStatusPendingVerify)

	inFlight := &domain.EnvDeployment{
		ApplicationID: 1, Env: domain.EnvironmentProd, Branch: "master",
		RollbackOfID: bad.ID, Status: domain.EnvDeploymentStatusRunning, StartedAt: time.Now().UTC(),
	}
	if err := db.Create(inFlight).Error; err != nil {
		t.Fatalf("create in-flight rollback: %v", err)
	}

	if _, err := app.RollbackEnv(ctx, bad.ID, "bob"); err == nil {
		t.Fatal("RollbackEnv started a second rollback while one was still running")
	}

	// Once that rollback finishes, the batch is rollback-able again — the guard
	// is against concurrency, not a permanent one-rollback-per-deploy rule.
	if err := db.Model(inFlight).Update("status", domain.EnvDeploymentStatusSuccess).Error; err != nil {
		t.Fatalf("finish in-flight rollback: %v", err)
	}
	got, err := app.envDeploySvc.GetUnfinishedRollbackOf(ctx, bad.ID)
	if err != nil {
		t.Fatalf("GetUnfinishedRollbackOf: %v", err)
	}
	if got != nil {
		t.Fatalf("a finished rollback still blocks: #%d", got.ID)
	}
}

func TestRollbackBatchPin(t *testing.T) {
	pair := func(branch, sha string) service.EnvRollbackPair {
		return service.EnvRollbackPair{
			Bad:    &domain.ApplicationRelease{},
			Target: &domain.ApplicationRelease{Branch: branch, CommitSHA: sha},
		}
	}
	cases := []struct {
		name       string
		pairs      []service.EnvRollbackPair
		fallback   string
		wantBranch string
		wantSHA    string
	}{
		{
			name:       "every instance agrees",
			pairs:      []service.EnvRollbackPair{pair("master", "aaa"), pair("master", "aaa")},
			fallback:   "release/1",
			wantBranch: "master",
			wantSHA:    "aaa",
		},
		{
			// Instances resolve their targets independently, so they can land
			// on different commits; the batch row then records no single one.
			name:       "instances land on different commits",
			pairs:      []service.EnvRollbackPair{pair("master", "aaa"), pair("master", "bbb")},
			fallback:   "release/1",
			wantBranch: "master",
			wantSHA:    "",
		},
		{
			name:       "instances land on different branches",
			pairs:      []service.EnvRollbackPair{pair("master", "aaa"), pair("hotfix", "bbb")},
			fallback:   "release/1",
			wantBranch: "release/1",
			wantSHA:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			branch, sha := rollbackBatchPin(tc.pairs, tc.fallback)
			if branch != tc.wantBranch || sha != tc.wantSHA {
				t.Fatalf("rollbackBatchPin = (%q, %q), want (%q, %q)", branch, sha, tc.wantBranch, tc.wantSHA)
			}
		})
	}
}
