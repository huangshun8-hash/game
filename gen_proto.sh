#!/usr/bin/env bash
# 编译 pb/ 下所有 .proto 文件，输出到项目根目录（按 go_package 路径落盘）。
#
# 工具链版本已锁定，生成文件头部的版本注释必须稳定，避免不同机器生成结果漂移：
#   protoc        30.2   （生成注释中显示为 v6.30.2）
#   protoc-gen-go v1.36.6
#
# 安装方式：
#   protoc        ：从 https://github.com/protocolbuffers/protobuf/releases/tag/v30.2 下载对应平台压缩包解压
#   protoc-gen-go ：go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
#
# 可执行文件的查找顺序（不依赖任何个人机器上的固定安装目录）：
#   1. PROTOC / PROTOC_GEN_GO 环境变量指定的路径（多版本共存时用它精确指定）
#   2. PROTOC_HOME/bin 下的 protoc（便于把整个解压目录整体指向某个版本）
#   3. GOPATH/bin 下的 protoc-gen-go（go install 的默认落点）
#   4. PATH
#
# 可选环境变量：
#   PROTOC          ：protoc 可执行文件路径
#   PROTOC_GEN_GO   ：protoc-gen-go 可执行文件路径
#   PROTOC_HOME     ：protoc 解压目录（脚本会查找其 bin/protoc）

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 锁定的工具链版本
readonly WANT_PROTOC_VERSION="30.2"
readonly WANT_PROTOC_GEN_GO_VERSION="v1.36.6"

# findExecutable 按候选路径依次探测可执行文件，输出第一个命中的路径。
# 入参：候选路径列表（可为空串）
# 返回：命中路径写入 stdout；全部未命中则返回非 0
findExecutable() {
  local candidate
  for candidate in "$@"; do
    [[ -n "${candidate}" ]] || continue
    if [[ -x "${candidate}" ]]; then
      echo "${candidate}"
      return 0
    fi
    # 允许传入命令名，交由 PATH 解析
    if command -v "${candidate}" >/dev/null 2>&1; then
      command -v "${candidate}"
      return 0
    fi
  done
  return 1
}

# GOPATH/bin 是 go install 的默认落点；go env 不可用时置空，后续仅回退到 PATH
GOPATH_BIN=""
if GOPATH_DIR="$(go env GOPATH 2>/dev/null)" && [[ -n "${GOPATH_DIR}" ]]; then
  GOPATH_BIN="${GOPATH_DIR}/bin"
fi

# 解析 protoc：PROTOC → PROTOC_HOME/bin → PATH（不硬编码任何机器专属安装路径）
PROTOC_HOME_BIN=""
if [[ -n "${PROTOC_HOME:-}" ]]; then
  PROTOC_HOME_BIN="${PROTOC_HOME%/}/bin/protoc"
fi
PROTOC_BIN="$(findExecutable \
  "${PROTOC:-}" \
  "${PROTOC_HOME_BIN}" \
  "${PROTOC_HOME_BIN}.exe" \
  "protoc")" || {
  echo "错误：未找到 protoc。请安装 protoc ${WANT_PROTOC_VERSION}，并加入 PATH，或通过 PROTOC / PROTOC_HOME 环境变量指定路径。" >&2
  echo "      下载地址：https://github.com/protocolbuffers/protobuf/releases/tag/v${WANT_PROTOC_VERSION}" >&2
  exit 1
}

# 解析 protoc-gen-go：显式传给 protoc，避免 PATH 中存在多个版本时选错
PROTOC_GEN_GO_BIN="$(findExecutable \
  "${PROTOC_GEN_GO:-}" \
  "${GOPATH_BIN:+${GOPATH_BIN}/protoc-gen-go.exe}" \
  "${GOPATH_BIN:+${GOPATH_BIN}/protoc-gen-go}" \
  "protoc-gen-go")" || {
  echo "错误：未找到 protoc-gen-go，请执行：go install google.golang.org/protobuf/cmd/protoc-gen-go@${WANT_PROTOC_GEN_GO_VERSION}" >&2
  exit 1
}

# 校验 protoc 版本（输出形如：libprotoc 30.2）
GOT_PROTOC_VERSION="$("${PROTOC_BIN}" --version | awk '{print $2}')"
if [[ "${GOT_PROTOC_VERSION}" != "${WANT_PROTOC_VERSION}" ]]; then
  echo "错误：protoc 版本不匹配，期望 ${WANT_PROTOC_VERSION}，实际 ${GOT_PROTOC_VERSION}（${PROTOC_BIN}）。" >&2
  echo "      请下载 https://github.com/protocolbuffers/protobuf/releases/tag/v${WANT_PROTOC_VERSION} 后通过 PROTOC 环境变量指定。" >&2
fi

# 校验 protoc-gen-go 版本（输出形如：protoc-gen-go.exe v1.36.6）
GOT_PROTOC_GEN_GO_VERSION="$("${PROTOC_GEN_GO_BIN}" --version 2>&1 | awk '{print $NF}')"
if [[ "${GOT_PROTOC_GEN_GO_VERSION}" != "${WANT_PROTOC_GEN_GO_VERSION}" ]]; then
  echo "错误：protoc-gen-go 版本不匹配，期望 ${WANT_PROTOC_GEN_GO_VERSION}，实际 ${GOT_PROTOC_GEN_GO_VERSION}（${PROTOC_GEN_GO_BIN}）。" >&2
  echo "      请执行：go install google.golang.org/protobuf/cmd/protoc-gen-go@${WANT_PROTOC_GEN_GO_VERSION}" >&2
fi

echo "protoc ${GOT_PROTOC_VERSION} / protoc-gen-go ${GOT_PROTOC_GEN_GO_VERSION}，开始生成..."

"${PROTOC_BIN}" \
  --plugin=protoc-gen-go="${PROTOC_GEN_GO_BIN}" \
  --proto_path="${ROOT_DIR}" \
  --go_out="${ROOT_DIR}" \
  --go_opt=module=kemiluo \
  $(find "${ROOT_DIR}/pb" -name "*.proto" | sed "s|${ROOT_DIR}/||")

echo "生成完成。"
