-- Migration: 000065_insight_self_messages（PostgreSQL 000162 的 SQLite 对应实现）
-- 类型映射：timestamptz→DATETIME、bigint→INTEGER。

CREATE TABLE IF NOT EXISTS insight_self_messages (
  id         VARCHAR(36) PRIMARY KEY,
  tenant_id  INTEGER     NOT NULL,
  owner_id   VARCHAR(36) NOT NULL,
  role       VARCHAR(16) NOT NULL,
  content    TEXT        NOT NULL DEFAULT '',
  created_at DATETIME    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_self_messages_thread
  ON insight_self_messages (tenant_id, owner_id, created_at);
