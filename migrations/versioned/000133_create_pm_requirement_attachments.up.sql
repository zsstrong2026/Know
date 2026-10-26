-- 需求附件：文件字节直存数据库（BYTEA），文本类附件额外抽取纯文本供 AI 分析上下文使用。
CREATE TABLE pm_requirement_attachments (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    requirement_id VARCHAR(36) NOT NULL,
    file_name VARCHAR(255) NOT NULL DEFAULT '',
    content_type VARCHAR(128) NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    text_content TEXT NOT NULL DEFAULT '',                -- 纯文本类附件正文，仅用于 AI 上下文
    data BYTEA NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_pm_requirement_attachments_req ON pm_requirement_attachments (tenant_id, requirement_id, created_at);
