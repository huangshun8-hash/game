#!/usr/bin/env bash
# ============================================================
# 云主机环境预装脚本（Linux，首次部署前执行）
#
# 功能：
#   1. 校验 root 权限，识别发行版（apt / dnf / yum）
#   2. 安装/校验 gzip/gunzip（upload.sh 上传二进制时先在本地 gzip 压缩、由服务端 gunzip 解压）
#   3. 调用 scripts/install_docker.sh 安装/校验 Docker Engine 与 docker compose 插件（V2），
#      并按需配置 registry 镜像加速、预拉取运行时镜像（Docker 相关逻辑已独立成脚本，可单独执行）
#   4. 创建部署目录结构与日志目录（可选 --owner <用户> 将属主收归为非 root 运维用户）
#   5. 同步本目录内的 compose.yaml / deploy.sh / rollback.sh 到部署目录
#      （本目录 = install.sh 所在目录，即仓库 scripts/ 随 install.sh 一并上传的位置；仅本地文件复制，不依赖远程 git 仓库；
#        缺少文件仅告警，不中断后续 docker/镜像/目录初始化，收尾会提示补齐后再发布）
#
# 用法：
#   sudo ./scripts/install.sh
#   sudo ./scripts/install.sh --mirror aliyun            # 阿里云 Docker 源（国内云主机推荐）
#   sudo ./scripts/install.sh --registry-mirror https://docker.m.daocloud.io
#   sudo ./scripts/install.sh --skip-docker              # 已装 Docker，仅校验 + 建目录 + 同步
#   sudo ./scripts/install.sh --skip-pull                # 跳过运行时镜像预拉取
#   sudo ./scripts/install.sh --only-dirs                # 仅创建目录结构，不安装不同步
#   sudo ./scripts/install.sh --owner ubuntu             # 目录/日志属主收归为 ubuntu（非 root 运维用户）
#   sudo ./scripts/install.sh -h | --help
#
# Docker 相关参数（--mirror/--registry-mirror/--skip-docker/--skip-pull）会原样透传给
# scripts/install_docker.sh，Docker 环境也可单独预装：sudo ./scripts/install_docker.sh --mirror aliyun
#
# 环境变量：
#   SERVER_DIR : 部署根目录（默认 /data/servers）
#   LOGS_DIR   : 日志目录（默认 /data/logs）
#
# 首次部署前需上传到目标服务器的文件：
#   A. scripts 五件套（必须同目录上传，install.sh 运行时依赖兄弟文件）：
#      scripts/install.sh  scripts/install_docker.sh  scripts/compose.yaml  scripts/deploy.sh  scripts/rollback.sh
#   B. 生产配置（上传/配置到 ${SERVER_DIR}/config/，四份均 chmod 600）：
#      platform_prod.yaml  gamelogic_prod.yaml  gmt_prod.yaml  server.env
#   C. 服务二进制（构建产物 dist/ 下的 platform / gamelogic / gmt，传到服务器临时目录 /tmp/）
#   D. GMT 前端产物（可选，解压到 /data/web，须含 index.html，供 gmt 容器只读挂载）
# 然后执行首次部署：./scripts/deploy.sh <svc> <version> --binary /tmp/<svc>
# ============================================================

set -euo pipefail

# ---- 默认值 ----
SERVER_DIR="${SERVER_DIR:-/data/servers}"
LOGS_DIR="${LOGS_DIR:-/data/logs}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 以下 docker 相关参数仅做解析与透传，实际安装逻辑见 scripts/install_docker.sh
# Docker 安装源：official | aliyun
INSTALL_MIRROR="official"
# Docker registry 镜像加速地址（可选）
REGISTRY_MIRROR=""
SKIP_DOCKER=0
# 是否预拉取运行时镜像（逻辑上为 0=默认预拉取，1=跳过）
SKIP_PULL=0
# 非 root 运维：部署目录/日志目录的属主用户（为空=保持 root 属主）
OWNER=""
# 仅创建目录结构，跳过安装与同步
ONLY_DIRS=0

usage() {
  sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

# ---- 参数解析 ----
while [ $# -gt 0 ]; do
  case "$1" in
    --mirror)
      [ $# -ge 2 ] || { echo "[ERROR] --mirror 需要参数 official|aliyun" >&2; exit 1; }
      INSTALL_MIRROR="$2"
      shift 2
      ;;
    --registry-mirror)
      [ $# -ge 2 ] || { echo "[ERROR] --registry-mirror 需要参数（镜像加速 URL）" >&2; exit 1; }
      REGISTRY_MIRROR="$2"
      shift 2
      ;;
    --skip-docker)
      SKIP_DOCKER=1
      shift
      ;;
    --skip-pull)
      SKIP_PULL=1
      shift
      ;;
    --only-dirs)
      ONLY_DIRS=1
      shift
      ;;
    --owner)
      [ $# -ge 2 ] || { echo "[ERROR] --owner 需要参数（用户名）" >&2; exit 1; }
      OWNER="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "[ERROR] 未知参数: $1（使用 --help 查看用法）" >&2
      exit 1
      ;;
  esac
done

case "${INSTALL_MIRROR}" in
  official|aliyun) ;;
  *) echo "[ERROR] --mirror 仅支持 official|aliyun" >&2; exit 1 ;;
