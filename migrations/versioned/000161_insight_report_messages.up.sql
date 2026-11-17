-- Migration: 000161_insight_report_messages
-- Description: 洞察报告内追问的会话消息（仅报告 + 证据集上下文）
--
-- 归属口径与报告一致：tenant_id + owner_id，管理员不可读。
-- 报告删除时由服务层同步清理，避免留下孤儿消息。

CREATE TABLE IF NOT EXISTS insight_report_messages (
  id         varchar(36) PRIMARY KEY,
  tenant_id  bigint      NOT NULL,
  owner_id   varchar(36) NOT NULL,
  report_id  varchar(36) NOT NULL,
  role       varchar(16) NOT NULL,
  content    text        NOT NULL DEFAULT '',
  refs_json  jsonb,
  created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_report_messages_thread
  ON insight_report_messages (tenant_id, owner_id, report_id, created_at);
