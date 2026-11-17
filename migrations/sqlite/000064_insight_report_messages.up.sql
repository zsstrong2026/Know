-- Migration: 000064_insight_report_messages（PostgreSQL 000161 的 SQLite 对应实现）
-- 类型映射：timestamptz→DATETIME、jsonb→TEXT、bigint→INTEGER。

CREATE TABLE IF NOT EXISTS insight_report_messages (
  id         VARCHAR(36) PRIMARY KEY,
  tenant_id  INTEGER     NOT NULL,
  owner_id   VARCHAR(36) NOT NULL,
  report_id  VARCHAR(36) NOT NULL,
  role       VARCHAR(16) NOT NULL,
  content    TEXT        NOT NULL DEFAULT '',
  refs_json  TEXT,
  created_at DATETIME    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_insight_report_messages_thread
  ON insight_report_messages (tenant_id, owner_id, report_id, created_at);
