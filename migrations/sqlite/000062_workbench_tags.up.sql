-- Migration: 000062_workbench_tags（PostgreSQL 000158 的 SQLite 对应实现）
CREATE TABLE IF NOT EXISTS workbench_tags (
  id         VARCHAR(36) PRIMARY KEY,
  tenant_id  INTEGER     NOT NULL,
  owner_id   VARCHAR(36) NOT NULL,
  name       VARCHAR(40) NOT NULL,
  color      VARCHAR(16) NOT NULL DEFAULT 'gray',
  created_at DATETIME    NOT NULL,
  updated_at DATETIME    NOT NULL,
  deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wt_owner_name
  ON workbench_tags (tenant_id, owner_id, name)
  WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS workbench_item_tags (
  tenant_id  INTEGER     NOT NULL,
  owner_id   VARCHAR(36) NOT NULL,
  item_id    VARCHAR(36) NOT NULL,
  tag_id     VARCHAR(36) NOT NULL,
  created_at DATETIME    NOT NULL,
  PRIMARY KEY (item_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_wit_tag
  ON workbench_item_tags (tenant_id, owner_id, tag_id);
