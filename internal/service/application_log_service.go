package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/git"
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// ApplicationLogService fetches a running instance's runtime logs (written by
// sdk/go/log on the deployed app's own machine) from its agenda-node agent.
// It reads Application/ApplicationEnvTarget/ApplicationRelease/Machine
// directly via their repositories, for the same reason
// ApplicationReleaseService does — plain point-in-time data assembly to
// resolve which machine and host directory to ask, not business-logic
// delegation.
type ApplicationLogService struct {
	apps          *repository.ApplicationRepository
	targets       *repository.ApplicationTargetRepository
	releases      *repository.ApplicationReleaseRepository
	machines      *repository.MachineRepository
	workspaceRoot string
}

func NewApplicationLogService(
	apps *repository.ApplicationRepository,
	targets *repository.ApplicationTargetRepository,
	releases *repository.ApplicationReleaseRepository,
	machines *repository.MachineRepository,
	workspaceRoot string,
) *ApplicationLogService {
	return &ApplicationLogService{apps: apps, targets: targets, releases: releases, machines: machines, workspaceRoot: workspaceRoot}
}

// GetInstanceLogs resolves targetID's machine and current release branch,
// then asks that machine's agenda-node agent to tail the logs. service ==
// ""  returns every service's log file; tail <= 0 leaves the node's default.
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
	machine, err := s.machines.GetByID(ctx, target.MachineID)
	if err != nil {
		return nil, err
	}
	mc := ToMachineConfig(machine)
	if !mc.IsAgent() {
		return nil, errors.New("machine is not in agent mode; log reading requires agenda-node")
	}

	release, err := s.releases.GetLatestVerified(ctx, appID, target.Env, target.InstanceName)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, errors.New("no verified release found for this instance")
	}

	root := mc.WorkspaceRoot
	if root == "" {
		root = s.workspaceRoot
	}
	localPath, err := git.ResolveLocalPath(app.RepoURL, release.Branch, root, mc.IsLocal())
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(localPath, "logs")

	return nodeproxy.FetchLogs(ctx, mc.AgentBaseURL, mc.AgentToken, app.Name, target.InstanceName, logDir, service, tail)
}
