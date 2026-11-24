-- Migration: 000076_sop_project_notes（PostgreSQL 000175 的 SQLite 对应实现）
-- 类型映射：TIMESTAMPTZ→DATETIME、NOW()→CURRENT_TIMESTAMP、BIGINT→INTEGER、
-- BOOLEAN→BOOLEAN（默认写 0/1，照 000069/000073）。
--
-- 编号说明：计划原定 PG 000174 ↔ SQLite 000076，执行时 000174 已被
-- meeting_phase_events 占用，PG 侧顺延为 000175；SQLite 侧 000076 未被占用，
-- 对应关系保持 000175 ↔ 000076（§15 编号对照同步修订）。
--
-- 归属口径：tenant_id + project_id 贯穿；软删且软删行不占 seq 位。
-- sop_projects/sop_items 的 M11 新列已就地写入 000068/000069 建表语句，
-- 本迁移只负责黑板表与 parent 部分索引。

CREATE TABLE IF NOT EXISTS sop_project_notes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'note',
    title VARCHAR(500) NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    item_id VARCHAR(36) NOT NULL DEFAULT '',
    run_id VARCHAR(36) NOT NULL DEFAULT '',
    source VARCHAR(16) NOT NULL DEFAULT 'human',
    pinned BOOLEAN NOT NULL DEFAULT 0,
    seq INTEGER NOT NULL DEFAULT 0,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_notes_project_seq ON sop_project_notes(project_id, seq) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_sop_notes_project_kind ON sop_project_notes(tenant_id, project_id, kind, created_at);
CREATE INDEX IF NOT EXISTS ix_sop_items_parent ON sop_items(parent_id) WHERE deleted_at IS NULL;
