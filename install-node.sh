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
REPO_URL_EXPLICIT=false
REPO_REF_EXPLICIT=false
[[ -n "${AGENDA_NODE_REPO_URL+x}" ]] && REPO_URL_EXPLICIT=true
[[ -n "${AGENDA_NODE_REPO_REF+x}" ]] && REPO_REF_EXPLICIT=true
REPO_URL="${AGENDA_NODE_REPO_URL:-$DEFAULT_REPO_URL}"
REPO_REF="${AGENDA_NODE_REPO_REF:-$DEFAULT_REPO_REF}"
CONFIG_DIR="$INSTALL_DIR/config"
CONFIG_FILE="$CONFIG_DIR/agenda-node.yaml"
COMPOSE_ENV_FILE="$INSTALL_DIR/compose.env"
REPO_URL_FILE="$INSTALL_DIR/repo-url"
REPO_REF_FILE="$INSTALL_DIR/repo-ref"
SOURCE_DIR=""
SOURCE_MANAGED=false
SOURCE_WAS_CLONED=false
RECONFIGURE=false
PROMPT_RESULT=""

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

require_bin() {
    command -v "$1" >/dev/null 2>&1 || die "缺少命令 $1：$2"
}

check_prereqs() {
    [[ "$EUID" -eq 0 ]] || die "请使用 root 运行：sudo bash install-node.sh"
    require_bin docker "请先安装 Docker Engine"
    docker compose version >/dev/null 2>&1 || die "未找到 Docker Compose v2 插件"
    docker info >/dev/null 2>&1 || die "Docker daemon 不可用，请先启动 Docker"
    require_bin curl "用于验证控制面 heartbeat 接口"
}

prompt_required() {
    local label="$1" secret="${2:-false}" value=""
    [[ -r /dev/tty && -w /dev/tty ]] || die "首次安装或 --reconfigure 需要交互式终端 (/dev/tty)"
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

usage() {
    cat <<'EOF'
Usage: sudo bash install-node.sh [--reconfigure]

Without options:
  - first install: ask for Machine ID, Agent token, and Central API
  - repeated run: reuse the existing config and redeploy without prompts

Options:
  --reconfigure  Ask for all values again and replace the existing config
  -h, --help     Show this help

Environment overrides:
  AGENDA_NODE_REPO_REF        Git branch/tag to install (default: master)
  AGENDA_NODE_REPO_URL        Source repository URL
  AGENDA_NODE_SOURCE_DIR      Build an existing checkout; never auto-update it
  AGENDA_NODE_INSTALL_DIR     Persistent installer directory
  AGENDA_NODE_WORKSPACE_ROOT  Deployment workspace path
EOF
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --reconfigure) RECONFIGURE=true ;;
            -h|--help) usage; exit 0 ;;
            *) die "未知参数：$1（使用 --help 查看帮助）" ;;
        esac
        shift
    done
}

load_source_settings() {
    if [[ "$REPO_URL_EXPLICIT" == "false" && -s "$REPO_URL_FILE" ]]; then
        IFS= read -r REPO_URL <"$REPO_URL_FILE" || die "无法读取 $REPO_URL_FILE"
    fi
    if [[ "$REPO_REF_EXPLICIT" == "false" && -s "$REPO_REF_FILE" ]]; then
        IFS= read -r REPO_REF <"$REPO_REF_FILE" || die "无法读取 $REPO_REF_FILE"
    fi
    [[ "$REPO_URL" != *[[:space:]]* && -n "$REPO_URL" ]] || die "源码仓库 URL 无效"
    [[ "$REPO_REF" != *[[:space:]]* && -n "$REPO_REF" ]] || die "源码分支/tag 无效"
}

persist_source_settings() {
    [[ "$SOURCE_MANAGED" == "true" ]] || return
    printf '%s\n' "$REPO_URL" >"$REPO_URL_FILE"
    printf '%s\n' "$REPO_REF" >"$REPO_REF_FILE"
    chmod 600 "$REPO_URL_FILE" "$REPO_REF_FILE"
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
        SOURCE_MANAGED=true
        if [[ -d "$SOURCE_DIR/.git" ]]; then
            log "复用已有源码：$SOURCE_DIR"
        elif [[ -e "$SOURCE_DIR" ]]; then
            die "$SOURCE_DIR 已存在但不是 Git 仓库，请移走后重试"
        else
            log "下载 agenda-v2 源码到 $SOURCE_DIR"
            git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" "$SOURCE_DIR"
            SOURCE_WAS_CLONED=true
        fi
    fi

}

