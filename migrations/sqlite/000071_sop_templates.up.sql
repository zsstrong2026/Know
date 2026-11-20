-- Migration: 000071_sop_templates（PostgreSQL 000169 的 SQLite 对应实现）
-- 类型映射：JSONB→JSON、TIMESTAMPTZ→DATETIME、NOW()→CURRENT_TIMESTAMP、
-- BOOLEAN→BOOLEAN（默认写 0/1，照 000069）、BIGINT→INTEGER。
--
-- 归属口径：tenant_id 贯穿且所有查询强制过滤；软删 deleted_at。
-- definition 是当前草稿（JSON 文档）；运行只引用 revision 的冻结快照，不读活模板（spec §3.3）。
-- is_builtin 的内置模板只读（复制后修改）；T7 以 tenant_id=0 的行下发全租户可见的 seed。
-- revisions 无 deleted_at：不可变快照永不删，同一模板内 revision 唯一。

CREATE TABLE IF NOT EXISTS sop_templates (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    project_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    version INTEGER NOT NULL DEFAULT 1,
    published_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    definition JSON NOT NULL DEFAULT '{}',
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    metric_category VARCHAR(32) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS ix_sop_templates_tenant ON sop_templates(tenant_id, project_id, status, deleted_at);

CREATE TABLE IF NOT EXISTS sop_template_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    template_id VARCHAR(36) NOT NULL,
    revision INTEGER NOT NULL,
    definition JSON NOT NULL,
    published_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_revisions_template_rev ON sop_template_revisions(template_id, revision);
