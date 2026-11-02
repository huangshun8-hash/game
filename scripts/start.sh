#!/usr/bin/env bash
# 启动项目各服务。
# 用法：
#   ./scripts/start.sh               # 后台启动全部服务（platform/gamelogic/gmt）
#   ./scripts/start.sh gamelogic     # 前台启动指定服务（日志直接输出，Ctrl+C 停止）
#   ./scripts/start.sh gmt platform  # 后台启动指定的多个服务
#   ./scripts/start.sh all           # 等价于无参数
#
# 依赖：.env.local（由 .env.local.example 复制而来），脚本会自动加载其中的环境变量。
# 前置：MongoDB 需以副本集模式运行（项目使用事务），Redis 已启动。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

# 服务名 -> go 包路径（相对项目根）
declare -A SERVICE_PATHS=(
  [platform]="./services/platform"
  [gamelogic]="./services/gamelogic"
  [gmt]="./services/gmt"
)

# 启动顺序：platform → gamelogic → gmt（下游依赖上游）
ORDER=(platform gamelogic gmt)

# 1. 加载 .env.local（含 JWT_SIGNING_KEY / INTERNAL_SECRET 等敏感项，不入库）
if [[ -f "${ROOT_DIR}/.env.local" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${ROOT_DIR}/.env.local"
  set +a
  echo "[INFO] 已加载环境变量: .env.local"
else
  echo "[WARN] .env.local 不存在，请先复制 .env.local.example 并填写 JWT_SIGNING_KEY / INTERNAL_SECRET" >&2
fi

# 2. 校验关键环境变量（与服务内 confx.ValidateSecret 对齐：至少 32 字节）
check_secret() {
  local name="$1" val
  val="${!name:-}"
  if [[ -z "${val}" ]]; then
    echo "[ERROR] ${name} 未设置，拒绝启动" >&2
    exit 1
  fi
  if [[ ${#val} -lt 32 ]]; then
    echo "[ERROR] ${name} 长度不足 32 字节（当前 ${#val}），拒绝启动" >&2
    exit 1
  fi
}
check_secret JWT_SIGNING_KEY
check_secret INTERNAL_SECRET

# 3. 解析目标服务
targets=()
if [[ $# -eq 0 ]] || [[ "$1" == "all" ]]; then
  targets=("${ORDER[@]}")
else
  for svc in "$@"; do
    if [[ -z "${SERVICE_PATHS[$svc]:-}" ]]; then
      echo "[ERROR] 未知服务: ${svc}（可选：platform/gamelogic/gmt/all）" >&2
      exit 1
    fi
    targets+=("${svc}")
  done
fi

# 4. 单个服务前台启动，其余场景后台启动
mkdir -p "${ROOT_DIR}/logs"

if [[ ${#targets[@]} -eq 1 ]]; then
  svc="${targets[0]}"
  echo "[INFO] 前台启动 ${svc}: go run ${SERVICE_PATHS[$svc]}"
  exec go run "${SERVICE_PATHS[$svc]}"
fi

pids=()
for svc in "${targets[@]}"; do
  log="${ROOT_DIR}/logs/${svc}.log"
  echo "[INFO] 后台启动 ${svc}，日志: ${log}"
  go run "${SERVICE_PATHS[$svc]}" >"${log}" 2>&1 &
  pids+=($!)
done

echo "[INFO] 已后台启动 ${#pids[@]} 个服务（PID: ${pids[*]}）"
echo "[INFO] 停止服务：kill ${pids[*]}；查看日志：tail -f logs/*.log"