update_managed_source() {
    [[ "$SOURCE_MANAGED" == "true" ]] || {
        log "使用现有源码 checkout，不自动修改：$SOURCE_DIR"
        return
    }
    [[ "$SOURCE_WAS_CLONED" == "false" ]] || return

    if [[ -n "$(git -C "$SOURCE_DIR" status --porcelain)" ]]; then
        die "托管源码存在本地改动，已停止自动更新：$SOURCE_DIR"
    fi

    local current_repo_url
    current_repo_url="$(git -C "$SOURCE_DIR" remote get-url origin)"
    if [[ "$current_repo_url" != "$REPO_URL" ]]; then
        log "更新托管源码仓库地址"
        git -C "$SOURCE_DIR" remote set-url origin "$REPO_URL"
    fi

    log "更新托管源码到 $REPO_REF"
    git -C "$SOURCE_DIR" fetch --depth 1 origin "$REPO_REF"
    git -C "$SOURCE_DIR" checkout --detach FETCH_HEAD
}

validate_source_layout() {
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

prepare_runtime_paths() {
    mkdir -p "$CONFIG_DIR" "$WORKSPACE_ROOT"
    chmod 700 "$CONFIG_DIR"

    cat >"$COMPOSE_ENV_FILE" <<EOF
AGENDA_NODE_CONFIG_DIR=$CONFIG_DIR
AGENDA_NODE_WORKSPACE_ROOT=$WORKSPACE_ROOT
EOF
    chmod 600 "$COMPOSE_ENV_FILE"
}

validate_existing_config() {
    local -a invalid_fields=()
    grep -Eq '^machine_id:[[:space:]]*[1-9][0-9]*[[:space:]]*$' "$CONFIG_FILE" || invalid_fields+=("machine_id")
    grep -Eq '^token:[[:space:]]*".+"[[:space:]]*$' "$CONFIG_FILE" || invalid_fields+=("token")
    grep -Eq '^central_base_url:[[:space:]]*"https?://.+"[[:space:]]*$' "$CONFIG_FILE" || invalid_fields+=("central_base_url")

    if [[ ${#invalid_fields[@]} -gt 0 ]]; then
        die "现有配置缺少或包含无效字段：${invalid_fields[*]}。请使用 --reconfigure 修复"
    fi
    chmod 600 "$CONFIG_FILE"
    log "复用已有配置：$CONFIG_FILE"
}

render_files() {
    local machine_id="$1" agent_token="$2" central_api="$3"
    local token_yaml central_yaml
    token_yaml="$(yaml_quote "$agent_token")"
    central_yaml="$(yaml_quote "$central_api")"

    log "写入 agenda-node 配置"
    prepare_runtime_paths

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
}

configure_node() {
    if [[ -f "$CONFIG_FILE" && "$RECONFIGURE" == "false" ]]; then
        prepare_runtime_paths
        validate_existing_config
        return
    fi

    if [[ "$RECONFIGURE" == "true" && -f "$CONFIG_FILE" ]]; then
        log "--reconfigure 已启用，将重新填写并替换现有配置"
    else
        log "未找到现有配置，进入首次安装"
    fi

    prompt_machine_id
    local machine_id="$PROMPT_RESULT"
    prompt_required "Agent token" true
    local agent_token="$PROMPT_RESULT"
    prompt_central_api
    local central_api="$PROMPT_RESULT"

    probe_heartbeat "$machine_id" "$agent_token" "$central_api"
    render_files "$machine_id" "$agent_token" "$central_api"
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
            printf '  重新配置：sudo bash install-node.sh --reconfigure\n'
            printf '\n请回到 Machines 页面确认状态已变为 Online。\n'
            return
        fi
        sleep 2
    done

    "${compose[@]}" logs --no-color --tail=100 agenda-node >&2 || true
    die "agenda-node 未能在 80 秒内通过健康检查"
}

main() {
    parse_args "$@"
    check_prereqs
    mkdir -p "$INSTALL_DIR"
    load_source_settings
    resolve_source_dir
    update_managed_source
    validate_source_layout
    persist_source_settings
    configure_node
    deploy_node
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
