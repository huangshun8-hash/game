#!/usr/bin/env bash
# ============================================================
# 生产二进制构建脚本（开发机 / CI 执行）
#
# 功能：
#   - 交叉编译 linux/amd64 静态二进制（CGO_ENABLED=0）
#   - 通过 ldflags 注入版本号到 pkg/httpx.Version，服务 /version 接口可核对运行版本
#   - 输出 dist/ 目录并生成 sha256 版本清单（便于服务器端核对完整性）
#
# 用法：
#   ./scripts/build.sh                        # 编译全部服务，版本号自动生成（YYYYMMDD_HHMM）
#   ./scripts/build.sh gamelogic              # 只编译指定服务
#   ./scripts/build.sh -v 20260821_003        # 指定版本号
#   ./scripts/build.sh -v 20260821_003 gamelogic gmt   # 指定版本号 + 部分服务
#
# 环境变量：
#   OUT_DIR：输出目录（默认 ${ROOT_DIR}/dist）
# ============================================================

set -euo pipefail

# 项目根目录
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# 输出目录
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/dist}"

# 服务名 -> go 包路径
declare -A SERVICE_PATHS=(
  [platform]="./services/platform"
  [gamelogic]="./services/gamelogic"
  [gmt]="./services/gmt"
)

# ldflags 注入目标：pkg/httpx 包级变量，供 /version 接口返回
VERSION_SYMBOL="kemiluo/pkg/httpx.Version"
# 版本号格式：YYYYMMDD_序号（至少 3 位序号），与服务器 release 目录命名保持一致
VERSION_REGEX='^[0-9]{8}_[0-9]{3,}$'

# git commit 作为构建信息（可选）
GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

# ---- 解析命令行参数 ----
VERSION=""
targets=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -v | --version)
      VERSION="${2:-}"
      shift 2
      ;;
    -h | --help)
      sed -n '2,18p' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *)
      targets+=("$1")
      shift
      ;;
  esac
done

# 未指定版本号时自动生成：YYYYMMDD_HHMM（例如 20260821_1430）
if [[ -z "${VERSION}" ]]; then
  VERSION="$(date -u +%Y%m%d_%H%M)"
fi
# 校验版本号格式
if [[ ! "${VERSION}" =~ ${VERSION_REGEX} ]]; then
  echo "[ERROR] 非法版本号: ${VERSION}（要求格式 ${VERSION_REGEX}）" >&2
  exit 1
fi

# 未指定服务时默认编译全部
if [[ ${#targets[@]} -eq 0 ]]; then
  targets=(platform gamelogic gmt)
fi

mkdir -p "${OUT_DIR}"

# ---- 逐个交叉编译 ----
for svc in "${targets[@]}"; do
  pkg="${SERVICE_PATHS[${svc}]:-}"
  if [[ -z "${pkg}" ]]; then
    echo "[ERROR] 未知服务: ${svc}（可选：platform/gamelogic/gmt）" >&2
    exit 1
  fi
  output="${OUT_DIR}/${svc}"
  echo "[INFO] 编译 ${svc} (${pkg}) 版本=${VERSION} -> ${output}"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -ldflags "-s -w -X ${VERSION_SYMBOL}=${VERSION}" \
    -o "${output}" "${pkg}"
done

# ---- 计算文件 sha256（兼容 Linux sha256sum / macOS shasum）----
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "[ERROR] 未找到 sha256sum/shasum，请安装 coreutils" >&2
    return 1
  fi
}

# ---- 生成版本清单（sha256）----
manifest="${OUT_DIR}/${VERSION}_manifest.txt"
{
  echo "version: ${VERSION}"
  echo "commit: ${GIT_COMMIT}"
  echo "buildTime: $(date -u -Iseconds)"
  for svc in "${targets[@]}"; do
    echo "${svc}  $(sha256_of "${OUT_DIR}/${svc}")"
  done
} > "${manifest}"

echo "[INFO] 构建完成，共 ${#targets[@]} 个服务"
echo "[INFO] 输出目录: ${OUT_DIR}"
echo "[INFO] 版本清单: ${manifest}"
echo "[INFO] 上传示例: scp ${OUT_DIR}/* root@<服务器IP>:/tmp/"