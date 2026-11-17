-- Migration: 000066_insight_privacy（PostgreSQL 000163 的 SQLite 对应实现）
-- 类型映射：timestamptz→DATETIME、bigint→INTEGER。

CREATE TABLE IF NOT EXISTS insight_privacy (
  tenant_id     INTEGER     NOT NULL,
  owner_id      VARCHAR(36) NOT NULL,
  settings_json TEXT        NOT NULL DEFAULT '{}',
  updated_at    DATETIME    NOT NULL,
  PRIMARY KEY (tenant_id, owner_id)
);
