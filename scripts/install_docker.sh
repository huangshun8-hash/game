#!/usr/bin/env bash
# ============================================================
# Docker 环境预装脚本（Linux）
#
# 说明：从 scripts/install.sh 抽离出的 Docker 相关安装逻辑，可独立执行，
#       也由 install.sh 以兄弟脚本方式调用（docker 相关参数原样透传）。
#
# 功能：
#   1. 校验 root 权限，识别发行版（apt / dnf / yum）
#   2. 安装/校验 curl（配置 Docker 官方源需要）
#   3. 安装/校验 Docker Engine 与 docker compose 插件（V2）
#      已安装则跳过；已装 Engine 但缺 compose 插件时自动补装（源支持 official / aliyun）
#   4. 可选配置 Docker registry 镜像加速（写入 /etc/docker/daemon.json，已存在则合并、不覆盖其他配置）
#   5. 预拉取运行时镜像 alpine:3.22 缓存到本地（--skip-pull 可跳过；失败仅警告不中断）
#
# 用法：
#   sudo ./scripts/install_docker.sh
#   sudo ./scripts/install_docker.sh --mirror aliyun            # 阿里云 Docker 源（国内云主机推荐）
#   sudo ./scripts/install_docker.sh --registry-mirror https://docker.m.daocloud.io
#   sudo ./scripts/install_docker.sh --skip-docker              # 已装 Docker，仅校验 docker 与 compose 插件
#   sudo ./scripts/install_docker.sh --skip-pull                # 跳过运行时镜像预拉取
#   sudo ./scripts/install_docker.sh -h | --help
#
# 环境变量：
#   OS_FAMILY : 包管理器类型（apt/dnf），调用方已探测时传入，缺省自行识别
#   OS_ID     : 发行版 ID（如 ubuntu/centos），调用方已探测时传入，缺省自行识别
# ============================================================

set -euo pipefail

# Docker 安装源：official | aliyun
INSTALL_MIRROR="aliyun"
# Docker registry 镜像加速地址（可选）
REGISTRY_MIRROR=""
# 是否仅校验 docker 环境（1=只校验，不安装、不配镜像加速以外的初始化）
SKIP_DOCKER=0
# 是否预拉取运行时镜像（0=默认预拉取，1=跳过）
SKIP_PULL=0

usage() {
  sed -n '2,27p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
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
  echo "[ERROR] 请使用 root 执行：sudo ./scripts/install_docker.sh" >&2
  exit 1
fi

# ---- 发行版识别：apt / dnf ----
# 调用方（install.sh）已探测时通过 OS_FAMILY/OS_ID 环境变量传入，避免重复解析 /etc/os-release
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
OS_FAMILY="${OS_FAMILY:-$(detect_os_family)}"
OS_ID="${OS_ID:-$(. /etc/os-release 2>/dev/null && echo "${ID:-unknown}" || echo unknown)}"
echo "[INFO] 系统: ${OS_ID}（包管理器: ${OS_FAMILY}）"

# ---- 工具：curl（配置 Docker 源、拉取 GPG key 需要）----
ensure_curl() {
  if command -v curl >/dev/null 2>&1; then
    echo "[OK] curl 已存在"
    return 0
  fi
  echo "[INFO] 安装 curl..."
  case "${OS_FAMILY}" in
    apt) DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y curl ;;
    dnf)
      if command -v dnf >/dev/null 2>&1; then
        dnf -y install curl
      else
        yum -y install curl
      fi
      ;;
    *) echo "[WARN] 无法识别的发行版，请手动安装 curl" >&2 ;;
  esac
}

