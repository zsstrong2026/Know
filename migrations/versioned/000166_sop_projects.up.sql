-- Migration: 000166_sop_projects
-- Description: SOP 工作台：项目表（项目编号前缀 / 事项编号自增游标 / 项目知识库 / 仓库绑定）
--
-- 归属口径：tenant_id 贯穿且所有查询强制过滤；软删 deleted_at。
-- next_item_no 是事项编号 <code>-<n> 的分配游标，初始 0，AllocItemNo 自增后返回（首次=1）。
-- kb_owner ∈ project（随项目删除）/ linked（改绑，仅解绑）/ pm（迁移，仅解绑）。

CREATE TABLE IF NOT EXISTS sop_projects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    owner_user_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    code VARCHAR(10) NOT NULL,
    next_item_no INTEGER NOT NULL DEFAULT 0,
    kb_id VARCHAR(36) NOT NULL DEFAULT '',
    kb_owner VARCHAR(16) NOT NULL DEFAULT 'project',
    brief_knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    brief_rendered_seq BIGINT NOT NULL DEFAULT 0,
    default_template_id VARCHAR(36) NOT NULL DEFAULT '',
    default_executor VARCHAR(64) NOT NULL DEFAULT 'platform_agent',
    on_run_success_item_status VARCHAR(32) NOT NULL DEFAULT '',
    notify_on_gate_or_fail BOOLEAN NOT NULL DEFAULT TRUE,
    workspace_root VARCHAR(500) NOT NULL DEFAULT '',
    repos JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    domain_data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_projects_tenant_code ON sop_projects(tenant_id, code) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_projects_tenant_name ON sop_projects(tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_sop_projects_tenant ON sop_projects(tenant_id, deleted_at);
