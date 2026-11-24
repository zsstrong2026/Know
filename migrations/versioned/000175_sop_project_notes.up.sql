-- Migration: 000175_sop_project_notes
-- Description: SOP 工作台：项目黑板（共享上下文时间线）与父项拆分索引
--
-- 编号说明：计划原定 000174，执行时该号已被 meeting_phase_events 占用，按计划
-- §15 规则顺延到 000175（SQLite 侧 000076 未被占用，保持对应）。
--
-- 归属口径：tenant_id + project_id 贯穿且所有查询强制过滤；软删 deleted_at。
-- seq 是项目内自增序号（黑板时间线的稳定排序键，编辑也推进），软删行不占位。
-- 说明：kb_owner/brief_knowledge_id/brief_rendered_seq 已随 000166 建表、
-- parent_id/split_source 已随 000167 建表，本迁移不再 ALTER——sop_* 是本轮新增表，
-- 无存量数据需要补列，避免 PG/SQLite 上 ALTER 语义差异。本迁移只负责黑板表与
-- parent 部分索引。

CREATE TABLE IF NOT EXISTS sop_project_notes (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'note',
    title VARCHAR(500) NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    item_id VARCHAR(36) NOT NULL DEFAULT '',
    run_id VARCHAR(36) NOT NULL DEFAULT '',
    source VARCHAR(16) NOT NULL DEFAULT 'human',
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    seq BIGINT NOT NULL DEFAULT 0,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_notes_project_seq ON sop_project_notes(project_id, seq) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_sop_notes_project_kind ON sop_project_notes(tenant_id, project_id, kind, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_sop_items_parent ON sop_items(parent_id) WHERE deleted_at IS NULL;
