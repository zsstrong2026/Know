#!/usr/bin/env bash
# 工作台备份 / 恢复 / 校验脚本（实现方案 §12.5、§14.8）
#
# 备份四类：数据库、资产文件、配置、索引元信息（embedding 模型 ID）。
# 资产是唯一无法重建的部分；索引可以重建，因此只记录模型 ID，不复制向量。
#
# 用法：
#   scripts/workbench-backup.sh backup  [输出目录]      # 默认 backups/<UTC 时间戳>
#   scripts/workbench-backup.sh restore <备份目录> --yes # 必须显式确认，禁止误覆盖
#   scripts/workbench-backup.sh verify  <备份目录>       # 只校验，不写任何数据
#
# 依赖：docker（PG / 容器化部署）、tar、sha256sum（macOS 用 shasum）。
set -euo pipefail

# Git-Bash / MSYS 会把 docker 参数里的 `/data`、`/tmp` 改写成 Windows 路径，
# 导致容器内挂载点与 tar 目录都变成 "C:/Program Files/Git/data"。
# 本脚本的容器路径全部是有意为之，因此关闭这项自动转换。
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# 容器名与卷名与 docker-compose.yml 保持一致，可用环境变量覆盖。
APP_CONTAINER="${APP_CONTAINER:-WeKnora-app}"
PG_CONTAINER="${PG_CONTAINER:-WeKnora-postgres}"
ASSET_VOLUME="${ASSET_VOLUME:-know_data-files}"

