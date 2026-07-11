#!/usr/bin/env bash
# All-in-one single-host quickstart deployer for agenda-v2.
#
# Brings up MySQL + Redis + all three binaries (control plane, gateway, node)
# + the web console via deploy/quickstart/docker-compose.yml, generates and
# persists secrets on first run, and auto-registers the node as an agent-mode
# machine through the control plane's own API (the same "leave the token
# blank, let the server generate + encrypt it" path the UI uses).
#
# This is a single-machine dev/staging quickstart, not a production topology
# guide — for a multi-machine deployment, provision agenda-node on each
# target host separately (see cmd/agenda-node/README.md) and use the web
# console to add machines instead of this script's auto-bootstrap.
#
# Usage:
#   ./deploy.sh up                 # build + start core stack (idempotent)
#   ./deploy.sh up --observability # also start Prometheus + Grafana
#   ./deploy.sh status             # container state + health endpoints
#   ./deploy.sh logs [service]     # tail logs (all services if omitted)
#   ./deploy.sh down               # stop containers, keep data + secrets
#   ./deploy.sh reset              # stop + wipe volumes AND generated
#                                   # config/secrets (destructive, asks first)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QS_DIR="$ROOT_DIR/deploy/quickstart"
CONFIG_DIR="$QS_DIR/config"
ENV_FILE="$QS_DIR/.env"
COMPOSE=(docker compose -f "$QS_DIR/docker-compose.yml" --env-file "$ENV_FILE")

CONTROL_PLANE_URL_DEFAULT="http://localhost:8080"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

require_bin() {
    command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1 ($2)"
}

check_prereqs() {
    require_bin docker "install Docker Desktop / Docker Engine"
    docker compose version >/dev/null 2>&1 || die "docker compose v2 plugin not found"
    require_bin curl "used to bootstrap the agent machine via the control-plane API"
    require_bin jq "used to parse API responses; brew install jq / apt install jq"
    require_bin openssl "used to generate secrets"
}

rand_hex() { openssl rand -hex "$1"; }

# ---------------------------------------------------------------------------
# Secrets: generated once into deploy/quickstart/.env, then reused forever.
# Re-running ./deploy.sh up never rotates them (rotating jwt_secret/master_key
# after data exists breaks existing sessions / encrypted Settings).
# ---------------------------------------------------------------------------
ensure_env_file() {
    mkdir -p "$QS_DIR"
    if [[ -f "$ENV_FILE" ]]; then
        log "reusing existing secrets: $ENV_FILE"
        return
    fi
    log "generating secrets (first run) -> $ENV_FILE"
    cat >"$ENV_FILE" <<EOF
MYSQL_ROOT_PASSWORD=$(rand_hex 16)
MYSQL_PASSWORD=$(rand_hex 16)
JWT_SECRET=$(rand_hex 32)
MASTER_KEY=$(rand_hex 32)
SERVER_AUTH_TOKEN=$(rand_hex 24)
GATEWAY_SERVICE_TOKEN=$(rand_hex 24)
SCRAPE_TOKEN=$(rand_hex 24)
ADMIN_USERNAME=admin
ADMIN_PASSWORD=$(rand_hex 12)
CONTROL_PLANE_PORT=8080
GATEWAY_PORT=8081
NODE_MGMT_PORT=7100
NODE_PROXY_PORT=7200
WEB_PORT=8090
PROMETHEUS_PORT=9090
GRAFANA_PORT=3000
EOF
    chmod 600 "$ENV_FILE"
}

# shellcheck disable=SC1090
load_env() { set -a; source "$ENV_FILE"; set +a; }

render_control_plane_config() {
    local out="$CONFIG_DIR/agenda-v2.yaml"
    mkdir -p "$CONFIG_DIR"
    # Always re-render from the current .env, never cache. This file is 100%
    # derived (see the "don't hand-edit" note in docker-compose.yml) — caching
    # it let it silently drift out of sync with .env whenever .env changed
    # (e.g. MYSQL_PASSWORD regenerated) but the config directory survived, which
    # produces a control-plane MySQL auth failure that's very hard to diagnose
    # (gateway reads its DSN live from .env via compose interpolation and keeps
    # working, so only control-plane breaks).
    log "rendering $out"
    cat >"$out" <<EOF
server:
  addr: ":8080"
  auth_token: "${SERVER_AUTH_TOKEN}"
  pprof_addr: "127.0.0.1:6060"

database:
  dsn: "agenda:${MYSQL_PASSWORD}@tcp(mysql:3306)/agenda_v2?charset=utf8mb4&parseTime=True&loc=Local"

redis:
  addr: "redis:6379"
  username: ""
  password: ""
  db: 0

git:
  git_bin: "git"
  operation_timeout: "60s"
  tokens: {}

deploy:
  max_output_bytes: 65536
  default_timeout: "5m"
  agent_poll_interval: "2s"

gateway:
  enabled: true
  base_url: "http://gateway:8080"
  service_token: "${GATEWAY_SERVICE_TOKEN}"
  timeout: "10s"
  backend_scheme: "http"
  backend_host: "host.docker.internal"

log:
  level: "info"

security:
  master_key: "${MASTER_KEY}"

auth:
  jwt_secret: "${JWT_SECRET}"
  token_ttl: "24h"
  bootstrap_admin_username: "${ADMIN_USERNAME}"
  bootstrap_admin_password: "${ADMIN_PASSWORD}"

observability:
  alert_eval_interval: "30s"

workspace_root: "/root/.agenda-v2/workspaces"

machines: {}
EOF
}

