package service

import (
	"context"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

func backfillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.Application{},
		&domain.ApplicationEnvironment{},
		&domain.ApplicationEnvTarget{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func runBackfill(t *testing.T, db *gorm.DB) int {
	t.Helper()
	n, err := BackfillApplicationEnvVars(
		context.Background(),
		repository.NewApplicationRepository(db),
		repository.NewApplicationEnvironmentRepository(db),
		repository.NewApplicationTargetRepository(db),
	)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	return n
}

func prodVars(t *testing.T, db *gorm.DB, appID int64) map[string]string {
	t.Helper()
	row, err := repository.NewApplicationEnvironmentRepository(db).
		GetByApplicationEnv(context.Background(), appID, domain.EnvironmentProd)
	if err != nil {
		t.Fatalf("get prod row: %v", err)
	}
	vars, err := row.ParseEnvVars()
	if err != nil {
		t.Fatalf("parse prod vars: %v", err)
	}
	return vars
}

func deployConfigOf(t *testing.T, db *gorm.DB, appID int64) map[string]any {
	t.Helper()
	var app domain.Application
	if err := db.First(&app, appID).Error; err != nil {
		t.Fatalf("reload app: %v", err)
	}
	var cfg map[string]any
	if err := sonic.UnmarshalString(app.DeployConfig, &cfg); err != nil {
		t.Fatalf("parse deploy_config: %v", err)
	}
	return cfg
}

// TestBackfillMovesBaselineToProd is the core migration property: the
// application-level baseline lands in the prod environment row and is removed
// from deploy_config, while every other deploy_config field survives untouched.
func TestBackfillMovesBaselineToProd(t *testing.T) {
	db := backfillTestDB(t)
	app := &domain.Application{
		Name: "svc", RepoURL: "git@example.com:x.git", DeployMethod: domain.DeployMethodDocker,
		DeployConfig: `{"work_dir":"./svc","compose_file":"docker-compose.yml","env":{"DB_DSN":"prod-dsn","LOG_LEVEL":"info"}}`,
	}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}

	if n := runBackfill(t, db); n != 1 {
		t.Fatalf("migrated = %d, want 1", n)
	}

	vars := prodVars(t, db, app.ID)
	if vars["DB_DSN"] != "prod-dsn" || vars["LOG_LEVEL"] != "info" {
		t.Fatalf("prod vars = %v, want the baseline", vars)
	}
	cfg := deployConfigOf(t, db, app.ID)
	if _, ok := cfg["env"]; ok {
		t.Fatalf("deploy_config still carries env: %v", cfg)
	}
	if cfg["work_dir"] != "./svc" || cfg["compose_file"] != "docker-compose.yml" {
		t.Fatalf("deploy_config lost fields: %v", cfg)
	}
}

// TestBackfillIsIdempotent pins that a second run is a no-op and — critically —
// does not undo a value the operator changed in the UI after migrating.
func TestBackfillIsIdempotent(t *testing.T) {
	db := backfillTestDB(t)
	app := &domain.Application{
		Name: "svc", RepoURL: "git@example.com:x.git", DeployMethod: domain.DeployMethodDocker,
		DeployConfig: `{"work_dir":".","env":{"LOG_LEVEL":"info"}}`,
	}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	runBackfill(t, db)

	svc := NewApplicationEnvironmentService(repository.NewApplicationEnvironmentRepository(db))
	if _, err := svc.Upsert(context.Background(), app.ID, domain.EnvironmentProd,
		UpsertApplicationEnvironmentRequest{EnvVars: map[string]string{"LOG_LEVEL": "debug"}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if n := runBackfill(t, db); n != 0 {
		t.Fatalf("second run migrated = %d, want 0", n)
	}
	if got := prodVars(t, db, app.ID)["LOG_LEVEL"]; got != "debug" {
		t.Fatalf("LOG_LEVEL = %q, want the operator's edit to survive", got)
	}
}

// TestBackfillKeepsExistingProdValue: where both layers define a key, the prod
// row wins — it is the higher-priority layer at deploy time, so preserving it
// keeps the app's effective env unchanged across the migration.
func TestBackfillKeepsExistingProdValue(t *testing.T) {
	db := backfillTestDB(t)
	app := &domain.Application{
		Name: "svc", RepoURL: "git@example.com:x.git", DeployMethod: domain.DeployMethodDocker,
		DeployConfig: `{"env":{"LOG_LEVEL":"info","ONLY_BASELINE":"1"}}`,
	}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	if err := db.Create(&domain.ApplicationEnvironment{
		ApplicationID: app.ID, Env: domain.EnvironmentProd,
		EnvVarsJSON: `{"LOG_LEVEL":"warn"}`,
	}).Error; err != nil {
		t.Fatalf("create prod row: %v", err)
	}

	runBackfill(t, db)

	vars := prodVars(t, db, app.ID)
	if vars["LOG_LEVEL"] != "warn" {
		t.Fatalf("LOG_LEVEL = %q, want the env-level value to win", vars["LOG_LEVEL"])
	}
	if vars["ONLY_BASELINE"] != "1" {
		t.Fatalf("baseline-only key lost: %v", vars)
	}
}

// TestBackfillSkipsAPIApplications: an api-method application's deploy_config
// is a webhook spec with no env baseline, and must not be rewritten.
func TestBackfillSkipsAPIApplications(t *testing.T) {
	db := backfillTestDB(t)
	raw := `{"url":"https://hook.example.com","method":"POST"}`
	app := &domain.Application{
		Name: "hook", RepoURL: "git@example.com:x.git", DeployMethod: domain.DeployMethodAPI,
		DeployConfig: raw,
	}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}

	if n := runBackfill(t, db); n != 0 {
		t.Fatalf("migrated = %d, want 0", n)
	}
	var reloaded domain.Application
	if err := db.First(&reloaded, app.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.DeployConfig != raw {
		t.Fatalf("deploy_config rewritten: %q", reloaded.DeployConfig)
	}
}

// TestUpsertAllReplacesPerEnv covers the matrix write path: each environment is
// replaced independently, an empty value is stored verbatim (no inheritance),
// and environments absent from the payload are left alone.
func TestUpsertAllReplacesPerEnv(t *testing.T) {
	db := backfillTestDB(t)
	svc := NewApplicationEnvironmentService(repository.NewApplicationEnvironmentRepository(db))
	ctx := context.Background()

	if _, err := svc.UpsertAll(ctx, 7, UpsertApplicationEnvironmentsRequest{
		Envs: map[domain.Environment]map[string]string{
			domain.EnvironmentProd:  {"DB_DSN": "prod", "FEATURE_X": "on"},
			domain.EnvironmentStage: {"DB_DSN": "stage", "FEATURE_X": ""},
			domain.EnvironmentTest:  {},
		},
	}); err != nil {
		t.Fatalf("upsert all: %v", err)
	}

	got, err := svc.GetAll(ctx, 7)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	for _, env := range domain.AllEnvironments() {
		if _, ok := got.Envs[env]; !ok {
			t.Fatalf("env %s missing from response", env)
		}
	}
	if v, ok := got.Envs[domain.EnvironmentStage]["FEATURE_X"]; !ok || v != "" {
		t.Fatalf("stage FEATURE_X = (%q, present=%v), want an empty string", v, ok)
	}
	if len(got.Envs[domain.EnvironmentTest]) != 0 {
		t.Fatalf("test env = %v, want empty", got.Envs[domain.EnvironmentTest])
	}

	// A payload naming only stage must not touch prod.
	if _, err := svc.UpsertAll(ctx, 7, UpsertApplicationEnvironmentsRequest{
		Envs: map[domain.Environment]map[string]string{domain.EnvironmentStage: {"DB_DSN": "stage2"}},
	}); err != nil {
		t.Fatalf("upsert stage: %v", err)
	}
	got, err = svc.GetAll(ctx, 7)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if got.Envs[domain.EnvironmentProd]["DB_DSN"] != "prod" {
		t.Fatalf("prod clobbered: %v", got.Envs[domain.EnvironmentProd])
	}
	if _, ok := got.Envs[domain.EnvironmentStage]["FEATURE_X"]; ok {
		t.Fatalf("stage should have been fully replaced: %v", got.Envs[domain.EnvironmentStage])
	}
}

// TestUpsertAllRejectsBadKeys: bad names fail the whole save rather than being
// silently dropped when the compose override is generated.
func TestUpsertAllRejectsBadKeys(t *testing.T) {
	db := backfillTestDB(t)
	svc := NewApplicationEnvironmentService(repository.NewApplicationEnvironmentRepository(db))

	for _, key := range []string{"AGENDA_LOG_DIR", "1BAD", "has-dash", "has space", ""} {
		_, err := svc.UpsertAll(context.Background(), 1, UpsertApplicationEnvironmentsRequest{
			Envs: map[domain.Environment]map[string]string{domain.EnvironmentProd: {key: "v"}},
		})
		if err == nil {
			t.Fatalf("key %q accepted, want rejection", key)
		}
	}
}
