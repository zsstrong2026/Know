-- Migration: 000162_insight_self_messages
-- Description: 自我洞悉说明书内追问的会话消息（仅说明书 + 主题报告摘要上下文）
--
-- 自我洞悉是覆盖更新的单人单份快照，因此归属直接用 tenant_id + owner_id，
-- 不挂 self_id；重新生成时由服务层清空旧会话，避免追问残留旧结论。

CREATE TABLE IF NOT EXISTS insight_self_messages (
  id         varchar(36) PRIMARY KEY,
  tenant_id  bigint      NOT NULL,
  owner_id   varchar(36) NOT NULL,
  role       varchar(16) NOT NULL,
  content    text        NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_self_messages_thread
  ON insight_self_messages (tenant_id, owner_id, created_at);