render_node_config() {
    local machine_id="$1" agent_token="$2"
    local out="$CONFIG_DIR/agenda-node.yaml"
    log "rendering $out (machine_id=${machine_id})"
    cat >"$out" <<EOF
listen_addr: "0.0.0.0:7100"
proxy_listen_addr: "0.0.0.0:7200"
machine_id: ${machine_id}
token: "${agent_token}"
central_base_url: "http://control-plane:8080"
heartbeat_interval: "15s"
max_output_bytes: 65536
job_retention: "1h"
EOF
}

render_prometheus_config() {
    local out="$CONFIG_DIR/prometheus.yml"
    log "rendering $out"
    sed "s/\${SCRAPE_TOKEN}/${SCRAPE_TOKEN}/g" "$QS_DIR/prometheus.yml.tmpl" >"$out"
}

wait_for() {
    local name="$1" url="$2" tries="${3:-40}"
    log "waiting for ${name} (${url}) ..."
    for ((i = 1; i <= tries; i++)); do
        if curl -fsS -o /dev/null "$url" 2>/dev/null; then
            log "${name} is up"
            return 0
        fi
        sleep 3
    done
    die "${name} did not become healthy in time — check: ${COMPOSE[*]} logs"
}

# ---------------------------------------------------------------------------
# One-time bootstrap: log in as the seeded admin, create the node as an
# agent-mode machine (server generates + encrypts agent_token for us, same
# path the web UI's "create machine" form uses), render agenda-node.yaml from
# the response, then start the node container.
# ---------------------------------------------------------------------------
bootstrap_node() {
    local cp_url="http://localhost:${CONTROL_PLANE_PORT}"

    if [[ -f "$CONFIG_DIR/agenda-node.yaml" ]]; then
        log "agenda-node.yaml already exists, skipping machine auto-registration"
        return
    fi

    log "logging in as bootstrap admin to register the quickstart node"
    local login_resp token
    login_resp=$(curl -fsS -X POST "$cp_url/api/v1/auth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"username\":\"${ADMIN_USERNAME}\",\"password\":\"${ADMIN_PASSWORD}\"}") \
        || die "login failed — check control-plane logs: ${COMPOSE[*]} logs control-plane"
    token=$(jq -r '.token' <<<"$login_resp")
    [[ -n "$token" && "$token" != "null" ]] || die "login response had no token: $login_resp"

    log "creating agent-mode machine 'quickstart-node'"
    local create_resp machine_id agent_token
    create_resp=$(curl -fsS -X POST "$cp_url/api/v1/machines" \
        -H "Authorization: Bearer ${token}" \
        -H 'Content-Type: application/json' \
        -d '{
              "name": "quickstart-node",
              "machine_type": "prod",
              "mode": "agent",
              "agent_base_url": "http://node:7100",
              "agent_proxy_base_url": "http://node:7200",
              "workspace_root": "/root/.agenda-v2/workspaces"
            }') \
        || die "machine creation failed"
    machine_id=$(jq -r '.machine.id' <<<"$create_resp")
    agent_token=$(jq -r '.agent_token' <<<"$create_resp")
    [[ -n "$machine_id" && "$machine_id" != "null" ]] || die "no machine id in response: $create_resp"
    [[ -n "$agent_token" && "$agent_token" != "null" ]] || die "no agent_token in response: $create_resp"

    render_node_config "$machine_id" "$agent_token"

    log "starting node container"
    "${COMPOSE[@]}" up -d --build node
    wait_for "agenda-node" "http://localhost:${NODE_MGMT_PORT}/v1/health"

    log "waiting for first heartbeat (up to 45s) so the machine shows Online ..."
    for ((i = 1; i <= 15; i++)); do
        local online
        online=$(curl -fsS -H "Authorization: Bearer ${token}" "$cp_url/api/v1/machines/${machine_id}" \
            | jq -r '.online // .machine.online // false' 2>/dev/null || echo false)
        if [[ "$online" == "true" ]]; then
            log "machine ${machine_id} is Online"
            return
        fi
        sleep 3
    done
    warn "machine ${machine_id} did not report Online yet — check node logs (./deploy.sh logs node), it may just need a bit longer"
}