# ---- Docker Engine 安装（apt）----
install_docker_apt() {
  local base gpg_url arch codename
  if [ "${INSTALL_MIRROR}" = "aliyun" ]; then
    base="https://mirrors.aliyun.com/docker-ce/linux/${OS_ID}"
    gpg_url="https://mirrors.aliyun.com/docker-ce/linux/${OS_ID}/gpg"
  else
    base="https://download.docker.com/linux/${OS_ID}"
    gpg_url="https://download.docker.com/linux/${OS_ID}/gpg"
  fi
  # shellcheck disable=SC1091
  . /etc/os-release
  codename="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}"
  [ -n "${codename}" ] || { echo "[ERROR] 无法识别系统代号（${OS_ID}）" >&2; return 1; }
  arch="$(dpkg --print-architecture)"
  install -m 0755 -d /etc/apt/keyrings
  DEBIAN_FRONTEND=noninteractive apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl gnupg
  curl -fsSL "${gpg_url}" | gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg
  echo "deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.gpg] ${base} ${codename} stable" \
    > /etc/apt/sources.list.d/docker.list
  DEBIAN_FRONTEND=noninteractive apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y \
    docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

# ---- Docker Engine 安装（dnf / yum）----
install_docker_dnf() {
  local repo_url
  if [ "${INSTALL_MIRROR}" = "aliyun" ]; then
    repo_url="https://mirrors.aliyun.com/docker-ce/linux/centos/docker-ce.repo"
  else
    repo_url="https://download.docker.com/linux/centos/docker-ce.repo"
  fi
  if command -v dnf >/dev/null 2>&1; then
    dnf -y install dnf-plugins-core
    dnf config-manager --add-repo "${repo_url}" || curl -fsSL -o /etc/yum.repos.d/docker-ce.repo "${repo_url}"
    dnf -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  else
    yum -y install yum-utils
    yum-config-manager --add-repo "${repo_url}" || curl -fsSL -o /etc/yum.repos.d/docker-ce.repo "${repo_url}"
    yum -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  fi
}

# ---- 补装 docker compose 插件（V2，与 Engine 同源安装）----
install_compose_plugin() {
  case "${OS_FAMILY}" in
    apt)
      DEBIAN_FRONTEND=noninteractive apt-get update
      DEBIAN_FRONTEND=noninteractive apt-get install -y docker-compose-plugin
      ;;
    dnf)
      if command -v dnf >/dev/null 2>&1; then
        dnf -y install docker-compose-plugin
      else
        yum -y install docker-compose-plugin
      fi
      ;;
    *)
      echo "[ERROR] 无法识别的发行版，请手动安装 docker-compose-plugin" >&2
      return 1
      ;;
  esac
}

# 等待 docker daemon 就绪
wait_docker() {
  echo "[INFO] 等待 docker daemon 就绪..."
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if docker info >/dev/null 2>&1; then
      echo "[OK] docker daemon 就绪"
      return 0
    fi
    sleep 1
  done
  echo "[ERROR] docker daemon 未就绪，请检查安装日志" >&2
  exit 1
}

# ---- Docker 安装/校验（含 compose 插件）----
ensure_docker() {
  if command -v docker >/dev/null 2>&1; then
    echo "[OK] docker 已存在: $(docker --version 2>/dev/null || echo version-unreadable)"
    if docker compose version >/dev/null 2>&1; then
      echo "[OK] docker compose 插件可用: $(docker compose version)"
      return 0
    fi
    echo "[WARN] docker 已安装但没有 compose 插件，尝试补装 docker-compose-plugin..."
    install_compose_plugin
    docker compose version >/dev/null 2>&1 || {
      echo "[ERROR] compose 插件补装失败，请手动安装 docker-compose-plugin 后重试" >&2
      exit 1
    }
    echo "[OK] docker compose 插件已补装: $(docker compose version)"
  else
    echo "[INFO] 未检测到 docker，开始安装（源: ${INSTALL_MIRROR}）..."
    case "${OS_FAMILY}" in
      apt) install_docker_apt ;;
      dnf) install_docker_dnf ;;
      *) echo "[ERROR] 无法识别的发行版，请参考官方文档手动安装 Docker" >&2; exit 1 ;;
    esac
  fi

  # 探测系统的服务管理器并启动 docker
  if command -v systemctl >/dev/null 2>&1; then
    systemctl enable docker >/dev/null 2>&1 || true
    systemctl start docker
  elif command -v service >/dev/null 2>&1; then
    service docker start
  fi
  wait_docker
}

# 重启 docker 守护进程（仅当其正在运行，且无 systemctl/service 时静默跳过）
restart_docker() {
  if [ -f /var/run/docker.pid ] || docker info >/dev/null 2>&1; then
    systemctl restart docker 2>/dev/null || service docker restart 2>/dev/null || true
  fi
}

