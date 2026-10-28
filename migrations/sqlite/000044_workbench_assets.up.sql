-- Migration: 000044_workbench_assets
-- Description: 对齐 versioned 000138：工作台条目附件表（P1-1 只建表）

CREATE TABLE IF NOT EXISTS workbench_item_assets (
  id            VARCHAR(36)  PRIMARY KEY,
  tenant_id     INTEGER      NOT NULL,
  owner_id      VARCHAR(36)  NOT NULL,
  item_id       VARCHAR(36)  NOT NULL,
  storage_path  TEXT         NOT NULL,
  original_name TEXT         NOT NULL,
  mime_type     TEXT         NOT NULL,
  content_hash  TEXT,
  size_bytes    INTEGER      NOT NULL DEFAULT 0,
  last_seen_at  DATETIME,
  created_at    DATETIME     NOT NULL,
  deleted_at    DATETIME
);

CREATE INDEX IF NOT EXISTS idx_wia_item
  ON workbench_item_assets (tenant_id, owner_id, item_id)
  WHERE deleted_at IS NULL;
-- 同一附件重复粘贴时不重复落盘：按内容哈希去重（软删行不参与）
CREATE UNIQUE INDEX IF NOT EXISTS uq_wia_item_hash
  ON workbench_item_assets (tenant_id, owner_id, item_id, content_hash)
  WHERE content_hash IS NOT NULL AND deleted_at IS NULL;
