-- Migration: 000168_sop_item_extras
-- Description: SOP 工作台：事项澄清问答与事项活动日志（只增不改不删）
--
-- 归属口径：tenant_id 贯穿且所有查询强制过滤。
-- 澄清只允许 open→answered 单向迁移（answered_at 记首次回答时间），其余字段不可改。
-- 活动只增不改不删，故无 deleted_at 列（spec §3.9）；kind ∈ business/audit。

CREATE TABLE IF NOT EXISTS sop_item_clarifications (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    question TEXT NOT NULL,
    answer TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'open',
    source VARCHAR(16) NOT NULL DEFAULT 'ai',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    answered_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_sop_clarifications_item ON sop_item_clarifications(item_id, status, deleted_at);

CREATE TABLE IF NOT EXISTS sop_item_activities (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    actor VARCHAR(200) NOT NULL DEFAULT '',
    action VARCHAR(100) NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_sop_activities_item ON sop_item_activities(item_id, kind, created_at);
