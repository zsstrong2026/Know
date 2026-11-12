#!/usr/bin/env bash
# 工作台备份恢复演练（P4-4）：把备份包恢复到**独立演练库 + 独立卷**，
# 生产库与生产资产卷全程只读，不需要停服。
#
# 用法：
#   scripts/workbench-backup-drill.sh <备份目录> [演练库名] [演练卷名]
#
# 与 scripts/workbench-backup.sh restore 的区别：
#   restore 是事故恢复（覆盖生产），本脚本是日常演练（隔离环境 + 行数比对）。
set -uo pipefail

DIR="${1:?用法：$0 <备份目录> [演练库名] [演练卷名]}"
DIR="${DIR%/}"
DRILL_DB="${2:-weknora_drill}"
DRILL_VOL="${3:-know_data-files-drill}"
PG="${PG_CONTAINER:-WeKnora-postgres}"
PROD_DB="${DB_NAME:-WeKnora}"
SRC_VOL="${ASSET_VOLUME:-know_data-files}"
DB_USER="${DB_USER:-postgres}"
LOG="${DIR}/drill.log"

# Git-Bash / MSYS 会改写 docker 参数里的容器路径。
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

: > "${LOG}"
say() { printf '%s\n' "$*" | tee -a "${LOG}"; }
die() { say "错误：$*"; exit 1; }

[ -f "${DIR}/weknora.dump" ] || die "缺少 ${DIR}/weknora.dump"
[ -f "${DIR}/assets.tar.gz" ] || die "缺少 ${DIR}/assets.tar.gz"

say "== 1. 重建演练库 ${DRILL_DB} =="
docker exec "${PG}" psql -U "${DB_USER}" -d postgres \
    -c "DROP DATABASE IF EXISTS \"${DRILL_DB}\";" >>"${LOG}" 2>&1
docker exec "${PG}" psql -U "${DB_USER}" -d postgres \
    -c "CREATE DATABASE \"${DRILL_DB}\" TEMPLATE template0;" >>"${LOG}" 2>&1

say "== 2. 重建演练卷 ${DRILL_VOL} =="
docker volume rm -f "${DRILL_VOL}" >>"${LOG}" 2>&1
docker volume create "${DRILL_VOL}" >/dev/null

say "== 3. 恢复数据库 =="
START=$(date +%s)
docker exec -i "${PG}" pg_restore -U "${DB_USER}" -d "${DRILL_DB}" \
    --clean --if-exists --no-owner < "${DIR}/weknora.dump" >>"${LOG}" 2>&1
RC=$?
say "pg_restore exit=${RC}，耗时 $(( $(date +%s) - START )) 秒"
[ "${RC}" -eq 0 ] || die "pg_restore 失败，见 ${LOG}"

say "== 4. 恢复资产卷 =="
# 大文件走 stdin 在 Windows/Git-Bash 下会被截断（tar: invalid magic），必须挂盘。
docker run --rm -v "${DRILL_VOL}:/data" \
    -v "$(cd "${DIR}" && pwd):/backup:ro" alpine:3.20 \
    sh -c 'tar xzf /backup/assets.tar.gz -C /data' >>"${LOG}" 2>&1 \
    || die "资产解包失败，见 ${LOG}"

CMP="SELECT 'workbench_items',count(*) FROM workbench_items
UNION ALL SELECT 'workbench_item_versions',count(*) FROM workbench_item_versions
UNION ALL SELECT 'workbench_item_assets',count(*) FROM workbench_item_assets
UNION ALL SELECT 'workbench_archives',count(*) FROM workbench_archives
UNION ALL SELECT 'workbench_folders',count(*) FROM workbench_folders
UNION ALL SELECT 'workbench_tags',count(*) FROM workbench_tags
UNION ALL SELECT 'workbench_templates',count(*) FROM workbench_templates
UNION ALL SELECT 'workbench_notices',count(*) FROM workbench_notices
UNION ALL SELECT 'doc_documents',count(*) FROM doc_documents
UNION ALL SELECT 'resources',count(*) FROM resources
UNION ALL SELECT 'chunks',count(*) FROM chunks;"

