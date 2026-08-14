#!/usr/bin/env bash
# Interactive Docker Compose installer for a remote agenda-node.
#
# The machine must already exist in the agenda-v2 console. This installer asks
# for the machine ID, its one-time agent token, and the control-plane API base
# URL, verifies that tuple against the heartbeat endpoint, then builds and
# starts agenda-node with persistent config outside the source checkout.
set -euo pipefail

DEFAULT_INSTALL_DIR="/opt/agenda-node"
DEFAULT_WORKSPACE_ROOT="/root/.agenda-v2/workspaces"
DEFAULT_REPO_URL="https://github.com/FredrickUnderwood/Agenda-V2.git"
DEFAULT_REPO_REF="master"

INSTALL_DIR="${AGENDA_NODE_INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"
WORKSPACE_ROOT="${AGENDA_NODE_WORKSPACE_ROOT:-$DEFAULT_WORKSPACE_ROOT}"
REPO_URL="${AGENDA_NODE_REPO_URL:-$DEFAULT_REPO_URL}"
REPO_REF="${AGENDA_NODE_REPO_REF:-$DEFAULT_REPO_REF}"
CONFIG_DIR="$INSTALL_DIR/config"
CONFIG_FILE="$CONFIG_DIR/agenda-node.yaml"
COMPOSE_ENV_FILE="$INSTALL_DIR/compose.env"
SOURCE_DIR=""
PROMPT_RESULT=""

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

require_bin() {
    command -v "$1" >/dev/null 2>&1 || die "缺少命令 $1：$2"
}

check_prereqs() {
    [[ "$EUID" -eq 0 ]] || die "请使用 root 运行：sudo bash install-node.sh"
    [[ -r /dev/tty && -w /dev/tty ]] || die "该脚本需要交互式终端 (/dev/tty)"
    require_bin docker "请先安装 Docker Engine"
    docker compose version >/dev/null 2>&1 || die "未找到 Docker Compose v2 插件"
    docker info >/dev/null 2>&1 || die "Docker daemon 不可用，请先启动 Docker"
    require_bin curl "用于验证控制面 heartbeat 接口"
}

prompt_required() {
    local label="$1" secret="${2:-false}" value=""
    while [[ -z "$value" ]]; do
        printf '%s: ' "$label" >/dev/tty
        if [[ "$secret" == "true" ]]; then
            IFS= read -r -s value </dev/tty || die "读取输入失败"
            printf '\n' >/dev/tty
        else
            IFS= read -r value </dev/tty || die "读取输入失败"
        fi
        [[ -n "$value" ]] || warn "$label 不能为空"
    done
    PROMPT_RESULT="$value"
}

prompt_machine_id() {
    local value=""
    while true; do
        prompt_required "Machine ID"
        value="$PROMPT_RESULT"
        if [[ "$value" =~ ^[1-9][0-9]*$ ]]; then
            PROMPT_RESULT="$value"
            return
        fi
        warn "Machine ID 必须是大于 0 的整数"
    done
}

prompt_central_api() {
    local value=""
    while true; do
        prompt_required "Central API（例如 http://47.109.43.173:8080）"
        value="$PROMPT_RESULT"
        while [[ "$value" == */ ]]; do value="${value%/}"; done
        if [[ "$value" != http://* && "$value" != https://* ]]; then
            warn "Central API 必须以 http:// 或 https:// 开头"
            continue
        fi
        if [[ "$value" =~ [[:space:]] ]]; then
            warn "Central API 不能包含空格"
            continue
        fi
        if [[ "$value" == */api || "$value" == */api/v1 ]]; then
            warn "只填写基础地址，不要添加 /api 或 /api/v1"
            continue
        fi
        PROMPT_RESULT="$value"
        return
    done
}

confirm_overwrite() {
    [[ -f "$CONFIG_FILE" ]] || return
    warn "已存在配置：$CONFIG_FILE"
    local answer=""
    printf '重新填写并覆盖它？[y/N]: ' >/dev/tty
    IFS= read -r answer </dev/tty || die "读取输入失败"
    [[ "$answer" == "y" || "$answer" == "Y" ]] || die "已取消，现有配置未改动"
}

resolve_source_dir() {
    local script_parent="" candidate=""
    candidate="$(dirname "${BASH_SOURCE[0]}")"
    if [[ -d "$candidate" ]]; then
        script_parent="$(cd "$candidate" 2>/dev/null && pwd || true)"
    fi

    if [[ -n "${AGENDA_NODE_SOURCE_DIR:-}" ]]; then
        SOURCE_DIR="$AGENDA_NODE_SOURCE_DIR"
    elif [[ -n "$script_parent" && -f "$script_parent/go.mod" && -f "$script_parent/cmd/agenda-node/Dockerfile" ]]; then
        SOURCE_DIR="$script_parent"
        log "使用当前源码：$SOURCE_DIR"
    else
        require_bin git "用于下载 agenda-v2 源码"
        SOURCE_DIR="$INSTALL_DIR/source"
        if [[ -d "$SOURCE_DIR/.git" ]]; then
            log "复用已有源码：$SOURCE_DIR"
        elif [[ -e "$SOURCE_DIR" ]]; then
            die "$SOURCE_DIR 已存在但不是 Git 仓库，请移走后重试"
        else
            log "下载 agenda-v2 源码到 $SOURCE_DIR"
            git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" "$SOURCE_DIR"
        fi
    fi

    [[ -f "$SOURCE_DIR/cmd/agenda-node/docker-compose.yml" ]] || die "源码中缺少 cmd/agenda-node/docker-compose.yml"
    [[ -f "$SOURCE_DIR/cmd/agenda-node/Dockerfile" ]] || die "源码中缺少 cmd/agenda-node/Dockerfile"
    grep -q 'AGENDA_NODE_CONFIG_DIR' "$SOURCE_DIR/cmd/agenda-node/docker-compose.yml" \
        || die "agenda-node 源码版本过旧，请更新源码后重试"
}

yaml_quote() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    printf '"%s"' "$value"
}

probe_heartbeat() {
    local machine_id="$1" agent_token="$2" central_api="$3"
    local endpoint="$central_api/api/v1/machines/$machine_id/heartbeat"
    local body_file status body
    body_file="$(mktemp)"

    log "验证 Machine ID、Agent token 和 Central API"
    if ! status="$(curl -sS --connect-timeout 8 --max-time 15 \
        -o "$body_file" -w '%{http_code}' \
        -H 'Content-Type: application/json' \
        -H "X-Agenda-Node-Token: $agent_token" \
        --data '{"version":"installer-probe"}' \
        "$endpoint")"; then
        rm -f "$body_file"
        die "无法访问 $endpoint；请检查地址、端口、安全组和防火墙"
    fi

    body="$(head -c 500 "$body_file" 2>/dev/null || true)"
    rm -f "$body_file"
    case "$status" in
        2??)
            [[ "$body" == *'"ok":true'* || "$body" == *'"ok": true'* ]] \
                || die "heartbeat 返回 HTTP $status，但响应不是控制面预期格式：$body"
            log "heartbeat 校验成功"
            ;;
        401) die "heartbeat 返回 401：Agent token 与 Machine $machine_id 不匹配" ;;
        404) die "heartbeat 返回 404：Machine $machine_id 不存在，或 Central API 指向了错误/旧版服务" ;;
        *)   die "heartbeat 返回 HTTP $status：$body" ;;
    esac
}

