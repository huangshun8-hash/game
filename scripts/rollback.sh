#!/usr/bin/env bash
# ============================================================
# 版本回滚脚本（Linux 服务器执行）
#
# 用法：
#   ./scripts/rollback.sh                        # 查看全部服务的当前版本与可用版本
#   ./scripts/rollback.sh --list                 # 同上
#   ./scripts/rollback.sh <service>              # 回滚到上一可用版本（releases 中最新一个）
#   ./scripts/rollback.sh <service> <version>    # 回滚指定服务到指定版本
#
# 行为：
#   1. 校验 service/version，version 省略时自动选择 releases 中除当前外的最新版本
#   2. 原子切换软链接 current/<service>
#   3. docker compose restart <service>
#   4. 轮询 /health 并核对 /version（旧二进制无该接口时自动跳过）
#   5. 回滚目标不健康时，自动恢复回切换前的版本并告警
#
# 环境变量：
#   GAME_SERVER_DIR : 部署根目录（默认 /data/servers）
#   HEALTH_URL_BASE : 健康检查地址前缀（默认 http://127.0.0.1:<服务默认端口>）
#   HEALTH_TRIES    : 健康检查尝试次数（默认 30）
#   HEALTH_DELAY    : 健康检查间隔秒数（默认 2）
# ============================================================

set -euo pipefail

# ---- 部署目录 ----
BASE_DIR="${GAME_SERVER_DIR:-/data/servers}"
COMPOSE_FILE="${BASE_DIR}/compose.yaml"

# 服务默认端口（健康检查用；若在 YAML 中改过 server.port，请用 HEALTH_URL_BASE 覆盖）
declare -A DEFAULT_PORTS=(
  [platform]=8080
  [gamelogic]=8081
  [gmt]=8082
)
VERSION_REGEX='^[0-9]{8}_[0-9]{3,}$'

# ---- 健康检查（轮询直到就绪或超时）----
health_url() {
  echo "${HEALTH_URL_BASE:-http://127.0.0.1:${DEFAULT_PORTS[${SVC}]}}/health"
}
version_url() {
  echo "${HEALTH_URL_BASE:-http://127.0.0.1:${DEFAULT_PORTS[${SVC}]}}/version"
}

wait_healthy() {
  local url="$1"
  local tries="${HEALTH_TRIES:-30}"
  local delay="${HEALTH_DELAY:-2}"
  local i
  for ((i = 1; i <= tries; i++)); do
    if curl -fsS --max-time 3 "${url}" >/dev/null 2>&1; then
      echo "[INFO] 健康检查通过: ${url}（第 ${i} 次尝试）"
      return 0
    fi
    sleep "${delay}"
  done
  echo "[ERROR] 健康检查超时: ${url}（已尝试 ${tries} 次）" >&2
  return 1
}

# ---- 版本核对（旧二进制无 /version 时自动跳过）----
check_version() {
  local url="$1"
  local expect="$2"
  local body ver
  body="$(curl -fsS --max-time 3 "${url}" 2>/dev/null || true)"
  if [[ -z "${body}" ]]; then
    echo "[WARN] ${url} 未响应（旧版本二进制可能无该接口），跳过版本核对"
    return 0
  fi
  ver="$(printf '%s' "${body}" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  if [[ -z "${ver}" ]]; then
    echo "[WARN] ${url} 未能解析版本号，跳过版本核对"
    return 0
  fi
  if [[ "${ver}" != "${expect}" ]]; then
    echo "[ERROR] 运行版本 ${ver} ≠ 目标版本 ${expect}" >&2
    return 1
  fi
  echo "[INFO] 版本核对通过: ${ver}"
  return 0
}

# ---- 重启容器 ----
restart_service() {
  echo "[INFO] 重启容器服务: ${SVC}"
  docker compose --project-directory "${BASE_DIR}" -f "${COMPOSE_FILE}" restart "${SVC}"
}

