package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// ComposeDownStep tears down one instance's containers on its machine. It is
// deliberately checkout-independent: rather than `docker compose down` (which
// needs the compose file present and a branch-specific project name), it removes
// containers by the agenda identity labels the compose-override step stamps on
// them (contract.Label*), so it finds every container the instance left behind
// regardless of which branch is currently checked out — or whether the checkout
// still exists at all.
//
// ProjectName, when known (the instance's current running branch resolves it),
// adds two best-effort passes the label filter can't cover: removing containers
// from instances deployed before agenda labels existed (they still carry docker
// compose's own com.docker.compose.project label) and cleaning up the now-empty
// compose network. Both tolerate failure — the authoritative pass is the
// label-based container removal.
//
// It never removes named volumes (RemoveVolumes is reserved for a future
// explicit "delete data" path): an app's database volume must survive a
// decommission so the instance can be brought back with its data intact.
type ComposeDownStep struct {
	Machine      *config.MachineConfig
	AppName      string
	EnvName      string
	InstanceName string
	// ProjectName is app-branch-env-instance (util.Slug'd), resolved from the
	// instance's current running branch. Empty when no branch is known — then
	// only the label-based pass runs.
	ProjectName string
	// RemoveVolumes deletes the instance's named volumes too. Off for
	// decommission (keep data); wired for a future "delete instance data" path.
	RemoveVolumes bool
}

func (s *ComposeDownStep) Execute(ctx context.Context, rc *RunContext) error {
	r := runner.New(s.Machine)
	return r.RunShell(ctx, "", s.script(), rc.Output)
}

// script builds the /bin/sh teardown. It is idempotent by construction: every
// removal is guarded by a non-empty check, so re-running it against an
// already-torn-down instance is a clean no-op — which is what makes a failed
// decommission safe to simply retry.
func (s *ComposeDownStep) script() string {
	var b strings.Builder
	b.WriteString("set -e\n")

	// 1) Authoritative: remove this instance's containers by agenda labels.
	// Branch-independent and orphan-proof — this is the pass that must succeed.
	fmt.Fprintf(&b, "labeled=$(docker ps -aq --filter label=%s --filter label=%s --filter label=%s)\n",
		shQuote(contract.LabelApp+"="+s.AppName),
		shQuote(contract.LabelEnv+"="+s.EnvName),
		shQuote(contract.LabelInstance+"="+s.InstanceName),
	)
	b.WriteString(`if [ -n "$labeled" ]; then echo "removing labeled containers: $labeled"; docker rm -f $labeled; else echo "no agenda-labeled containers found"; fi` + "\n")

	// 2) Best-effort, project-scoped: catch pre-label instances (they carry
	// docker compose's own project label) and clean up the compose network /
	// optional volumes. All tolerate failure so they never fail the teardown.
	if s.ProjectName != "" {
		projFilter := shQuote("com.docker.compose.project=" + s.ProjectName)
		fmt.Fprintf(&b, "projcids=$(docker ps -aq --filter label=%s || true)\n", projFilter)
		b.WriteString(`if [ -n "$projcids" ]; then echo "removing compose-project containers: $projcids"; docker rm -f $projcids || true; fi` + "\n")
		if s.RemoveVolumes {
			fmt.Fprintf(&b, "vols=$(docker volume ls -q --filter label=%s || true)\n", projFilter)
			b.WriteString(`if [ -n "$vols" ]; then echo "removing volumes: $vols"; docker volume rm $vols || true; fi` + "\n")
		}
		fmt.Fprintf(&b, "nets=$(docker network ls -q --filter label=%s || true)\n", projFilter)
		b.WriteString(`if [ -n "$nets" ]; then docker network rm $nets >/dev/null 2>&1 || true; fi` + "\n")
	}
	return b.String()
}