enable_observability_settings() {
    local cp_url="http://localhost:${CONTROL_PLANE_PORT}"
    local token
    token=$(curl -fsS -X POST "$cp_url/api/v1/auth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"username\":\"${ADMIN_USERNAME}\",\"password\":\"${ADMIN_PASSWORD}\"}" | jq -r '.token')
    [[ -n "$token" && "$token" != "null" ]] || { warn "could not log in to set observability Settings, do it manually"; return; }

    curl -fsS -X PUT "$cp_url/api/v1/settings/observability.prometheus_url" \
        -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
        -d '{"value":"http://prometheus:9090","is_secret":false}' >/dev/null

    curl -fsS -X PUT "$cp_url/api/v1/settings/observability.scrape_token" \
        -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
        -d "{\"value\":\"${SCRAPE_TOKEN}\",\"is_secret\":true}" >/dev/null

    log "observability.prometheus_url / observability.scrape_token Settings configured"
}

print_summary() {
    cat <<EOF

────────────────────────────────────────────────────────────────────────────
 agenda-v2 quickstart is up
────────────────────────────────────────────────────────────────────────────
  Web console:      http://localhost:${WEB_PORT}
  Control plane API: http://localhost:${CONTROL_PLANE_PORT}
  Gateway:           http://localhost:${GATEWAY_PORT}
  Node mgmt API:      http://localhost:${NODE_MGMT_PORT} (internal use)

  Admin login:  ${ADMIN_USERNAME} / ${ADMIN_PASSWORD}
  Secrets file: deploy/quickstart/.env  (keep this out of version control)

  Next steps (see doc/deploy-smoke-test.md for the full walkthrough):
    1. Log into the web console, confirm the 'quickstart-node' machine shows Online.
    2. Create an application pointing at a repo with a docker-compose.yml.
    3. Deploy a release and watch the pipeline run through the node.
    4. Configure an alert channel (Settings) and try 'Test webhook'.
────────────────────────────────────────────────────────────────────────────
EOF
}

cmd_up() {
    local with_observability=false
    for arg in "$@"; do
        [[ "$arg" == "--observability" ]] && with_observability=true
    done

    check_prereqs
    ensure_env_file
    load_env
    render_control_plane_config

    log "building and starting mysql, redis, control-plane, gateway, web"
    "${COMPOSE[@]}" up -d --build mysql redis control-plane gateway web

    wait_for "control plane" "http://localhost:${CONTROL_PLANE_PORT}/api/v1/healthz"
    wait_for "gateway" "http://localhost:${GATEWAY_PORT}/-/health"

    bootstrap_node

    if $with_observability; then
        load_env
        render_prometheus_config
        log "starting prometheus + grafana"
        "${COMPOSE[@]}" --profile observability up -d prometheus grafana
        enable_observability_settings
        log "Grafana: http://localhost:3000  (anonymous viewer)"
    fi

    print_summary
}

cmd_status() {
    load_env 2>/dev/null || true
    "${COMPOSE[@]}" ps
    echo
    for pair in "control-plane:http://localhost:${CONTROL_PLANE_PORT:-8080}/api/v1/healthz" \
                "gateway:http://localhost:${GATEWAY_PORT:-8081}/-/health" \
                "node:http://localhost:${NODE_MGMT_PORT:-7100}/v1/health"; do
        name="${pair%%:*}"; url="${pair#*:}"
        if curl -fsS -o /dev/null "$url" 2>/dev/null; then
            echo "  $name: healthy"
        else
            echo "  $name: NOT reachable ($url)"
        fi
    done
}

cmd_logs() {
    "${COMPOSE[@]}" logs -f --tail=200 "$@"
}

cmd_down() {
    "${COMPOSE[@]}" down
}

cmd_reset() {
    warn "this stops all containers and DELETES volumes + generated config/secrets."
    read -r -p "Type 'reset' to confirm: " confirm
    [[ "$confirm" == "reset" ]] || die "aborted"
    "${COMPOSE[@]}" down -v || true
    rm -rf "$CONFIG_DIR" "$ENV_FILE"
    log "reset complete"
}

main() {
    local sub="${1:-up}"
    [[ $# -gt 0 ]] && shift
    case "$sub" in
        up) cmd_up "$@" ;;
        status) cmd_status ;;
        logs) cmd_logs "$@" ;;
        down) cmd_down ;;
        reset) cmd_reset ;;
        *) die "unknown command '$sub' (up|status|logs|down|reset)" ;;
    esac
}

main "$@"
