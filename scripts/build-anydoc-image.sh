#!/usr/bin/env bash
# 构建「永久链接 anydoc」的 weknora-app 镜像。
#
# 背景：anydoc 是 Rust 静态库，只能通过 -tags anydoc 链接；直接重建 Dockerfile.app
# 会卡在 browserskill 阶段的 github.com 克隆（当前网络不可达）。本脚本改走
# 「增量烘层」路线，在已有可用镜像之上只替换后端二进制与迁移脚本：
#   1. 在 golang 容器里编出 Rust 归档 libanydoc_go.a（缓存到命名卷，二次构建约 2 分钟）
#   2. 在同一容器里 make build-prod GO_BUILD_TAGS=anydoc，产出后端二进制
#   3. 用 docker/Dockerfile.anydoc 在基础镜像上只替换二进制与迁移文件
# 产物镜像可被 docker compose 直接使用，容器重建后 anydoc 依然可用。
#
# 用法：scripts/build-anydoc-image.sh [选项]
#   --rebuild-lib    强制重建 Rust 归档（默认复用已有 libanydoc_go.a）
#   --restart        构建完成后重建 app 容器
#   --keep-context   保留构建上下文目录，便于排查
#   -h, --help       显示帮助
#
# 环境变量：
#   BASE_IMAGE    继承的基础镜像，默认 weknora-app:local
#   TARGET_IMAGE  产物镜像 tag，默认与 BASE_IMAGE 相同（就地升级）
#   ARCH          目标架构，默认 amd64
#   GO_IMAGE      编译用镜像，默认 golang:1.26-bookworm

set -euo pipefail

# Git-Bash/MSYS 会把容器内的 POSIX 路径改写成 Windows 路径，必须关闭该行为；
# 宿主机路径统一用 cygpath -w 显式转换后再交给 docker。
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

BASE_IMAGE="${BASE_IMAGE:-weknora-app:local}"
TARGET_IMAGE="${TARGET_IMAGE:-$BASE_IMAGE}"
ARCH="${ARCH:-amd64}"
GO_IMAGE="${GO_IMAGE:-golang:1.26-bookworm}"

# 就地升级会覆盖基础镜像。首次运行在这里留一份干净基底，
# 之后每次都从它继承，避免在已烘过 anydoc 的镜像上反复叠层。
CLEAN_BASE="${CLEAN_BASE:-${BASE_IMAGE}-clean-base}"

REBUILD_LIB=0
SQUASH=0
RESTART=0
KEEP_CONTEXT=0

log()  { printf '\033[0;34m[anydoc-image]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[anydoc-image]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[0;31m[anydoc-image]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'TXT'
构建永久链接 anydoc 的 weknora-app 镜像。

用法：scripts/build-anydoc-image.sh [选项]
  --rebuild-lib    强制重建 Rust 归档（默认复用 third_party/anydoc-go/lib 下的产物）
  --squash         构建后扁平化为单层镜像，消除历史二进制层（镜像约 3.3GB -> 2.5GB）
  --restart        构建完成后重建 app 容器并等待健康检查
  --keep-context   保留 .tmp/anydoc-image-context 便于排查
  -h, --help       显示帮助

环境变量：
  BASE_IMAGE    继承的基础镜像，默认 weknora-app:local
  TARGET_IMAGE  产物镜像 tag，默认与 BASE_IMAGE 相同（就地升级）
  ARCH          目标架构，默认 amd64（可选 arm64）
  GO_IMAGE      编译用镜像，默认 golang:1.26-bookworm
TXT
  exit 0
}

while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild-lib)  REBUILD_LIB=1 ;;
    --squash)       SQUASH=1 ;;
    --restart)      RESTART=1 ;;
    --keep-context) KEEP_CONTEXT=1 ;;
    -h|--help)      usage ;;
    *) die "未知参数：$1（-h 查看用法）" ;;
  esac
  shift
done

# 目标架构 -> Rust 三元组 / 静态库目录，需与 scripts/build-anydoc-lib.sh 保持一致
case "$ARCH" in
  amd64) RUST_TARGET=x86_64-unknown-linux-gnu; LIB_DIR=linux_amd64_gnu ;;
  arm64) RUST_TARGET=aarch64-unknown-linux-gnu; LIB_DIR=linux_arm64_gnu ;;
  *) die "不支持的 ARCH=$ARCH（可选 amd64 / arm64）" ;;