# ---- 查看各服务版本状态 ----
list_versions() {
  local all_svcs=(platform gamelogic gmt)
  for svc in "${all_svcs[@]}"; do
    local cur
    cur="（尚未初始化）"
    if [[ -L "${BASE_DIR}/current/${svc}" ]]; then
      cur="$(basename "$(readlink -f "${BASE_DIR}/current/${svc}")")"
    fi
    echo "[${svc}] 当前版本: ${cur}"
    if [[ -d "${BASE_DIR}/releases/${svc}" ]]; then
      echo "  可用版本:"
      ls -1 "${BASE_DIR}/releases/${svc}" | sed 's/^/    /'
    else
      echo "  可用版本: （无）"
    fi
  done
}

# ---- 主流程 ----
if [[ $# -eq 0 ]] || [[ "$1" == "--list" ]]; then
  list_versions
  exit 0
fi

SVC="$1"
VERSION="${2:-}"

# 校验服务名
if [[ -z "${DEFAULT_PORTS[${SVC}]:-}" ]]; then
  echo "[ERROR] 未知服务: ${SVC}（可选：platform/gamelogic/gmt，或省略参数查看版本列表）" >&2
  exit 1
fi

# 当前软链接与目标版本
current_link="${BASE_DIR}/current/${SVC}"
PREV_VER=""
if [[ -L "${current_link}" ]]; then
  PREV_VER="$(basename "$(readlink -f "${current_link}")")"
fi

# 未指定版本时：自动选择 releases 中除当前外的最新一个
if [[ -z "${VERSION}" ]]; then
  VERSION="$(ls -1 "${BASE_DIR}/releases/${SVC}" 2>/dev/null | sort -r | grep -vFx "${PREV_VER:-__none__}" | head -n 1 || true)"
  if [[ -z "${VERSION}" ]]; then
    echo "[ERROR] 没有可用的回滚版本（releases/${SVC} 为空或只有当前版本）" >&2
    exit 1
  fi
  echo "[INFO] 未指定版本，自动选择: ${VERSION}"
fi

# 校验版本号格式与版本目录
if [[ ! "${VERSION}" =~ ${VERSION_REGEX} ]]; then
  echo "[ERROR] 非法版本号: ${VERSION}（要求格式 ${VERSION_REGEX}）" >&2
  exit 1
fi
binary_path="${BASE_DIR}/releases/${SVC}/${VERSION}/${SVC}"
if [[ ! -x "${binary_path}" ]]; then
  echo "[ERROR] 目标版本二进制不可执行: ${binary_path}" >&2
  exit 1
fi
if [[ -n "${PREV_VER}" ]] && [[ "${PREV_VER}" == "${VERSION}" ]]; then
  echo "[WARN] 目标版本与当前版本一致（均为 ${VERSION}），无需回滚"
  exit 0
fi

echo "[INFO] 回滚: ${SVC} ${PREV_VER:-（无）} -> ${VERSION}"

# 原子切换软链接
mkdir -p "${BASE_DIR}/current"
ln -sfn "../releases/${SVC}/${VERSION}" "${current_link}"
echo "[INFO] 软链接已切换: ${current_link} -> ${VERSION}"

restart_service

# 回滚目标健康检查与版本核对
rollback_ok="1"
if ! wait_healthy "$(health_url)"; then
  rollback_ok="0"
fi
if [[ "${rollback_ok}" == "1" ]] && ! check_version "$(version_url)" "${VERSION}"; then
  rollback_ok="0"
fi

if [[ "${rollback_ok}" == "1" ]]; then
  echo "[OK] 回滚成功，当前版本 ${VERSION}"
  exit 0
fi

# ---- 回滚目标异常：恢复切换前的版本 ----
if [[ -z "${PREV_VER}" ]]; then
  echo "[ERROR] 无切换前版本可恢复，请立即人工介入" >&2
  exit 1
fi
echo "[ERROR] 回滚目标服务异常，尝试恢复切换前的版本 ${PREV_VER}" >&2
ln -sfn "../releases/${SVC}/${PREV_VER}" "${current_link}"
restart_service
if wait_healthy "$(health_url)"; then
  echo "[OK] 已恢复切换前的版本 ${PREV_VER}"
  echo "[WARN] 回滚到 ${VERSION} 失败，原因请参考上方日志" >&2
  exit 1
fi
echo "[ERROR] 恢复切换前版本也不健康，请立即人工介入" >&2
exit 1