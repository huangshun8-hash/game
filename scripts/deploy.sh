#!/usr/bin/env bash
# ============================================================
# 服务发布脚本（Linux 服务器执行）
#
# 核心思路：releases/<svc>/<version> 保存版本二进制，
# current/<svc> 软链接指向当前生效版本，容器通过只读挂载读取；
# 发布 = 原子切换软链接 + docker compose restart + 健康/版本校验，
# 失败自动回滚上一个版本。
#
# 用法：
#   ./scripts/deploy.sh <service> <version> [--binary <path>] [--no-version-check]
#   示例：./scripts/deploy.sh gamelogic 20260821_002 --binary /tmp/gamelogic
#
#   service  : platform | gamelogic | gmt
#   version  : 版本号，格式 YYYYMMDD_序号（如 20260821_002）
#   --binary : 可选。新二进制的本地上传路径，脚本会自动放入版本目录并加执行权限
#   --no-version-check : 可选。跳过 /version 版本核对
#     （旧版本二进制没有 /version 接口时脚本会自动跳过，无需加此参数）
#
# 环境变量：
#   GAME_SERVER_DIR : 部署根目录（默认 /data/servers）
#   HEALTH_URL_BASE : 健康检查地址前缀（默认 http://127.0.0.1:<服务默认端口>）
#   HEALTH_TRIES    : 健康检查尝试次数（默认 30）
#   HEALTH_DELAY    : 健康检查间隔秒数（默认 2）
# ============================================================

set -euo pipefail

# ---- 发布目录 ----
BASE_DIR="${GAME_SERVER_DIR:-/data/servers}"
COMPOSE_FILE="${BASE_DIR}/compose.yaml"
DEPLOY_LOG="${BASE_DIR}/deploy.log"

# 服务默认端口（健康检查用；若在 YAML 中改过 server.port，请用 HEALTH_URL_BASE 覆盖）
declare -A DEFAULT_PORTS=(
  [platform]=8080
  [gamelogic]=8081
  [gmt]=8082
)
# 版本号格式：YYYYMMDD_序号（至少 3 位序号）
VERSION_REGEX='^[0-9]{8}_[0-9]{3,}$'

# ---- 解析命令行参数 ----
if [[ $# -lt 2 ]]; then
  echo "用法: $0 <service> <version> [--binary <path>] [--no-version-check]" >&2
  echo "" >&2
  echo "示例: $0 gamelogic 20260821_002 --binary /tmp/gamelogic" >&2
  exit 1
fi
SVC="$1"
VERSION="$2"
shift 2

BINARY_SRC=""
CHECK_VERSION_FLAG="1"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary)
      BINARY_SRC="${2:-}"
      shift 2
      ;;
    --no-version-check)
      CHECK_VERSION_FLAG="0"
      shift
      ;;
    *)
      echo "[ERROR] 未知参数: $1" >&2
      exit 1
      ;;
  esac
done

# ---- 基础校验 ----
if [[ -z "${DEFAULT_PORTS[${SVC}]:-}" ]]; then
  echo "[ERROR] 未知服务: ${SVC}（可选：platform/gamelogic/gmt）" >&2
  exit 1
fi
if [[ ! "${VERSION}" =~ ${VERSION_REGEX} ]]; then
  echo "[ERROR] 非法版本号: ${VERSION}（要求格式 ${VERSION_REGEX}）" >&2
  exit 1
fi
if [[ ! -f "${COMPOSE_FILE}" ]]; then
  echo "[ERROR] compose 文件不存在: ${COMPOSE_FILE}（请先按 docs/deploy.md 完成首次部署初始化）" >&2
  exit 1
fi

# ---- 目录与二进制 ----
release_dir="${BASE_DIR}/releases/${SVC}/${VERSION}"
binary_path="${release_dir}/${SVC}"
current_link="${BASE_DIR}/current/${SVC}"