# 读取 .env 取得库名/用户名/存储配置（不打印任何密钥）。
if [ -f "${PROJECT_ROOT}/.env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "${PROJECT_ROOT}/.env"
    set +a
fi

DB_DRIVER="${DB_DRIVER:-postgres}"
DB_USER="${DB_USER:-postgres}"
DB_NAME="${DB_NAME:-WeKnora}"
DB_PATH="${DB_PATH:-./data/weknora.db}"
STORAGE_TYPE="${STORAGE_TYPE:-local}"

log() { printf '[workbench-backup] %s\n' "$*"; }
die() { printf '[workbench-backup] 错误：%s\n' "$*" >&2; exit 1; }

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# 卷名前缀随 compose 项目名变化（默认目录名 know → know_data-files）。
resolve_asset_volume() {
    if docker volume inspect "${ASSET_VOLUME}" >/dev/null 2>&1; then
        printf '%s' "${ASSET_VOLUME}"
        return 0
    fi
    local candidate
    candidate="$(docker volume ls --format '{{.Name}}' | grep -E '_data-files$' | head -n 1 || true)"
    [ -n "${candidate}" ] || die "找不到资产卷 ${ASSET_VOLUME}，请用 ASSET_VOLUME=<卷名> 指定"
    printf '%s' "${candidate}"
}

# ---- 备份 ----

backup_database() {
    local out_dir="$1"
    if [ "${DB_DRIVER}" = "sqlite" ]; then
        # SQLite 必须停服或使用 .backup 的在线快照，直接 cp 可能拿到半写状态。
        log "备份 SQLite：${DB_PATH}"
        # MSYS2_ARG_CONV_EXCL 阻止 Git-Bash 把容器内 /tmp 改写成 Windows 路径。
        MSYS2_ARG_CONV_EXCL='*' docker exec "${APP_CONTAINER}" sh -c \
            "sqlite3 '${DB_PATH}' \".backup '/tmp/weknora.db'\"" \
            || die "sqlite3 不在镜像内，请改用停服后复制文件"
        docker cp "${APP_CONTAINER}:/tmp/weknora.db" "${out_dir}/weknora.db"
        return 0
    fi
    log "备份 PostgreSQL：${DB_NAME}（custom 格式，可选择性恢复单表）"
    # 直接写 stdout 再落到宿主文件，不用容器内 /tmp：
    # Git-Bash 等 MSYS 环境会把 docker exec 参数里的 /tmp/... 改写成 Windows 路径。
    docker exec "${PG_CONTAINER}" \
        pg_dump -U "${DB_USER}" -d "${DB_NAME}" -Fc > "${out_dir}/weknora.dump"
}

backup_assets() {
    local out_dir="$1"
    if [ "${STORAGE_TYPE}" != "local" ]; then
        # 对象存储的备份属于厂商侧能力，脚本不代为复制，只留一条明确记录。
        log "STORAGE_TYPE=${STORAGE_TYPE}：资产不在本地卷，跳过文件复制（请在对象存储侧开启版本/复制）"
        printf 'storage_type=%s（资产由对象存储侧备份）\n' "${STORAGE_TYPE}" \
            > "${out_dir}/assets.SKIPPED.txt"
        return 0
    fi
    local volume
    volume="$(resolve_asset_volume)"
    log "备份资产卷：${volume}"
    # -C / 后打包全部内容；输出同样走 stdout，避开容器内临时路径。
    docker run --rm -v "${volume}:/data:ro" alpine:3.20 \
        tar czf - -C /data . > "${out_dir}/assets.tar.gz"
}

backup_config() {
    local out_dir="$1"
    # 配置只留结构，不留明文密钥：.env 里的密码/Token 不进入备份包。
    log "备份配置（脱敏）"
    mkdir -p "${out_dir}/config"
    if [ -f "${PROJECT_ROOT}/config/config.yaml" ]; then
        cp "${PROJECT_ROOT}/config/config.yaml" "${out_dir}/config/config.yaml"
    fi
    if [ -f "${PROJECT_ROOT}/.env" ]; then
        grep -vE '^[[:space:]]*(DB_PASSWORD|REDIS_PASSWORD|.*_API_KEY|.*_SECRET|.*_TOKEN)=' \
            "${PROJECT_ROOT}/.env" > "${out_dir}/config/env.redacted" || true
    fi
    # 索引可重建，但模型必须一致，否则重建后的向量维度对不上。
    {
        printf 'embedding_model_id=%s\n' "$(read_embedding_model_id)"
        printf 'db_driver=%s\n' "${DB_DRIVER}"
        printf 'storage_type=%s\n' "${STORAGE_TYPE}"
    } > "${out_dir}/config/index-meta.txt"
}

# 从库里读工作知识库绑定的 embedding 模型；失败不影响备份，只写 unknown。
read_embedding_model_id() {
    if [ "${DB_DRIVER}" = "sqlite" ]; then
        printf 'unknown'
        return 0
    fi
    docker exec "${PG_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -tAc \
        "SELECT COALESCE(MAX(embedding_model_id), '') FROM knowledge_bases WHERE is_system = true" \
        2>/dev/null || printf 'unknown'
}

cmd_backup() {
    local out_dir="${1:-${PROJECT_ROOT}/backups/$(date -u +%Y%m%dT%H%M%SZ)}"
    mkdir -p "${out_dir}"
    out_dir="$(cd "${out_dir}" && pwd)"
    log "输出目录：${out_dir}"

    backup_database "${out_dir}"
    backup_assets "${out_dir}"
    backup_config "${out_dir}"

    # 校验和清单放在最后：恢复前先校验，避免用半截包覆盖生产库。
    ( cd "${out_dir}" && find . -type f ! -name 'SHA256SUMS' -print0 \
        | xargs -0 sha256sum > SHA256SUMS )
    log "完成。校验和：${out_dir}/SHA256SUMS"
}

# ---- 校验 ----

cmd_verify() {
    local dir="${1:?用法：verify <备份目录>}"
    [ -f "${dir}/SHA256SUMS" ] || die "缺少 SHA256SUMS，备份不完整"
    ( cd "${dir}" && sha256sum -c SHA256SUMS )
    if [ "${DB_DRIVER}" = "postgres" ] && [ -f "${dir}/weknora.dump" ]; then
        # 只列目录，不写库；能列出说明 dump 头部完整。
        docker exec -i "${PG_CONTAINER}" pg_restore -l >/dev/null < "${dir}/weknora.dump" \
            || die "dump 无法解析，备份可能损坏"
    fi
    log "校验通过"
}

# ---- 恢复 ----

cmd_restore() {
    local dir="${1:?用法：restore <备份目录> --yes}"
    local confirmed="${2:-}"
    [ "${confirmed}" = "--yes" ] || die "恢复会覆盖当前数据，请追加 --yes 确认"

    cmd_verify "${dir}"
    log "停止应用容器（避免恢复期间写入）"
    docker stop "${APP_CONTAINER}" >/dev/null

    if [ "${DB_DRIVER}" = "postgres" ]; then
        # --clean --if-exists：把同名对象替换为备份内容。
        # 迁移版本随 dump 一起回落，因此不需要（也禁止）再跑 down 迁移。
        log "恢复 PostgreSQL"
        docker exec -i "${PG_CONTAINER}" \
            pg_restore -U "${DB_USER}" -d "${DB_NAME}" --clean --if-exists --no-owner \
            < "${dir}/weknora.dump"
    else
        log "恢复 SQLite"
        docker cp "${dir}/weknora.db" "${APP_CONTAINER}:${DB_PATH}"
    fi

    if [ -f "${dir}/assets.tar.gz" ]; then
        local volume
        volume="$(resolve_asset_volume)"
        log "恢复资产卷：${volume}"
        # 走挂载而不是 stdin：Git-Bash/Windows 的管道对大文件会截断成 "tar: invalid magic"。
        docker run --rm \
            -v "${volume}:/data" \
            -v "$(cd "${dir}" && pwd):/backup:ro" \
            alpine:3.20 \
            sh -c 'rm -rf /data/*; tar xzf /backup/assets.tar.gz -C /data'
    fi

    log "启动应用容器"
    docker start "${APP_CONTAINER}" >/dev/null
    log "恢复完成。按 docs/affine-know/backup-drill.md 执行只读检查清单。"
}

case "${1:-}" in
    backup)  shift; cmd_backup  "$@" ;;
    restore) shift; cmd_restore "$@" ;;
    verify)  shift; cmd_verify  "$@" ;;
    *) die "用法：$0 {backup [目录] | restore <目录> --yes | verify <目录>}" ;;
esac