esac

ANYDOC_LIB="$ROOT_DIR/third_party/anydoc-go/lib/$LIB_DIR/libanydoc_go.a"
CONTEXT_DIR="$ROOT_DIR/.tmp/anydoc-image-context"
DOCKERFILE="$(cygpath -w "$ROOT_DIR/docker/Dockerfile.anydoc")"
WIN_ROOT="$(cygpath -w "$ROOT_DIR")"
WIN_CONTEXT=""

# 两个编译阶段共用的容器环境：Go 与 Rust 都要能执行，cgo 必须开启
CONTAINER_ENV=(
  -e "CGO_ENABLED=1"
  -e "GOPROXY=${GOPROXY_ARG:-https://goproxy.cn,direct}"
  -e "GOSUMDB=off"
  -e "RUSTUP_HOME=/usr/local/rustup"
  -e "CARGO_HOME=/usr/local/cargo"
)

require_docker() {
  command -v docker >/dev/null 2>&1 || die "未找到 docker 命令"
  docker info >/dev/null 2>&1 || die "Docker 守护进程不可用"
}

require_base_image() {
  docker image inspect "$BASE_IMAGE" >/dev/null 2>&1 ||
    die "基础镜像 $BASE_IMAGE 不存在。请先构建它，或用 BASE_IMAGE=<镜像> 指定已有镜像。"

  # 判定继承来源：优先用干净基底，避免在已烘过 anydoc 的镜像上反复叠层。
  # 干净基底在「当前镜像还没烘过 anydoc」时自动固化，因此首次运行会把
  # 现有的普通镜像保存下来，后续重建始终从同一份基底出发。
  SOURCE_IMAGE="$BASE_IMAGE"
  if docker image inspect "$CLEAN_BASE" >/dev/null 2>&1; then
    SOURCE_IMAGE="$CLEAN_BASE"
    log "继承干净基底：$CLEAN_BASE"
  elif [ "$(image_anydoc_label "$BASE_IMAGE")" = "true" ]; then
    # 基础镜像本身已是 anydoc 版本（例如首次运行时就地升级过）。
    # 仍可正常构建：Go 构建可重现，二进制字节一致时 COPY 层缓存命中，
    # 不会新增层。只有二进制变化时才会多出一层约 290MB 的旧版本。
    warn "基础镜像 $BASE_IMAGE 已是 anydoc 版本，找不到干净基底 $CLEAN_BASE。"
    warn "本次仍会正常构建；二进制未变化时层缓存命中、镜像不膨胀。"
    warn "若长期反复重建，建议先重建一份普通镜像，或用 BASE_IMAGE=<普通镜像> 指定。"
  else
    docker tag "$BASE_IMAGE" "$CLEAN_BASE"
    SOURCE_IMAGE="$CLEAN_BASE"
    log "已将当前镜像固化为干净基底：$CLEAN_BASE"
  fi
}

