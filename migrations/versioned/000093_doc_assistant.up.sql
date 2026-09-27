-- Migration: 000093_doc_assistant
-- Description: 文档助手模块三张表（模板 / 生成任务 / 生成文档-版本化）

-- 文档模板（个人私有）
CREATE TABLE IF NOT EXISTS doc_templates (
    id            VARCHAR(36)  NOT NULL PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL,
    owner_id      VARCHAR(64)  NOT NULL,
    name          VARCHAR(200) NOT NULL,
    description   TEXT,
    global_config JSONB        NOT NULL DEFAULT '{}',
    chapters      JSONB        NOT NULL DEFAULT '[]',
    created_at    TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    deleted_at    TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_doc_templates_tenant_owner ON doc_templates (tenant_id, owner_id);
CREATE INDEX IF NOT EXISTS idx_doc_templates_deleted_at ON doc_templates (deleted_at);

-- 生成任务
CREATE TABLE IF NOT EXISTS doc_generation_tasks (
    id                 VARCHAR(36)  NOT NULL PRIMARY KEY,
    tenant_id          BIGINT       NOT NULL,
    owner_id           VARCHAR(64)  NOT NULL,
    template_id        VARCHAR(36)  NOT NULL,
    template_snapshot  JSONB,
    kb_ids             JSONB        NOT NULL DEFAULT '[]',
    kb_filters         JSONB,
    model_id           VARCHAR(100),
    theme              TEXT         NOT NULL,
    extra_requirement  TEXT,
    status             VARCHAR(32)  NOT NULL DEFAULT 'pending',
    progress           JSONB,
    error              TEXT,
    created_at         TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    finished_at        TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_tenant_owner ON doc_generation_tasks (tenant_id, owner_id);
CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_template_id ON doc_generation_tasks (template_id);
CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_status ON doc_generation_tasks (status);

-- 生成文档（版本化）
CREATE TABLE IF NOT EXISTS doc_documents (
    id         VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id  BIGINT      NOT NULL,
    owner_id   VARCHAR(64) NOT NULL,
    task_id    VARCHAR(36) NOT NULL,
    version    INT         NOT NULL DEFAULT 1,
    status     VARCHAR(32) NOT NULL DEFAULT 'generated',
    chapters   JSONB       NOT NULL DEFAULT '[]',
    md_path    TEXT,
    created_at TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_doc_documents_tenant_owner ON doc_documents (tenant_id, owner_id);
CREATE INDEX IF NOT EXISTS idx_doc_documents_task_id ON doc_documents (task_id, version);
