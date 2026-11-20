-- Migration: 000069_sop_items（PostgreSQL 000167 的 SQLite 对应实现）
-- 类型映射：JSONB→JSON、TIMESTAMPTZ→DATETIME、BYTEA→BLOB、NOW()→CURRENT_TIMESTAMP、
-- BOOLEAN→BOOLEAN（默认写 0/1，照 000068）、BIGINT→INTEGER。
--
-- 归属口径：tenant_id 贯穿且所有查询强制过滤；软删 deleted_at。
-- no 是项目内编号（<项目 code>-<n>，由 sop_projects.next_item_no 分配），项目内唯一。
-- is_big/parent_id/split_source 是大需求两级拆分的承载列（T59 追加 parent 部分索引）。
-- 附件 data 为 BLOB 直存，列表查询不取该列。

CREATE TABLE IF NOT EXISTS sop_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    no VARCHAR(24) NOT NULL,
    title VARCHAR(500) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    kind VARCHAR(16) NOT NULL DEFAULT 'requirement',
    priority VARCHAR(4) NOT NULL DEFAULT 'P2',
    status VARCHAR(16) NOT NULL DEFAULT 'backlog',
    source VARCHAR(16) NOT NULL DEFAULT 'internal',
    proposer VARCHAR(200) NOT NULL DEFAULT '',
    owner VARCHAR(200) NOT NULL DEFAULT '',
    tags JSON NOT NULL DEFAULT '[]',
    template_id VARCHAR(36) NOT NULL DEFAULT '',
    kb_id VARCHAR(36) NOT NULL DEFAULT '',
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    outcome JSON NOT NULL DEFAULT '{}',
    domain_data JSON NOT NULL DEFAULT '{}',
    last_run_id VARCHAR(36) NOT NULL DEFAULT '',
    is_big BOOLEAN NOT NULL DEFAULT 0,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    split_source VARCHAR(16) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_items_project_no ON sop_items(project_id, no) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_sop_items_tenant_project ON sop_items(tenant_id, project_id, status, deleted_at);
CREATE INDEX IF NOT EXISTS ix_sop_items_last_run ON sop_items(last_run_id);

CREATE TABLE IF NOT EXISTS sop_item_attachments (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    name VARCHAR(500) NOT NULL,
    media_type VARCHAR(100) NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    data BLOB,
    text_content TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS ix_sop_item_attachments_item ON sop_item_attachments(item_id, deleted_at);