# 读取镜像上的 anydoc 标记，用于识别「已经烘过层」的产物镜像
image_anydoc_label() {
  docker image inspect "$1" --format '{{index .Config.Labels "org.weknora.anydoc"}}' 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# 步骤 1：Rust 静态库（anydoc 本体）
# ---------------------------------------------------------------------------
build_anydoc_lib() {
  if [ "$REBUILD_LIB" -eq 0 ] && [ -f "$ANYDOC_LIB" ]; then
    log "复用已有 Rust 归档：${ANYDOC_LIB#"$ROOT_DIR"/}（$(du -h "$ANYDOC_LIB" | cut -f1)）"
    return
  fi

  log "构建 Rust 归档（首次装 rustup 并编译约 6 分钟，之后走缓存更快）"
  docker run --rm -i \
    --platform "linux/$ARCH" \
    -v "$WIN_ROOT:/src" \
    -v anydoc-rustup:/usr/local/rustup \
    -v anydoc-cargo:/usr/local/cargo \
    -e "RUST_TARGET=$RUST_TARGET" \
    -w /src "${CONTAINER_ENV[@]}" \
    "$GO_IMAGE" bash -s <<'EOS'
set -euo pipefail
export PATH=/usr/local/cargo/bin:$PATH

# 全部走 rsproxy.cn：github.com 与 static.crates.io 在当前网络下不可达
if ! command -v rustup >/dev/null 2>&1; then
  export RUSTUP_DIST_SERVER=https://rsproxy.cn
  export RUSTUP_UPDATE_ROOT=https://rsproxy.cn/rustup
  curl --proto '=https' --tlsv1.2 -sSf --retry 3 -o /tmp/rustup-init.sh https://rsproxy.cn/rustup-init.sh
  sh /tmp/rustup-init.sh -y --profile minimal --default-toolchain stable
fi

# cargo 镜像配置写进命名卷，容器重建后依然生效；已存在则不覆盖
if [ ! -f "$CARGO_HOME/config.toml" ]; then
  mkdir -p "$CARGO_HOME"
  cat > "$CARGO_HOME/config.toml" <<'CFG'
[source.crates-io]
replace-with = "rsproxy-sparse"

[source.rsproxy-sparse]
registry = "sparse+https://rsproxy.cn/index/"

[net]
git-fetch-with-cli = true
retry = 5
CFG
fi

cd /src
TARGET="$RUST_TARGET" bash scripts/build-anydoc-lib.sh
EOS
}

# ---------------------------------------------------------------------------
# 步骤 2：Go 后端二进制（-tags anydoc）
# ---------------------------------------------------------------------------
build_binary() {
  log "编译后端二进制（-tags anydoc，linux/$ARCH）"
  docker run --rm -i \
    --platform "linux/$ARCH" \
    -v "$WIN_ROOT:/src" \
    -v weknora-gomod:/go/pkg/mod \
    -v weknora-gocache:/root/.cache/go-build \
    -w /src "${CONTAINER_ENV[@]}" \
    "$GO_IMAGE" bash -s <<'EOS'
set -euo pipefail
export PATH=/usr/local/go/bin:/usr/local/cargo/bin:$PATH

# cgo 链接 sqlite 需要头文件；保持幂等，二次构建不重复安装
if [ ! -f /usr/include/sqlite3.h ]; then
  apt-get update -qq
  apt-get install -y -qq libsqlite3-dev >/dev/null
fi

GO_BUILD_TAGS=anydoc make build-prod

# 就地验证链接结果，避免把未链接的二进制烘进镜像
grep -aq 'firecrawl/anydoc/go' WeKnora || {
  echo "错误：二进制缺少 anydoc 绑定符号，-tags anydoc 未生效" >&2; exit 1; }
grep -aq 'anydoc engine not built into this binary' WeKnora && {
  echo "错误：二进制仍带 anydoc stub 标记，链接被降级" >&2; exit 1; }
echo "二进制 anydoc 校验通过"
EOS
}

# ---------------------------------------------------------------------------
# 步骤 3：准备最小构建上下文（只放二进制与迁移，避免上传数百 MB 源码）
# ---------------------------------------------------------------------------
prepare_context() {
  log "准备构建上下文：.tmp/anydoc-image-context"
  rm -rf "$CONTEXT_DIR"
  mkdir -p "$CONTEXT_DIR/migrations"

  # 编译输出直接落在挂载的宿主机目录，这里只做同盘移动，不再复制 290MB
  mv "$ROOT_DIR/WeKnora" "$CONTEXT_DIR/WeKnora"
  # 迁移必须与二进制同批更新：容器启动时 AUTO_MIGRATE 以 /app/migrations 为源，
  # 只换二进制会让 schema 版本停在旧值（见 workbench-handover.md §7.2）
  cp -R "$ROOT_DIR/migrations/versioned" "$CONTEXT_DIR/migrations/versioned"

  WIN_CONTEXT="$(cygpath -w "$CONTEXT_DIR")"
  log "上下文大小：$(du -sh "$CONTEXT_DIR" | cut -f1)"
}

# ---------------------------------------------------------------------------
# 步骤 4：在基础镜像上烘入二进制与迁移
# ---------------------------------------------------------------------------
build_image() {
  log "构建镜像：$SOURCE_IMAGE -> $TARGET_IMAGE"
  docker build \
    --platform "linux/$ARCH" \
    -f "$DOCKERFILE" \
    --build-arg "BASE_IMAGE=$SOURCE_IMAGE" \
    -t "$TARGET_IMAGE" \
    "$WIN_CONTEXT"
}

verify_image() {
  log "校验产物镜像"
  docker run --rm --platform "linux/$ARCH" --entrypoint sh "$TARGET_IMAGE" -c '
    test -x /app/WeKnora || { echo "二进制不可执行"; exit 1; }
    grep -aq "firecrawl/anydoc/go" /app/WeKnora || { echo "镜像内缺少 anydoc 符号"; exit 1; }
    if grep -aq "anydoc engine not built into this binary" /app/WeKnora; then
      echo "镜像内仍是未链接版本"; exit 1
    fi
    echo "镜像内二进制 anydoc 校验通过"
  '
}

# ---------------------------------------------------------------------------
# 步骤 5（可选）：扁平化为单层镜像
# ---------------------------------------------------------------------------
# buildx 当前没有 --squash，因此用 export/import 把多层压成一层。
# 代价：import 会丢镜像配置，所以再用 Dockerfile.anydoc-squash 补回
# PATH / ENTRYPOINT / CMD / EXPOSE / LABEL。典型收益 3.3GB -> 2.5GB。
squash_image() {
  local tmp_tar="$ROOT_DIR/.tmp/anydoc-squash.tar"
  local tmp_ctr="anydoc-squash-$$"
  local raw_tag="$TARGET_IMAGE-squash-raw"

  log "扁平化镜像：$TARGET_IMAGE（清理历史二进制层）"
  docker create --name "$tmp_ctr" --entrypoint sh "$TARGET_IMAGE" -c true >/dev/null
  # 用 -o 落盘再 import：走 stdin 管道在 Windows 上会把大 tar 截断
  docker export -o "$(cygpath -w "$tmp_tar")" "$tmp_ctr"
  docker rm -f "$tmp_ctr" >/dev/null

  docker import "$(cygpath -w "$tmp_tar")" "$raw_tag" >/dev/null
  rm -f "$tmp_tar"

  mkdir -p "$ROOT_DIR/.tmp/empty-ctx"
  docker build \
    --platform "linux/$ARCH" \
    -f "$(cygpath -w "$ROOT_DIR/docker/Dockerfile.anydoc-squash")" \
    --build-arg "RAW_IMAGE=$raw_tag" \
    -t "$TARGET_IMAGE" \
    "$(cygpath -w "$ROOT_DIR/.tmp/empty-ctx")" >/dev/null
  docker rmi "$raw_tag" >/dev/null 2>&1 || true
  rm -rf "$ROOT_DIR/.tmp/empty-ctx"

  log "扁平化后体积：$(docker images "$TARGET_IMAGE" --format '{{.Size}}')"
}

restart_app() {
  log "重建 app 容器以启用新镜像"
  ( cd "$ROOT_DIR" && docker compose up -d --no-deps --force-recreate app )
  log "等待健康检查（最多 150 秒）"
  for _ in $(seq 1 30); do
    if docker ps --filter name=WeKnora-app --format '{{.Status}}' | grep -q healthy; then
      log "app 容器已 healthy"
      return
    fi
    sleep 5
  done
  warn "轮询超时仍未 healthy，请检查 docker logs WeKnora-app"
}

cleanup() {
  # 编译失败时上下文不存在，宿主机根目录会残留 WeKnora；先清残留再删上下文
  if [ -f "$ROOT_DIR/WeKnora" ] && [ ! -f "$CONTEXT_DIR/WeKnora" ]; then
    rm -f "$ROOT_DIR/WeKnora"
  fi
  if [ "$KEEP_CONTEXT" -eq 0 ]; then
    rm -rf "$CONTEXT_DIR"
  elif [ -d "$CONTEXT_DIR" ]; then
    log "保留构建上下文：$CONTEXT_DIR"
  fi
}
trap cleanup EXIT

main() {
  require_docker
  require_base_image
  build_anydoc_lib
  build_binary
  prepare_context
  build_image
  if [ "$SQUASH" -eq 1 ]; then
    squash_image
  fi
  verify_image

  log "完成：$TARGET_IMAGE"
  if [ "$RESTART" -eq 1 ]; then
    restart_app
  else
    log "让容器生效：docker compose up -d --no-deps --force-recreate app"
    log "验证接口：curl -H 'X-API-Key: <key>' http://localhost:8080/api/v1/system/parser-engines"
  fi
}

main "$@"
