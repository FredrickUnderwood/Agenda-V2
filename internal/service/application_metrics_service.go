package service

import (
	"context"
	"errors"

	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// ApplicationMetricsService fetches a running instance's custom app metrics
// (written by sdk/go/metric on the deployed app's own machine) from its
// agenda-node agent, and enumerates the population of targets Prometheus
// should discover. It mirrors ApplicationLogService's shape closely — same
// machineGetter reuse, same reasoning for going through agenda-node instead
// of letting Prometheus reach app ports directly.
type ApplicationMetricsService struct {
	apps     *repository.ApplicationRepository
	targets  *repository.ApplicationTargetRepository
	machines machineGetter
}

func NewApplicationMetricsService(
	apps *repository.ApplicationRepository,
	targets *repository.ApplicationTargetRepository,
	machines machineGetter,
) *ApplicationMetricsService {
	return &ApplicationMetricsService{apps: apps, targets: targets, machines: machines}
}

// FetchInstanceMetrics resolves targetID's machine and asks its agenda-node
// agent to scrape the target's local metrics port. Unlike GetInstanceLogs, no
// release/branch/localPath resolution is needed — the metrics port is stable
// target-level config, not tied to a release's checkout path.
func (s *ApplicationMetricsService) FetchInstanceMetrics(ctx context.Context, targetID int64) (body []byte, contentType string, err error) {
	target, err := s.targets.GetByID(ctx, targetID)
	if err != nil {
		return nil, "", err
	}
	if !target.MetricsEnabled || target.MetricsPort <= 0 {
		return nil, "", errors.New("target does not have metrics enabled")
	}
	if target.MachineID <= 0 {
		return nil, "", errors.New("target has no machine assigned")
	}
	app, err := s.apps.GetByID(ctx, target.ApplicationID)
	if err != nil {
		return nil, "", err
	}
	machine, err := s.machines.Get(ctx, target.MachineID)
	if err != nil {
		return nil, "", err
	}
	mc := ToMachineConfig(machine)
	if !mc.IsAgent() {
		return nil, "", errors.New("machine is not in agent mode; metrics scraping requires agenda-node")
	}

	return nodeproxy.FetchMetrics(ctx, mc.AgentBaseURL, mc.AgentToken, app.Name, target.InstanceName, target.MetricsPort, "")
}

// ScrapeTarget is one row of the Prometheus http_sd_configs discovery
// response — enough identity for the handler to build labels + a query param
// carrying the target ID (the "multi-target exporter" relabeling pattern).
type ScrapeTarget struct {
	TargetID     int64
	AppName      string
	Env          string
	InstanceName string
}

// ListScrapeTargets returns every metrics-enabled, enabled target on an
// agent-mode machine — the population Prometheus's http_sd job discovers.
// SSH-mode machines are silently skipped (same limitation logs have: no
// resident agenda-node to relay through).
func (s *ApplicationMetricsService) ListScrapeTargets(ctx context.Context) ([]ScrapeTarget, error) {
	targets, err := s.targets.ListMetricsEnabled(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]ScrapeTarget, 0, len(targets))
	appNames := make(map[int64]string)
	for _, t := range targets {
		if t.MachineID <= 0 {
			continue
		}
		machine, err := s.machines.Get(ctx, t.MachineID)
		if err != nil {
			continue
		}
		mc := ToMachineConfig(machine)
		if !mc.IsAgent() {
			continue
		}
		appName, ok := appNames[t.ApplicationID]
		if !ok {
			app, err := s.apps.GetByID(ctx, t.ApplicationID)
			if err != nil {
				continue
			}
			appName = app.Name
			appNames[t.ApplicationID] = appName
		}
		out = append(out, ScrapeTarget{
			TargetID:     t.ID,
			AppName:      appName,
			Env:          string(t.Env),
			InstanceName: t.InstanceName,
		})
	}
	return out, nil
}