esac

# ---- 前置校验：root 权限 ----
if [ "$(id -u)" -ne 0 ]; then
  echo "[ERROR] 请使用 root 执行：sudo ./scripts/install.sh" >&2
  exit 1
fi

# ---- 发行版识别：apt / dnf / yum ----
detect_os_family() {
  if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-} ${ID_LIKE:-}" in
      *debian*|*ubuntu*) echo "apt" ;;
      *rhel*|*centos*|*fedora*|*rocky*|*almalinux*|*alinux*|*anolis*) echo "dnf" ;;
      *) echo "unknown" ;;
    esac
  else
    echo "unknown"
  fi
}
OS_FAMILY="$(detect_os_family)"
OS_ID="$(. /etc/os-release 2>/dev/null && echo "${ID:-unknown}" || echo unknown)"
echo "[INFO] 系统: ${OS_ID}（包管理器: ${OS_FAMILY}）"

# ---- 工具：gzip / gunzip ----
# upload.sh 上传二进制时先在本地 gzip 压缩、由服务器端 gunzip 解压，故需保证 gunzip 可用。
# Debian/Ubuntu 与 RHEL 系均把 gunzip 放在 gzip 包内，安装 gzip 包即可同时获得 gzip/gunzip。
ensure_gunzip() {
  if command -v gunzip >/dev/null 2>&1; then
    echo "[OK] gunzip 已存在: $(gunzip --version 2>/dev/null | head -1 || echo version-unreadable)"
    return 0
  fi
  echo "[INFO] 未检测到 gunzip，安装 gzip（提供 gunzip）..."
  case "${OS_FAMILY}" in
    apt) DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y gzip ;;
    dnf)
      if command -v dnf >/dev/null 2>&1; then
        dnf -y install gzip
      else
        yum -y install gzip
      fi
      ;;
    *) echo "[WARN] 无法识别的发行版，请手动安装 gzip/gunzip" >&2 ;;
  esac
  # 安装失败不中断环境初始化：upload.sh 检测不到 gunzip 时会自动回退为不压缩上传
  if command -v gunzip >/dev/null 2>&1; then
    echo "[OK] gunzip 安装完成"
  else
    echo "[WARN] gunzip 仍不可用，upload.sh 上传时将自动回退为不压缩传输" >&2
  fi
}

# ---- Docker 安装/校验：委托 scripts/install_docker.sh（docker 相关逻辑独立成脚本，便于单独复用）----
ensure_docker() {
  local installer="${SCRIPT_DIR}/install_docker.sh"
  if [ ! -f "${installer}" ]; then
    echo "[ERROR] 缺少 ${installer}，请将本地仓库 scripts/ 整体重传（install.sh 依赖同目录的 install_docker.sh）" >&2
    exit 1
  fi
  # 透传 docker 相关参数；并复用外层已探测的发行版信息，避免子脚本重复解析 /etc/os-release
  local args=()
  if [ "${INSTALL_MIRROR}" != "official" ]; then args+=(--mirror "${INSTALL_MIRROR}"); fi
  if [ -n "${REGISTRY_MIRROR}" ]; then args+=(--registry-mirror "${REGISTRY_MIRROR}"); fi
  if [ "${SKIP_DOCKER}" -eq 1 ]; then args+=(--skip-docker); fi
  if [ "${SKIP_PULL}" -eq 1 ]; then args+=(--skip-pull); fi
  OS_FAMILY="${OS_FAMILY}" OS_ID="${OS_ID}" bash "${installer}" ${args[@]+"${args[@]}"}
}

# ---- 目录结构创建 ----
ensure_dirs() {
  echo "[INFO] 创建部署目录结构与日志目录..."
  mkdir -p "${SERVER_DIR}/current" \
    "${SERVER_DIR}/releases" \
    "${SERVER_DIR}/config" \
    "${SERVER_DIR}/scripts" \
    "${LOGS_DIR}/platform" \
    "${LOGS_DIR}/gamelogic" \
    "${LOGS_DIR}/gmt"
  chmod 0755 "${SERVER_DIR}" "${LOGS_DIR}"
  # 非 root 运维模型：把属主收归为 --owner 指定用户（chown -R 同时对已存在的旧部署树一次性收权）
  if [ -n "${OWNER}" ]; then
    if ! id "${OWNER}" >/dev/null 2>&1; then
      echo "[ERROR] 用户不存在: ${OWNER}（请确认用户名，或先创建该用户）" >&2
      return 1
    fi
    chown -R "${OWNER}":"${OWNER}" "${SERVER_DIR}" "${LOGS_DIR}"
    echo "[OK] 目录属主已设为: ${OWNER}"
  fi
  echo "[OK] 部署根目录: ${SERVER_DIR}"
  echo "[OK] 日志目录:   ${LOGS_DIR}"
}