# ---- 可选：投放新二进制到版本目录 ----
if [[ -n "${BINARY_SRC}" ]]; then
  if [[ ! -f "${BINARY_SRC}" ]]; then
    echo "[ERROR] 二进制不存在: ${BINARY_SRC}" >&2
    exit 1
  fi
  mkdir -p "${release_dir}"
  cp -f "${BINARY_SRC}" "${binary_path}"
  chmod +x "${binary_path}"
  echo "[INFO] 已投放二进制: ${BINARY_SRC} -> ${binary_path}"
fi

# ---- 校验版本目录与二进制 ----
if [[ ! -d "${release_dir}" ]]; then
  echo "[ERROR] 版本目录不存在: ${release_dir}" >&2
  echo "[提示] 请先上传二进制并用 --binary 投放，或手动创建版本目录" >&2
  exit 1
fi
if [[ ! -x "${binary_path}" ]]; then
  echo "[ERROR] 二进制不可执行: ${binary_path}" >&2
  exit 1
fi

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

# ---- 版本核对（新二进制带 /version 时生效，旧二进制自动跳过）----
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

# ---- 写发布日志（deploy.log）----
log_deploy() {
  local result="$1"
  local now
  now="$(date -u '+%Y-%m-%d %H:%M:%S %z')"
  printf '%s [deploy] service=%s old=%s new=%s result=%s\n' \
    "${now}" "${SVC}" "${OLD_VER:-none}" "${VERSION}" "${result}" >> "${DEPLOY_LOG}"
}

# ---- 回滚到旧版本 ----
rollback() {
  if [[ -z "${OLD_VER:-}" ]]; then
    echo "[ERROR] 无旧版本可回滚（首次发布），请人工排查新版本问题" >&2
    return 1
  fi
  if [[ "${OLD_VER}" == "${VERSION}" ]]; then
    return 1
  fi
  echo "[INFO] 开始回滚: ${SVC} ${VERSION} -> ${OLD_VER}"
  ln -sfn "../releases/${SVC}/${OLD_VER}" "${current_link}"
  restart_service
  if wait_healthy "$(health_url)"; then
    echo "[INFO] 回滚成功，当前版本: ${OLD_VER}"
    return 0
  else
    echo "[ERROR] 回滚后的服务也不健康，请人工介入" >&2
    return 1
  fi
}

# ---- 主流程 ----
# 记录旧版本（首次部署时 current 软链接不存在，OLD_VER 为空）
if [[ -L "${current_link}" ]]; then
  OLD_VER="$(basename "$(readlink -f "${current_link}")")"
else
  OLD_VER=""
fi
echo "[INFO] 服务: ${SVC}"
echo "[INFO] 旧版本: ${OLD_VER:-（尚未初始化）}"
echo "[INFO] 新版本: ${VERSION}"

# 原子切换软链接（ln -sfn 为原子操作，瞬间完成）
mkdir -p "${BASE_DIR}/current"
ln -sfn "../releases/${SVC}/${VERSION}" "${current_link}"
echo "[INFO] 软链接已切换: ${current_link} -> $(basename "$(readlink -f "${current_link}")")"

# 重启容器（重新解析 bind mount 源路径的软链接，加载新版本二进制）
restart_service

# 健康检查 + 版本核对，任一失败则自动回滚
if wait_healthy "$(health_url)"; then
  version_ok="1"
  if [[ "${CHECK_VERSION_FLAG}" == "1" ]]; then
    if ! check_version "$(version_url)" "${VERSION}"; then
      version_ok="0"
    fi
  fi
  if [[ "${version_ok}" == "1" ]]; then
    log_deploy "success"
    echo "[OK] 发布成功，当前版本 ${VERSION}"
    exit 0
  fi
fi

# ---- 失败：自动回滚 ----
echo "[ERROR] 新版本启动/校验失败，执行自动回滚" >&2
if rollback; then
  log_deploy "rollback"
  echo "[OK] 已自动回滚到 ${OLD_VER}，请排查新版本问题后再发布"
else
  log_deploy "rollback_failed"
  echo "[ERROR] 自动回滚失败，请立即人工介入" >&2
fi
exit 1