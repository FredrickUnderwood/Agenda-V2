package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/git"
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// machineGetter resolves a machine with its agent token already decrypted to
// plaintext, ready to present to that machine's agenda-node — satisfied by
// *MachineService.Get. A plain *repository.MachineRepository would return the
// token still encrypted at rest, which breaks the outbound node call below.
type machineGetter interface {
	Get(ctx context.Context, id int64) (*domain.Machine, error)
}

// ApplicationLogService fetches a running instance's runtime logs (written by
// sdk/go/log on the deployed app's own machine) from its agenda-node agent.
// It reads Application/ApplicationEnvTarget/ApplicationRelease directly via
// their repositories, for the same reason ApplicationReleaseService does —
// plain point-in-time data assembly to resolve which machine and host
// directory to ask, not business-logic delegation. Machine goes through
// machineGetter instead, since its agent_token needs decrypting (see below).
type ApplicationLogService struct {
	apps          *repository.ApplicationRepository
	targets       *repository.ApplicationTargetRepository
	releases      *repository.ApplicationReleaseRepository
	machines      machineGetter
	workspaceRoot string
}

func NewApplicationLogService(
	apps *repository.ApplicationRepository,
	targets *repository.ApplicationTargetRepository,
	releases *repository.ApplicationReleaseRepository,
	machines machineGetter,
	workspaceRoot string,
) *ApplicationLogService {
	return &ApplicationLogService{apps: apps, targets: targets, releases: releases, machines: machines, workspaceRoot: workspaceRoot}
}

// GetInstanceLogs resolves targetID's machine, then asks that machine's
// agenda-node agent to tail the instance's logs. service == "" returns every
// service's log file; tail <= 0 leaves the node's default.
//
// The log directory is resolved from the stable (app, env, instance) identity
// (git.InstanceLogDir → <root>/run/<app>/<env>/<instance>/logs), NOT from any
// release branch: the running container writes there regardless of which branch
// or verify state it's in, so a freshly-deployed, not-yet-verified candidate
// (e.g. a blue/green slot mid-rollout) still shows logs. For instances deployed
// before the run/ layout existed, it falls back to the legacy per-branch path
// under the code checkout.
func (s *ApplicationLogService) GetInstanceLogs(ctx context.Context, appID, targetID int64, service string, tail int) (*contract.NodeLogsResponse, error) {
	target, err := s.targets.GetByID(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if target.ApplicationID != appID {
		return nil, fmt.Errorf("application %d target %d not found", appID, targetID)
	}
	app, err := s.apps.GetByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	if target.MachineID <= 0 {
		return nil, errors.New("target has no machine assigned")
	}
	machine, err := s.machines.Get(ctx, target.MachineID)
	if err != nil {
		return nil, err
	}
	mc := ToMachineConfig(machine)
	if !mc.IsAgent() {
		return nil, errors.New("machine is not in agent mode; log reading requires agenda-node")
	}

	root := mc.WorkspaceRoot
	if root == "" {
		root = s.workspaceRoot
	}
	logDir, err := git.InstanceLogDir(root, app.Name, string(target.Env), target.InstanceName, mc.IsLocal())
	if err != nil {
		return nil, err
	}

	resp, err := nodeproxy.FetchLogs(ctx, mc.AgentBaseURL, mc.AgentToken, app.Name, target.InstanceName, logDir, service, tail)
	if err == nil {
		return resp, nil
	}

	// Back-compat: instances deployed before the run/ layout wrote their logs
	// under the code checkout at <root>/<host>/<repo>/<branch>/logs. Try that,
	// keyed on the latest verified release's branch, before surfacing the error.
	if legacy := s.legacyInstanceLogs(ctx, appID, app, target, mc, root, service, tail); legacy != nil {
		return legacy, nil
	}
	return nil, err
}

// legacyInstanceLogs resolves logs from the pre-run/ layout (logs living inside
// the branch-specific code checkout). Returns nil when there's no verified
// release to derive a branch from, or the legacy fetch fails — the caller then
// surfaces the primary (new-path) error.
func (s *ApplicationLogService) legacyInstanceLogs(ctx context.Context, appID int64, app *domain.Application, target *domain.ApplicationEnvTarget, mc *config.MachineConfig, root, service string, tail int) *contract.NodeLogsResponse {
	release, err := s.releases.GetLatestVerified(ctx, appID, target.Env, target.InstanceName)
	if err != nil || release == nil {
		return nil
	}
	localPath, err := git.ResolveLocalPath(app.RepoURL, release.Branch, root, mc.IsLocal())
	if err != nil {
		return nil
	}
	resp, err := nodeproxy.FetchLogs(ctx, mc.AgentBaseURL, mc.AgentToken, app.Name, target.InstanceName, filepath.Join(localPath, "logs"), service, tail)
	if err != nil {
		return nil
	}
	return resp
}
