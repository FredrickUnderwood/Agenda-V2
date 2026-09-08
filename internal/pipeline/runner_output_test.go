package pipeline

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/node"
	"github.com/FredrickUnderwood/agenda-v2/internal/outputtail"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

type outputTestStep func(context.Context, *RunContext) error

func (s outputTestStep) Execute(ctx context.Context, rc *RunContext) error { return s(ctx, rc) }

func outputTestPipeline(t *testing.T, maxBytes int, step Step) (*Runner, *domain.DeployLog, *service.DeployLogService, []Blueprint) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&domain.DeployLog{}, &domain.PipelineStep{}); err != nil {
		t.Fatal(err)
	}
	logs := repository.NewDeployLogRepository(db)
	steps := repository.NewPipelineStepRepository(db)
	logSvc := service.NewDeployLogService(logs, steps)
	stepSvc := service.NewPipelineStepService(steps)
	log := &domain.DeployLog{ApplicationID: 12, ReleaseID: 175, Env: domain.EnvironmentProd, InstanceName: "default-1", Status: domain.DeployStatusPending}
	if err := logSvc.Create(context.Background(), log); err != nil {
		t.Fatal(err)
	}
	bps := []Blueprint{{Name: "compose_up", Type: domain.StepTypeComposeUp, Exec: step}}
	if err := stepSvc.CreateBatch(context.Background(), BlueprintToSteps(log.ID, bps)); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Deploy: config.DeployConfig{MaxOutputBytes: maxBytes}}
	return NewRunner(cfg, logSvc, stepSvc), log, logSvc, bps
}

func TestPipelinePersistsBoundedAgentOutputAfterTimeout(t *testing.T) {
	jobs := node.NewJobStore(65536, time.Hour)
	server := httptest.NewServer(node.NewServer("tok", jobs, node.NewProxyRegistry(), "").Handler())
	defer server.Close()
	machine := &config.MachineConfig{Mode: "agent", AgentBaseURL: server.URL, AgentToken: "tok", AgentPollInterval: 10 * time.Millisecond}
	step := outputTestStep(func(ctx context.Context, rc *RunContext) error {
		return runner.New(machine).RunShell(ctx, "", "i=0; while [ $i -lt 1000 ]; do echo old-build-output; i=$((i+1)); done; echo final-build-progress >&2; sleep 30", rc.Output)
	})
	r, log, svc, bps := outputTestPipeline(t, 256, step)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	r.Run(ctx, log, &domain.DeployTarget{App: &domain.Application{ID: 12}}, bps, t.TempDir())
	saved, err := svc.GetWithSteps(context.Background(), log.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.DeployStatusFailed || saved.ErrorMsg != context.DeadlineExceeded.Error() {
		t.Fatalf("deploy = %+v", saved)
	}
	for _, got := range []string{saved.Output, saved.Steps[0].Output} {
		if len(got) > 256 || !strings.HasPrefix(got, outputtail.Marker) || !strings.Contains(got, "final-build-progress\n") || !strings.HasSuffix(got, "[error] context deadline exceeded\n") {
			t.Fatalf("persisted output = %q", got)
		}
	}
}

func TestPipelineLimitsStoredLinesAndMySQLTextBytes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		maxBytes int
		output   string
		wantTail string
	}{
		{"lines", 0, strings.Repeat("old\n", 500) + "last\n", "last\n"},
		{"oversized setting", 1 << 20, strings.Repeat("中文", 20000) + "last\n", "last\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := outputTestStep(func(_ context.Context, rc *RunContext) error {
				_, err := io.WriteString(rc.Output, tc.output)
				return err
			})
			r, log, svc, bps := outputTestPipeline(t, tc.maxBytes, step)
			r.Run(context.Background(), log, &domain.DeployTarget{App: &domain.Application{ID: 12}}, bps, t.TempDir())
			saved, err := svc.GetWithSteps(context.Background(), log.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := saved.Steps[0].Output
			if saved.Status != domain.DeployStatusSuccess || !strings.HasSuffix(got, tc.wantTail) || !strings.HasPrefix(got, outputtail.Marker) || !utf8.ValidString(got) {
				t.Fatalf("status=%s output=%.100q", saved.Status, got)
			}
			if len(got) > r.maxOutputBytes || len(got) > 65535 || strings.Count(strings.TrimPrefix(got, outputtail.Marker), "\n") > 200 {
				t.Fatalf("persisted output exceeds limit: %d bytes", len(got))
			}
		})
	}
}
