-- Migration: 000138_workbench_assets
-- Description: 工作台条目附件表（P1-1 只建表，P2-3 才接服务与 UI）
-- 文件本体落在存储后端，这里只登记路径、哈希与最后引用时间。

CREATE TABLE IF NOT EXISTS workbench_item_assets (
  id            varchar(36) PRIMARY KEY,
  tenant_id     bigint      NOT NULL,
  owner_id      varchar(36) NOT NULL,
  item_id       varchar(36) NOT NULL,
  storage_path  text        NOT NULL,
  original_name text        NOT NULL,
  mime_type     varchar(128) NOT NULL,
  content_hash  varchar(64),
  size_bytes    bigint      NOT NULL DEFAULT 0,
  last_seen_at  timestamptz,
  created_at    timestamptz NOT NULL,
  deleted_at    timestamptz
);

CREATE INDEX IF NOT EXISTS idx_wia_item
  ON workbench_item_assets (tenant_id, owner_id, item_id)
  WHERE deleted_at IS NULL;
-- 同一附件重复粘贴时不重复落盘：按内容哈希去重（软删行不参与）
CREATE UNIQUE INDEX IF NOT EXISTS uq_wia_item_hash
  ON workbench_item_assets (tenant_id, owner_id, item_id, content_hash)
  WHERE content_hash IS NOT NULL AND deleted_at IS NULL;
