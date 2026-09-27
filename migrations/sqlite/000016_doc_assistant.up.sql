-- Mirrors versioned migration 000093_doc_assistant: 文档助手三张表（SQLite 用 TEXT 存 JSON）

-- 文档模板（个人私有）
CREATE TABLE IF NOT EXISTS doc_templates (
    id            VARCHAR(36)  PRIMARY KEY,
    tenant_id     INTEGER      NOT NULL,
    owner_id      VARCHAR(64)  NOT NULL,
    name          VARCHAR(200) NOT NULL,
    description   TEXT,
    global_config TEXT         NOT NULL DEFAULT '{}',
    chapters      TEXT         NOT NULL DEFAULT '[]',
    created_at    DATETIME     DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME     DEFAULT CURRENT_TIMESTAMP,
    deleted_at    DATETIME
);

CREATE INDEX IF NOT EXISTS idx_doc_templates_tenant_owner ON doc_templates (tenant_id, owner_id);
CREATE INDEX IF NOT EXISTS idx_doc_templates_deleted_at ON doc_templates (deleted_at);

-- 生成任务
CREATE TABLE IF NOT EXISTS doc_generation_tasks (
    id                 VARCHAR(36)  PRIMARY KEY,
    tenant_id          INTEGER      NOT NULL,
    owner_id           VARCHAR(64)  NOT NULL,
    template_id        VARCHAR(36)  NOT NULL,
    template_snapshot  TEXT,
    kb_ids             TEXT         NOT NULL DEFAULT '[]',
    kb_filters         TEXT,
    model_id           VARCHAR(100),
    theme              TEXT         NOT NULL,
    extra_requirement  TEXT,
    status             VARCHAR(32)  NOT NULL DEFAULT 'pending',
    progress           TEXT,
    error              TEXT,
    created_at         DATETIME     DEFAULT CURRENT_TIMESTAMP,
    finished_at        DATETIME
);

CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_tenant_owner ON doc_generation_tasks (tenant_id, owner_id);
CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_template_id ON doc_generation_tasks (template_id);
CREATE INDEX IF NOT EXISTS idx_doc_generation_tasks_status ON doc_generation_tasks (status);

-- 生成文档（版本化）
CREATE TABLE IF NOT EXISTS doc_documents (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  INTEGER     NOT NULL,
    owner_id   VARCHAR(64) NOT NULL,
    task_id    VARCHAR(36) NOT NULL,
    version    INTEGER     NOT NULL DEFAULT 1,
    status     VARCHAR(32) NOT NULL DEFAULT 'generated',
    chapters   TEXT        NOT NULL DEFAULT '[]',
    md_path    TEXT,
    created_at DATETIME    DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_doc_documents_tenant_owner ON doc_documents (tenant_id, owner_id);
CREATE INDEX IF NOT EXISTS idx_doc_documents_task_id ON doc_documents (task_id, version);