render_files() {
    local machine_id="$1" agent_token="$2" central_api="$3"
    local token_yaml central_yaml
    token_yaml="$(yaml_quote "$agent_token")"
    central_yaml="$(yaml_quote "$central_api")"

    log "创建安装目录和 workspace"
    mkdir -p "$CONFIG_DIR" "$WORKSPACE_ROOT"
    chmod 700 "$CONFIG_DIR"

    cat >"$CONFIG_FILE" <<EOF
listen_addr: "0.0.0.0:7100"
proxy_listen_addr: "0.0.0.0:7200"

# agenda-node runs in a container but drives the host Docker daemon.
proxy_backend_host: "host.docker.internal"

machine_id: $machine_id
token: $token_yaml
central_base_url: $central_yaml

heartbeat_interval: "15s"
max_output_bytes: 65536
job_retention: "1h"
proxy_drain_timeout: "30s"
EOF
    chmod 600 "$CONFIG_FILE"

    cat >"$COMPOSE_ENV_FILE" <<EOF
AGENDA_NODE_CONFIG_DIR=$CONFIG_DIR
AGENDA_NODE_WORKSPACE_ROOT=$WORKSPACE_ROOT
EOF
    chmod 600 "$COMPOSE_ENV_FILE"
}

deploy_node() {
    local -a compose=(
        docker compose
        --project-name agenda-node
        --env-file "$COMPOSE_ENV_FILE"
        -f "$SOURCE_DIR/cmd/agenda-node/docker-compose.yml"
    )

    log "构建并启动 agenda-node"
    "${compose[@]}" up -d --build agenda-node

    log "等待 agenda-node 管理接口就绪"
    local attempt
    for ((attempt = 1; attempt <= 40; attempt++)); do
        if curl -fsS -o /dev/null http://127.0.0.1:7100/v1/health 2>/dev/null; then
            "${compose[@]}" ps
            printf '\n'
            log "agenda-node 安装成功"
            printf '  配置文件：%s\n' "$CONFIG_FILE"
            printf '  Workspace：%s\n' "$WORKSPACE_ROOT"
            printf '  管理端口：7100（仅允许控制面访问）\n'
            printf '  代理端口：7200（仅允许 agenda-gateway 访问）\n'
            printf '  查看日志：docker compose --project-name agenda-node --env-file %q -f %q logs -f agenda-node\n' \
                "$COMPOSE_ENV_FILE" "$SOURCE_DIR/cmd/agenda-node/docker-compose.yml"
            printf '\n请回到 Machines 页面确认状态已变为 Online。\n'
            return
        fi
        sleep 2
    done

    "${compose[@]}" logs --no-color --tail=100 agenda-node >&2 || true
    die "agenda-node 未能在 80 秒内通过健康检查"
}

main() {
    check_prereqs
    mkdir -p "$INSTALL_DIR"
    resolve_source_dir
    confirm_overwrite

    prompt_machine_id
    local machine_id="$PROMPT_RESULT"
    prompt_required "Agent token" true
    local agent_token="$PROMPT_RESULT"
    prompt_central_api
    local central_api="$PROMPT_RESULT"

    probe_heartbeat "$machine_id" "$agent_token" "$central_api"
    render_files "$machine_id" "$agent_token" "$central_api"
    deploy_node
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
