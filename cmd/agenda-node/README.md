# agenda-node

The resident per-machine agent. It replaces SSH as an execution path for the
control plane and acts as a local reverse proxy for gateway traffic. See the
full design in [`doc/agenda-node-tech-design.md`](../../doc/agenda-node-tech-design.md).

## What it does

- **Execution**: exposes a token-guarded management API (`/v1/jobs`) the control
  plane drives instead of SSH. Commands run locally on the machine (git, docker
  compose) exactly as the control plane's local runner would.
- **Reverse proxy**: forwards `/i/<instance>/…` on its proxy port to the
  instance's current local port, so gateway backend URLs stay stable across port
  drift.
- **Heartbeat**: periodically reports liveness so the control plane can show the
  machine as online.

## Ports

| Port | Purpose | Restrict to |
|------|---------|-------------|
| 7100 | management API (jobs, proxy registration) | control plane IP |
| 7200 | reverse proxy (business traffic) | agenda-gateway IP |

Both are equivalent to the old "control plane can SSH / gateway can reach the
port" trust boundaries — lock them down at the firewall the same way.

## Install (recommended: systemd)

1. Build the binary: `go build -o agenda-node ./cmd/agenda-node` (or grab a
   release artifact).
2. Copy `agenda-node.example.yaml` → `/etc/agenda-node/agenda-node.yaml` and fill
   in `machine_id`, `token`, `central_base_url`.
3. Install the unit (`agenda-node.service`) and `systemctl enable --now
   agenda-node`. `Restart=always` is deliberate — see the design's risk notes.

## Install (alternative: docker compose)

`docker compose -f cmd/agenda-node/docker-compose.yml up -d --build`. This mounts
the host docker socket and the workspace root so the containerized node can drive
host deployments. Prefer systemd unless you specifically want it containerized.

## Bootstrap (the chicken-and-egg)

agenda-node must already run on a machine before that machine can be switched to
agent mode — so the first install uses the existing SSH path:

1. With the machine still in `mode: ssh`, install agenda-node (systemd unit or
   compose) and start it.
2. Confirm the heartbeat reached the control plane (the machine shows
   `agent_last_heartbeat_at` / `online: true`).
3. Switch the machine's `mode` to `agent` (set `agent_base_url`,
   `agent_proxy_base_url`, and the matching `agent_token`). Subsequent deploys on
   this machine now go through the node.

To roll back, set `mode` back to `ssh` — the SSH config is untouched.