say "== 5. 行数对比（生产 vs 演练） =="
PROD=$(docker exec "${PG}" psql -U "${DB_USER}" -d "${PROD_DB}" -tAc "${CMP}")
DRILL=$(docker exec "${PG}" psql -U "${DB_USER}" -d "${DRILL_DB}" -tAc "${CMP}")
if [ "${PROD}" = "${DRILL}" ]; then
    say "行数完全一致："
    printf '%s\n' "${DRILL}" | tee -a "${LOG}"
else
    say "行数不一致！"
    say "生产：${PROD}"
    say "演练：${DRILL}"
fi

# storage_path 在资源目录后端下是 resource:// 句柄，真实路径要查 resources.physical_path。
say "== 6. 资产文件校验 =="
FILES=$(docker exec "${PG}" psql -U "${DB_USER}" -d "${DRILL_DB}" -tAc \
    "SELECT DISTINCT r.physical_path
       FROM workbench_item_assets a
       JOIN resources r ON r.handle = replace(a.storage_path, 'resource://', '')
      WHERE a.deleted_at IS NULL AND r.deleted_at IS NULL;")
TOTAL=0
MISSING=0
while IFS= read -r p; do
    [ -z "${p}" ] && continue
    TOTAL=$((TOTAL + 1))
    if docker run --rm -v "${DRILL_VOL}:/data" alpine:3.20 test -f "/data/${p#local://}"; then
        say "  exists: ${p}"
    else
        say "  MISSING: ${p}"
        MISSING=$((MISSING + 1))
    fi
done <<< "${FILES}"
say "资产文件总数：${TOTAL}，缺失：${MISSING}"

say "== 7. 归档映射与知识存在性 =="
docker exec "${PG}" psql -U "${DB_USER}" -d "${DRILL_DB}" -tAc \
    "SELECT status||'|'||count(*) FROM workbench_archives GROUP BY status ORDER BY 1;" \
    | tee -a "${LOG}"
say "归档指向的知识缺失数：$(docker exec "${PG}" psql -U "${DB_USER}" -d "${DRILL_DB}" -tAc \
    "SELECT count(*) FROM workbench_archives a
      LEFT JOIN knowledges k ON k.id = a.knowledge_id WHERE k.id IS NULL;" | tr -d ' ')"

say "== 8. 快照完整性 =="
say "$(docker exec "${PG}" psql -U "${DB_USER}" -d "${DRILL_DB}" -tAc \
    "SELECT 'empty_ydoc='||count(*) FROM workbench_items
      WHERE deleted_at IS NULL AND snapshot_version > 0
        AND (ydoc IS NULL OR length(ydoc)=0);")"
say "$(docker exec "${PG}" psql -U "${DB_USER}" -d "${DRILL_DB}" -tAc \
    "SELECT 'max_version='||coalesce(max(snapshot_version),0) FROM workbench_items;")"

say "== 9. 迁移版本 =="
say "prod=$(docker exec "${PG}" psql -U "${DB_USER}" -d "${PROD_DB}" -tAc \
    "SELECT version||'|dirty='||dirty FROM schema_migrations;" | tr -d ' ')"
say "drill=$(docker exec "${PG}" psql -U "${DB_USER}" -d "${DRILL_DB}" -tAc \
    "SELECT version||'|dirty='||dirty FROM schema_migrations;" | tr -d ' ')"

say "== 完成。把上面的结果填进 docs/affine-know/backup-drill.md §6 =="
say "清理：docker exec ${PG} psql -U ${DB_USER} -d postgres -c 'DROP DATABASE ${DRILL_DB};' && docker volume rm ${DRILL_VOL}"
