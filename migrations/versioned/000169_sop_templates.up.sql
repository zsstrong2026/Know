-- Migration: 000169_sop_templates
-- Description: SOP 工作台：流程模板与发布 revision（发布即冻结的不可变快照）
--
-- 归属口径：tenant_id 贯穿且所有查询强制过滤；软删 deleted_at。
-- definition 是当前草稿（JSONB 文档，不是可查询列集）；运行只引用 revision 的冻结快照，
-- 不读活模板（spec §3.3）。
-- is_builtin 的内置模板只读（复制后修改）；T7 以 tenant_id=0 的行下发全租户可见的 seed。
-- revisions 无 deleted_at：不可变快照永不删（spec §3.3），同一模板内 revision 唯一。
-- metric_category ∈ 交付/调研/运维（供统计）。

CREATE TABLE IF NOT EXISTS sop_templates (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    version INTEGER NOT NULL DEFAULT 1,
    published_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    definition JSONB NOT NULL DEFAULT '{}',
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    metric_category VARCHAR(32) NOT NULL DEFAULT '',
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_templates_tenant ON sop_templates(tenant_id, project_id, status, deleted_at);

CREATE TABLE IF NOT EXISTS sop_template_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    template_id VARCHAR(36) NOT NULL,
    revision INTEGER NOT NULL,
    definition JSONB NOT NULL,
    published_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_revisions_template_rev ON sop_template_revisions(template_id, revision);