# 将 JSON 片段合并进已有 daemon.json（优先 jq，其次 python3，防止覆盖已有配置）
merge_daemon_json() {
  local target="$1" fragment="$2" tmp
  tmp="${target}.tmp.$$"
  if command -v jq >/dev/null 2>&1; then
    jq --argjson extra "${fragment}" '. + $extra' "${target}" > "${tmp}" && mv "${tmp}" "${target}"
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "${target}" "${tmp}" "${fragment}" <<'PY'
import json, os, sys
target, tmp, fragment = sys.argv[1], sys.argv[2], sys.argv[3]
with open(target, "r", encoding="utf-8") as f:
    content = f.read()
try:
    cfg = json.loads(content)
except ValueError:
    # 目标文件为空或非合法 JSON 时按空配置合并，避免 json.load 直接崩溃
    cfg = {}
cfg.update(json.loads(fragment))
with open(tmp, "w", encoding="utf-8") as f:
    json.dump(cfg, f, indent=2, ensure_ascii=False)
    f.write("\n")
os.replace(tmp, target)
PY
  else
    return 1
  fi
}

# ---- 可选：registry 镜像加速 ----
setup_registry_mirror() {
  [ -n "${REGISTRY_MIRROR}" ] || return 0
  if ! command -v docker >/dev/null 2>&1; then
    echo "[WARN] docker 未安装，跳过 registry 镜像加速配置" >&2
    return 0
  fi
  local daemon_file="/etc/docker/daemon.json" fragment
  mkdir -p /etc/docker
  fragment="{\"registry-mirrors\":[\"${REGISTRY_MIRROR}\"]}"
  if [ ! -f "${daemon_file}" ]; then
    cat > "${daemon_file}" <<EOF
{
  "registry-mirrors": ["${REGISTRY_MIRROR}"]
}
EOF
    restart_docker
    echo "[OK] 已配置 registry 镜像加速: ${REGISTRY_MIRROR}"
    return 0
  fi
  if grep -q "registry-mirrors" "${daemon_file}"; then
    echo "[SKIP] ${daemon_file} 已含 registry-mirrors，如需修改请手动编辑"
    restart_docker
    return 0
  fi
  cp -a "${daemon_file}" "${daemon_file}.bak.$(date +%s)"
  if merge_daemon_json "${daemon_file}" "${fragment}"; then
    restart_docker
    echo "[OK] 已合并 registry 镜像加速: ${REGISTRY_MIRROR}（原配置已备份）"
  else
    echo "${fragment}" > "${daemon_file}.registry-mirror.json"
    echo "[WARN] 缺少 jq/python3，无法安全合并已有配置；未改动 ${daemon_file}" >&2
    echo "[WARN] 请在 ${daemon_file} 中手工添加" >&2
    echo "[WARN]   ${fragment}" >&2
    echo "[WARN] 待合并内容已存于 ${daemon_file}.registry-mirror.json" >&2
  fi
}

# ---- 可选：预拉取运行时镜像（避免首次 up 时网络异常影响部署）----
pre_pull_images() {
  [ "${SKIP_PULL}" -eq 1 ] && { echo "[SKIP] --skip-pull：跳过运行时镜像预拉取"; return 0; }
  if ! command -v docker >/dev/null 2>&1; then
    echo "[WARN] docker 未安装，跳过运行时镜像预拉取" >&2
    return 0
  fi
  local img="alpine:3.22"
  if docker image inspect "${img}" >/dev/null 2>&1; then
    echo "[SKIP] 镜像 ${img} 已缓存在本地"
    return 0
  fi
  echo "[INFO] 预拉取运行时镜像 ${img}（网络不畅可配合 --registry-mirror 重试）..."
  if docker pull "${img}" >/dev/null 2>&1; then
    echo "[OK] 镜像 ${img} 已缓存到本地，后续部署/更新不再依赖外网"
  else
    echo "[WARN] 预拉取 ${img} 失败（首次 docker compose up 时会再次尝试，配置 --registry-mirror 可提高成功率）" >&2
  fi
}

# ============================================================
# 主流程
# ============================================================
echo "===== Docker 环境预装开始 ====="
# curl 用于配置 Docker 官方源 / 拉取 GPG key，属于 Docker 安装的依赖，故在此单独保证
ensure_curl

if [ "${SKIP_DOCKER}" -eq 1 ]; then
  echo "[INFO] --skip-docker：仅校验 docker 环境"
  if ! command -v docker >/dev/null 2>&1; then
    echo "[ERROR] 未检测到 docker，且指定了 --skip-docker，请先安装 Docker" >&2
    exit 1
  fi
  if ! docker compose version >/dev/null 2>&1; then
    echo "[ERROR] docker compose 插件（docker-compose-plugin）不可用，请先安装" >&2
    exit 1
  fi
  echo "[OK] docker + docker compose 插件可用"
else
  ensure_docker
fi

setup_registry_mirror
pre_pull_images
echo "===== Docker 环境预装完成 ====="