# ---- 同步本目录（install.sh 所在目录，随其一并上传的本地仓库 scripts/ 文件）的 compose.yaml / deploy.sh / rollback.sh 到部署目录 ----
sync_files() {
  local src dst
  # compose.yaml → ${SERVER_DIR}/compose.yaml
  src="${SCRIPT_DIR}/compose.yaml"
  dst="${SERVER_DIR}/compose.yaml"
  if [ ! -f "${src}" ]; then
    echo "[ERROR] 缺少 ${src}，请将本地仓库 scripts/ 整体重传（compose.yaml/deploy.sh/rollback.sh 须与 install.sh 同目录上传）" >&2
    return 1
  fi
  if [ -f "${dst}" ]; then
    echo "[SKIP] ${dst} 已存在，未覆盖（如需更新请手动 diff 后替换）"
  else
    cp -a "${src}" "${dst}" || return 1
    echo "[OK] 已生成 ${dst}"
  fi
  # deploy.sh / rollback.sh → ${SERVER_DIR}/scripts/
  for f in deploy.sh rollback.sh; do
    src="${SCRIPT_DIR}/${f}"
    dst="${SERVER_DIR}/scripts/${f}"
    if [ ! -f "${src}" ]; then
      echo "[ERROR] 缺少 ${src}，请将本地仓库 scripts/ 整体重传（compose.yaml/deploy.sh/rollback.sh 须与 install.sh 同目录上传）" >&2
      return 1
    fi
    if [ -f "${dst}" ]; then
      echo "[SKIP] ${dst} 已存在，未覆盖"
    else
      cp -a "${src}" "${dst}" || return 1
      chmod +x "${dst}" || return 1
      echo "[OK] 已生成 ${dst}"
    fi
  done
}

# ============================================================
# 主流程
# ============================================================
echo "===== 云主机环境预装开始 ====="
if [ "${ONLY_DIRS}" -eq 1 ]; then
  echo "[INFO] --only-dirs：仅创建目录，跳过安装与同步"
  ensure_dirs
  echo "===== 完成（仅目录）====="
  exit 0
fi

# gunzip 供 upload.sh 在服务端解压上传的二进制，属于本地工具依赖，与 docker 无关，故在此单独保证
ensure_gunzip
# docker + docker compose 插件安装/校验（含镜像加速与运行时镜像预拉取）统一由 install_docker.sh 负责
ensure_docker

ensure_dirs
# 同步部署文件失败仅告警，不允许中断环境初始化（docker/镜像/目录照常完成）
if ! sync_files; then
  echo "[WARN] 同步部署文件失败，已跳过；环境初始化继续。" >&2
  echo "[WARN] 注意：补齐 ${SERVER_DIR}/compose.yaml 与 ${SERVER_DIR}/scripts/deploy.sh、rollback.sh 后，才可执行" >&2
  echo "[WARN]      docker compose up -d 与发布/回滚；重跑 install.sh 可自动补同步（已存在文件不会被覆盖）。" >&2
fi

# 端口暴露提示：三个服务在 compose.yaml 中默认仅绑 127.0.0.1（外部不可直达）。
# 若不打算在本机部署额外反代/网关而想用云 SLB/ALB 直连后端，必须先放开绑定地址。
echo "[WARN] 注意：compose.yaml 中三个服务默认仅绑定 127.0.0.1，外部网络（含云 SLB/ALB）无法直连。" >&2
echo "[WARN] 如需用云 SLB/ALB 直连后端，请编辑 ${SERVER_DIR}/compose.yaml：" >&2
echo "[WARN]   将对应服务的端口映射由 \"127.0.0.1:<port>:<port>\" 改为 \"0.0.0.0:<port>:<port>\"（或内网网卡 IP），" >&2
echo "[WARN]   改完执行 docker compose up -d <svc> 重建容器生效（restart 不会重读端口映射）。" >&2
echo "[WARN] 若在本机前置反代/网关，请自行安装配置后代理到 127.0.0.1 对应端口（无需改 compose.yaml）。" >&2

echo ""
echo "===== 环境预装完成 ====="
echo "下一步："
echo "  1. 将生产配置放入 ${SERVER_DIR}/config/"
echo "     - <svc>_prod.yaml（可参考仓库 configs/ 下的 *_local.yaml 改造）"
echo "     - server.env（含 JWT_SIGNING_KEY / INTERNAL_SECRET / MONGO_URI / REDIS_ADDR 等）"
echo "     chmod 600 ${SERVER_DIR}/config/server.env"
if [ -n "${OWNER}" ]; then
  echo "     （--owner 已生效：非 root 运维模型下，配置放入后请以 ${OWNER} 属主保持 600，并确认 ${OWNER} 已加入 docker 组，详见 docs/deploy.md 5.5）"
fi
echo "  2. 发布二进制并启动："
echo "     cd ${SERVER_DIR} && docker compose up -d"
echo "  3. 后续版本更新使用："
echo "     ${SERVER_DIR}/scripts/deploy.sh <svc> <version> --binary /tmp/<svc>"
echo "     回滚：${SERVER_DIR}/scripts/rollback.sh <svc>"
echo "  4. 对外访问：确认端口暴露方式（见上方 SLB/ALB 或本机反代说明），并在安全组放行对应入口端口"
